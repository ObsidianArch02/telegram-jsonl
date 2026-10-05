package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Record struct {
	Schema     int           `json:"schema"`
	AccountID  int64         `json:"account_id"`
	Peer       string        `json:"peer"`
	MessageID  int           `json:"message_id"`
	Sender     string        `json:"sender,omitempty"`
	Date       time.Time     `json:"date"`
	EditedAt   *time.Time    `json:"edited_at,omitempty"`
	Outgoing   bool          `json:"outgoing"`
	Text       string        `json:"text"`
	ReplyTo    int           `json:"reply_to,omitempty"`
	MediaType  string        `json:"media_type,omitempty"`
	MessageURL string        `json:"message_url,omitempty"`
	AlbumID    string        `json:"album_id,omitempty"`
	Media      *MediaDetails `json:"media,omitempty"`
}

type PeerInfo struct {
	Kind         string `json:"kind"`
	ID           int64  `json:"id"`
	Name         string `json:"name,omitempty"`
	Username     string `json:"username,omitempty"`
	AccessHash   int64  `json:"access_hash,omitempty"`
	Offset       int    `json:"history_offset"`
	HistoryDone  bool   `json:"history_done"`
	Newest       int    `json:"newest_history_id"`
	HistorySince int64  `json:"history_since_unix"`
}

type archiveMeta struct {
	AccountID         int64                   `json:"account_ref"`
	Peers             map[string]PeerInfo     `json:"peers"`
	Deleted           map[string]map[int]bool `json:"deleted"`
	NonChannelDeleted map[int]bool            `json:"non_channel_deleted"`
	Blocked           map[string]string       `json:"blocked"`
}

type archive struct {
	mu         sync.Mutex
	dir        string
	meta       archiveMeta
	rows       map[string]map[int]Record
	generation uint64
	versions   map[string]map[int]uint64
	failure    *failure
	index      *sqliteStore
	state      *sqliteStore
}

var validPeer = regexp.MustCompile(`^(user|chat|channel)-[1-9][0-9]*$`)

func openArchive(dir string, fail *failure) (*archive, error) {
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	a := &archive{dir: dir, rows: make(map[string]map[int]Record), versions: make(map[string]map[int]uint64), failure: fail}
	a.meta = archiveMeta{Peers: map[string]PeerInfo{}, Deleted: map[string]map[int]bool{}, NonChannelDeleted: map[int]bool{}, Blocked: map[string]string{}}
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(a.metaPath()); os.IsNotExist(err) && len(files) > 0 {
		return nil, errors.New("existing JSONL requires SQLite archive metadata; use a fresh --data directory, existing exports are unchanged")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	a.index, err = openSQLite(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		return nil, err
	}
	a.state, err = openStateSQLite(a.metaPath())
	if err != nil {
		a.index.Close()
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			_ = a.Close()
		}
	}()
	err = a.state.ReadJSON("archive_metadata", &a.meta)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) && len(files) > 0 {
		return nil, errors.New("SQLite archive metadata missing for existing JSONL; use a fresh --data directory")
	}
	if a.meta.Peers == nil || a.meta.Deleted == nil || a.meta.NonChannelDeleted == nil || a.meta.Blocked == nil {
		return nil, errors.New("invalid archive metadata")
	}
	if err == nil {
		if err := joinArchiveProperties(a.index, &a.meta); err != nil {
			return nil, err
		}
	}
	dirty, err := a.pendingIntents()
	if err != nil {
		return nil, err
	}
	// A crash can leave an unfinished replacement containing an old message body.
	temps, err := filepath.Glob(filepath.Join(dir, ".write-*"))
	if err != nil {
		return nil, err
	}
	for _, path := range temps {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	for _, path := range files {
		peer := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !validPeer.MatchString(peer) {
			return nil, fmt.Errorf("invalid archive filename: %s", filepath.Base(path))
		}
		if _, ok := a.meta.Peers[peer]; !ok {
			return nil, fmt.Errorf("archive has no metadata: %s", peer)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 4*1024*1024)
		a.rows[peer] = map[int]Record{}
		purge := false
		for scanner.Scan() {
			var r Record
			if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("invalid JSONL in %s: %w", peer, err)
			}
			if r.Peer != peer || r.MessageID <= 0 || r.AccountID != a.meta.AccountID {
				_ = f.Close()
				return nil, errors.New("archive identity mismatch")
			}
			if a.excluded(peer, r.MessageID) {
				purge = true
				continue
			}
			a.rows[peer][r.MessageID] = r
		}
		scanErr := scanner.Err()
		_ = f.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if purge {
			if err := a.flush(peer); err != nil {
				return nil, err
			}
			delete(dirty, peer)
		}
	}
	// Only interrupted replacements need projection recovery; clean properties
	// are not rewritten when the daemon starts.
	for key := range dirty {
		if a.meta.Blocked[key] != "" {
			if err := a.index.RemovePeer(key); err != nil {
				return nil, err
			}
		} else if err := a.index.SyncMessages(key, a.rows[key]); err != nil {
			return nil, err
		}
		if err := a.clearIntent(key); err != nil {
			return nil, err
		}
	}
	for key := range a.meta.Blocked {
		if err := a.index.RemovePeer(key); err != nil {
			return nil, err
		}
	}
	opened = true
	return a, nil
}

