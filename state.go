package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/updates"
)

type channelState struct {
	Pts    int  `json:"pts"`
	HasPts bool `json:"has_pts"`
}

type protocolData struct {
	UserID   int64                  `json:"user_id"`
	State    *updates.State         `json:"state,omitempty"`
	Channels map[int64]channelState `json:"channels"`
}

type protocolStore struct {
	mu      sync.Mutex
	path    string
	store   *sqliteStore
	index   *sqliteStore
	data    protocolData
	failure *failure
}

var _ updates.StateStorage = (*protocolStore)(nil)
var _ updates.ChannelAccessHasher = (*protocolStore)(nil)

func openProtocolStore(path string, fail *failure) (*protocolStore, error) {
	s := &protocolStore{path: path, failure: fail, data: protocolData{Channels: map[int64]channelState{}}}
	store, err := openStateSQLite(path)
	if err != nil {
		return nil, err
	}
	s.store = store
	s.index, err = openSQLite(filepath.Join(filepath.Dir(path), "archive", "index.sqlite"))
	if err != nil {
		store.Close()
		return nil, err
	}
	if err := store.ReadJSON("updates", &s.data); err != nil && !os.IsNotExist(err) {
		_ = s.Close()
		return nil, err
	}
	if s.data.Channels == nil {
		_ = s.Close()
		return nil, errors.New("invalid updates state")
	}
	return s, nil
}

func (s *protocolStore) Close() error {
	return errors.Join(s.store.Close(), s.index.Close())
}

func (s *protocolStore) bind(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.UserID != 0 && s.data.UserID != id {
		return errors.New("updates state belongs to another account")
	}
	s.data.UserID = id
	return s.save()
}

func (s *protocolStore) save() error {
	if err := s.failure.check(); err != nil {
		return err
	}
	return s.failure.report(s.store.WriteJSON("updates", s.data))
}

func (s *protocolStore) checkUser(id int64) error {
	if id != s.data.UserID {
		return errors.New("updates account mismatch")
	}
	return s.failure.check()
}

func (s *protocolStore) GetState(_ context.Context, id int64) (updates.State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return updates.State{}, false, err
	}
	if s.data.State == nil {
		return updates.State{}, false, nil
	}
	return *s.data.State, true, nil
}

func (s *protocolStore) SetState(_ context.Context, id int64, state updates.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return err
	}
	s.data.State = &state
	return s.save()
}

func (s *protocolStore) changeState(id int64, fn func(*updates.State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return err
	}
	if s.data.State == nil {
		return errors.New("updates state not initialized")
	}
	fn(s.data.State)
	return s.save()
}

func (s *protocolStore) SetPts(_ context.Context, id int64, n int) error {
	return s.changeState(id, func(st *updates.State) { st.Pts = n })
}
func (s *protocolStore) SetQts(_ context.Context, id int64, n int) error {
	return s.changeState(id, func(st *updates.State) { st.Qts = n })
}
func (s *protocolStore) SetDate(_ context.Context, id int64, n int) error {
	return s.changeState(id, func(st *updates.State) { st.Date = n })
}
func (s *protocolStore) SetSeq(_ context.Context, id int64, n int) error {
	return s.changeState(id, func(st *updates.State) { st.Seq = n })
}
func (s *protocolStore) SetDateSeq(_ context.Context, id int64, date, seq int) error {
	return s.changeState(id, func(st *updates.State) { st.Date = date; st.Seq = seq })
}

func (s *protocolStore) GetChannelPts(_ context.Context, id, ch int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return 0, false, err
	}
	c := s.data.Channels[ch]
	return c.Pts, c.HasPts, nil
}

func (s *protocolStore) SetChannelPts(_ context.Context, id, ch int64, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return err
	}
	c := s.data.Channels[ch]
	c.Pts = n
	c.HasPts = true
	s.data.Channels[ch] = c
	return s.save()
}

func (s *protocolStore) ForEachChannels(ctx context.Context, id int64, fn func(context.Context, int64, int) error) error {
	s.mu.Lock()
	if err := s.checkUser(id); err != nil {
		s.mu.Unlock()
		return err
	}
	copy := make(map[int64]channelState, len(s.data.Channels))
	for id, c := range s.data.Channels {
		copy[id] = c
	}
	s.mu.Unlock()
	for id, c := range copy {
		if c.HasPts {
			if err := fn(ctx, id, c.Pts); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *protocolStore) SetChannelAccessHash(_ context.Context, id, ch, hash int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return err
	}
	if hash == 0 {
		return nil
	}
	var meta archiveMeta
	err := s.store.ReadJSON("archive_metadata", &meta)
	if err != nil && !os.IsNotExist(err) {
		return s.failure.report(err)
	}
	if meta.Blocked[fmt.Sprintf("channel-%d", ch)] != "" {
		return nil
	}
	return s.failure.report(s.index.UpsertPeer(fmt.Sprintf("channel-%d", ch), PeerInfo{Kind: "channel", ID: ch, AccessHash: hash}, false))
}

func (s *protocolStore) GetChannelAccessHash(_ context.Context, id, ch int64) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUser(id); err != nil {
		return 0, false, err
	}
	var hash int64
	err := s.index.db.QueryRow("SELECT access_hash FROM peers WHERE peer=?", fmt.Sprintf("channel-%d", ch)).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return hash, hash != 0, err
}

type stableSession struct {
	mu             sync.Mutex
	path           string
	failure        *failure
	ignoreExisting bool
}

func (s *stableSession) LoadSession(_ context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ignoreExisting {
		s.ignoreExisting = false
		return nil, session.ErrNotFound
	}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, session.ErrNotFound
	}
	return b, err
}

func (s *stableSession) StoreSession(_ context.Context, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure.report(atomicWrite(s.path, b))
}