func (a *archive) Close() error { return errors.Join(a.state.Close(), a.index.Close()) }
func (a *archive) metaPath() string {
	return filepath.Join(filepath.Dir(a.dir), "state.sqlite")
}

func (a *archive) hotMeta() archiveMeta {
	m := a.meta
	m.Peers = make(map[string]PeerInfo, len(a.meta.Peers))
	for key, p := range a.meta.Peers {
		p.Name, p.Username, p.AccessHash = "", "", 0
		m.Peers[key] = p
	}
	return m
}

func (a *archive) saveMeta() error {
	return a.failure.report(a.state.WriteJSON("archive_metadata", a.hotMeta()))
}
func (a *archive) excluded(peer string, id int) bool {
	return a.meta.Blocked[peer] != "" || a.meta.Deleted[peer][id] || (!strings.HasPrefix(peer, "channel-") && a.meta.NonChannelDeleted[id])
}

func (a *archive) bind(accountID int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.meta.AccountID != 0 && a.meta.AccountID != accountID {
		return errors.New("data directory belongs to a different account")
	}
	var bound int64
	err := a.index.ReadJSON("archive_account", &bound)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && bound != accountID {
		return errors.New("archive attributes belong to a different account")
	}
	a.meta.AccountID = accountID
	if err := a.failure.report(a.index.WriteJSON("archive_account", accountID)); err != nil {
		return err
	}
	return a.saveMeta()
}

func (a *archive) register(key string, p PeerInfo) error {
	if !validPeer.MatchString(key) || key != fmt.Sprintf("%s-%d", p.Kind, p.ID) {
		return errors.New("invalid peer")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if old, ok := a.meta.Peers[key]; ok {
		p.Offset = old.Offset
		p.HistoryDone = old.HistoryDone
		p.Newest = old.Newest
		p.HistorySince = old.HistorySince
		if p.AccessHash == 0 {
			p.AccessHash = old.AccessHash
		}
		if p.Name == "" {
			p.Name, p.Username = old.Name, old.Username
		}
	}
	if a.meta.Peers[key] == p {
		return nil
	}
	a.meta.Peers[key] = p
	if a.meta.Blocked[key] != "" {
		return a.saveMeta()
	}
	if err := a.failure.report(a.index.UpsertPeer(key, p, true)); err != nil {
		return err
	}
	return a.saveMeta()
}

func (a *archive) observePeer(key string, p PeerInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.meta.Blocked[key] != "" {
		return nil
	}
	return a.failure.report(a.index.UpsertPeer(key, p, false))
}

func (a *archive) peers() map[string]PeerInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]PeerInfo, len(a.meta.Peers))
	for k, p := range a.meta.Peers {
		if a.meta.Blocked[k] == "" {
			out[k] = p
		}
	}
	return out
}

func (a *archive) counts() (peers, records int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, rows := range a.rows {
		records += len(rows)
	}
	return len(a.meta.Peers), records
}

func (a *archive) peer(key string) (PeerInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.meta.Peers[key]
	return p, ok
}

func (a *archive) blocked(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.meta.Blocked[key] != ""
}

func (a *archive) checkpoint(peer string, offset int, done bool, newest int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.meta.Peers[peer]
	p.Offset = offset
	p.HistoryDone = done
	if newest > p.Newest {
		p.Newest = newest
	}
	a.meta.Peers[peer] = p
	return a.saveMeta()
}

// A wider history window must revisit the old lower boundary; narrowing keeps
// completed progress and never removes existing archive contents.
func (a *archive) setHistoryScope(since int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for key, p := range a.meta.Peers {
		if p.HistorySince == since {
			continue
		}
		if since < p.HistorySince {
			p.Offset = 0
			p.HistoryDone = false
		}
		p.HistorySince = since
		a.meta.Peers[key] = p
		changed = true
	}
	if changed {
		return a.saveMeta()
	}
	return nil
}

func (a *archive) fence() uint64 { a.mu.Lock(); defer a.mu.Unlock(); return a.generation }

func (a *archive) flush(peer string) error {
	ids := make([]int, 0, len(a.rows[peer]))
	for id := range a.rows[peer] {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, id := range ids {
		if err := enc.Encode(a.rows[peer][id]); err != nil {
			return a.failure.report(err)
		}
	}
	sum := sha256.Sum256(b.Bytes())
	if err := a.failure.report(a.state.WriteJSON("jsonl_intent:"+peer, jsonlIntent{ExpectedSHA256: hex.EncodeToString(sum[:])})); err != nil {
		return err
	}
	if err := a.failure.report(atomicWrite(filepath.Join(a.dir, peer+".jsonl"), b.Bytes())); err != nil {
		return err
	}
	if err := a.failure.report(a.index.SyncMessages(peer, a.rows[peer])); err != nil {
		return err
	}
	return a.clearIntent(peer)
}

type jsonlIntent struct {
	ExpectedSHA256 string `json:"expected_sha256"`
}

func (a *archive) pendingIntents() (map[string]jsonlIntent, error) {
	rows, err := a.state.db.Query("SELECT key,value_json FROM state WHERE key LIKE 'jsonl_intent:%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]jsonlIntent)
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		peer := strings.TrimPrefix(key, "jsonl_intent:")
		var intent jsonlIntent
		if err := json.Unmarshal(value, &intent); err != nil {
			return nil, errors.New("invalid JSONL replacement receipt")
		}
		digest, err := hex.DecodeString(intent.ExpectedSHA256)
		if !validPeer.MatchString(peer) || len(digest) != sha256.Size || err != nil {
			return nil, errors.New("invalid JSONL replacement receipt")
		}
		if _, ok := a.meta.Peers[peer]; !ok {
			return nil, errors.New("replacement receipt has no peer metadata")
		}
		out[peer] = intent
	}
	return out, rows.Err()
}

func (a *archive) clearIntent(peer string) error {
	return a.failure.report(a.state.WriteJSONBatch(nil, []string{"jsonl_intent:" + peer}))
}

func (a *archive) touch(peer string, id int) {
	a.generation++
	if a.versions[peer] == nil {
		a.versions[peer] = map[int]uint64{}
	}
	a.versions[peer][id] = a.generation
}

// fence distinguishes an old HTTP/RPC snapshot from edits received while it was in flight.
func (a *archive) upsert(records []Record, live bool, fence uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure.check(); err != nil {
		return err
	}
	changed := map[string][2]int{}
	for _, r := range records {
		if !validPeer.MatchString(r.Peer) || r.MessageID <= 0 || r.AccountID != a.meta.AccountID {
			return a.failure.report(errors.New("invalid record identity"))
		}
		if _, ok := a.meta.Peers[r.Peer]; !ok {
			return a.failure.report(errors.New("unregistered peer"))
		}
		if a.excluded(r.Peer, r.MessageID) || (!live && a.versions[r.Peer][r.MessageID] > fence) {
			continue
		}
		if a.rows[r.Peer] == nil {
			a.rows[r.Peer] = map[int]Record{}
		}
		old, exists := a.rows[r.Peer][r.MessageID]
		if exists && old.EditedAt != nil && (r.EditedAt == nil || old.EditedAt.After(*r.EditedAt)) {
			continue
		}
		if live {
			a.touch(r.Peer, r.MessageID)
		}
		if exists && recordsEqual(old, r) {
			continue
		}
		a.rows[r.Peer][r.MessageID] = r
		counts := changed[r.Peer]
		if exists {
			counts[1]++
		} else {
			counts[0]++
		}
		changed[r.Peer] = counts
	}
	for peer, counts := range changed {
		if err := a.flush(peer); err != nil {
			return err
		}
		logPrintf("JSONL saved: peer=%s added=%d updated=%d total=%d live=%t", peer, counts[0], counts[1], len(a.rows[peer]), live)
	}
	return nil
}

func recordsEqual(a, b Record) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// Persist body-free tombstones before purging files, so crash recovery cannot resurrect content.
func (a *archive) remove(peer string, ids []int, live bool, fence uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure.check(); err != nil {
		return err
	}
	if peer != "" && !validPeer.MatchString(peer) {
		return errors.New("invalid peer")
	}
	accepted := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if peer != "" && !live && a.versions[peer][id] > fence {
			continue
		}
		accepted = append(accepted, id)
		if peer == "" {
			a.meta.NonChannelDeleted[id] = true
		} else {
			if a.meta.Deleted[peer] == nil {
				a.meta.Deleted[peer] = map[int]bool{}
			}
			a.meta.Deleted[peer][id] = true
		}
	}
	if len(accepted) == 0 {
		return nil
	}
	if err := a.saveMeta(); err != nil {
		return err
	}
	for key, rows := range a.rows {
		if peer != "" && key != peer {
			continue
		}
		if peer == "" && strings.HasPrefix(key, "channel-") {
			continue
		}
		removed := 0
		for _, id := range accepted {
			if live {
				a.touch(key, id)
			}
			if _, ok := rows[id]; ok {
				delete(rows, id)
				removed++
			}
		}
		if removed > 0 {
			if err := a.flush(key); err != nil {
				return err
			}
			logPrintf("JSONL deletion saved: peer=%s removed=%d total=%d live=%t", key, removed, len(rows), live)
		}
	}
	return nil
}

func (a *archive) block(peer, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validPeer.MatchString(peer) {
		return errors.New("invalid peer")
	}
	if a.meta.Blocked[peer] != "" {
		return nil
	}
	a.meta.Blocked[peer] = reason
	if err := a.saveMeta(); err != nil {
		return err
	}
	if err := a.failure.report(a.index.RemovePeer(peer)); err != nil {
		return err
	}
	removed := len(a.rows[peer])
	if removed > 0 {
		a.rows[peer] = map[int]Record{}
		if err := a.flush(peer); err != nil {
			return err
		}
	}
	logPrintf("Archive peer excluded: peer=%s removed=%d reason=%s", peer, removed, reason)
	return nil
}

func (a *archive) ids(peer string) []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int, 0, len(a.rows[peer]))
	for id := range a.rows[peer] {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}
