package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type updateHandlerFunc func(context.Context, tg.UpdatesClass) error

func (f updateHandlerFunc) Handle(ctx context.Context, u tg.UpdatesClass) error { return f(ctx, u) }

type receiver struct {
	archive       *archive
	protocol      *protocolStore
	self          int64
	api           *tg.Client
	batch         int
	failure       *failure
	resync        chan struct{}
	entitiesMu    sync.Mutex
	entities      map[string]PeerInfo
	historyPolicy historyConfig
}

func peerKey(p tg.PeerClass) string {
	switch p := p.(type) {
	case *tg.PeerUser:
		return fmt.Sprintf("user-%d", p.UserID)
	case *tg.PeerChat:
		return fmt.Sprintf("chat-%d", p.ChatID)
	case *tg.PeerChannel:
		return fmt.Sprintf("channel-%d", p.ChannelID)
	default:
		return ""
	}
}

func inputPeer(p PeerInfo) tg.InputPeerClass {
	switch p.Kind {
	case "user":
		return &tg.InputPeerUser{UserID: p.ID, AccessHash: p.AccessHash}
	case "chat":
		return &tg.InputPeerChat{ChatID: p.ID}
	case "channel":
		return &tg.InputPeerChannel{ChannelID: p.ID, AccessHash: p.AccessHash}
	default:
		return &tg.InputPeerEmpty{}
	}
}

func (r *receiver) remember(key string, p PeerInfo) error {
	r.entitiesMu.Lock()
	if r.entities == nil {
		r.entities = map[string]PeerInfo{}
	}
	if old, ok := r.entities[key]; ok && p.AccessHash == 0 {
		p.AccessHash = old.AccessHash
	}
	r.entities[key] = p
	r.entitiesMu.Unlock()
	// Entity lists also contain group senders; they are not all account dialogs.
	if _, ok := r.archive.peer(key); ok {
		return r.archive.register(key, p)
	}
	return nil
}

func (r *receiver) remembered(key string, p PeerInfo) PeerInfo {
	r.entitiesMu.Lock()
	defer r.entitiesMu.Unlock()
	if cached, ok := r.entities[key]; ok {
		return cached
	}
	return p
}

func (r *receiver) observe(users []tg.UserClass, chats []tg.ChatClass) error {
	for _, u := range users {
		if u, ok := u.(*tg.User); ok && !u.Min {
			if err := r.remember(fmt.Sprintf("user-%d", u.ID), PeerInfo{Kind: "user", ID: u.ID, AccessHash: u.AccessHash}); err != nil {
				return err
			}
		}
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Chat:
			key := fmt.Sprintf("chat-%d", c.ID)
			if err := r.remember(key, PeerInfo{Kind: "chat", ID: c.ID}); err != nil {
				return err
			}
			if c.Noforwards {
				if err := r.archive.block(key, "protected content"); err != nil {
					return err
				}
			}
		case *tg.Channel:
			key := fmt.Sprintf("channel-%d", c.ID)
			hash := c.AccessHash
			if c.Min {
				hash = 0
			}
			if err := r.remember(key, PeerInfo{Kind: "channel", ID: c.ID, AccessHash: hash}); err != nil {
				return err
			}
			if !c.Min && hash != 0 {
				if err := r.protocol.SetChannelAccessHash(context.Background(), r.self, c.ID, hash); err != nil {
					return err
				}
			}
			if c.Noforwards {
				if err := r.archive.block(key, "protected content"); err != nil {
					return err
				}
			}
		case *tg.ChannelForbidden:
			if err := r.archive.block(fmt.Sprintf("channel-%d", c.ID), "access revoked"); err != nil {
				return err
			}
		case *tg.ChatForbidden:
			if err := r.archive.block(fmt.Sprintf("chat-%d", c.ID), "access revoked"); err != nil {
				return err
			}
		}
	}
	return nil
}

func ephemeral(m *tg.Message) bool {
	if m.TTLPeriod != 0 || m.Noforwards || len(m.RestrictionReason) > 0 {
		return true
	}
	switch media := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		if media.TTLSeconds != 0 || media.Photo == nil {
			return true
		}
		_, empty := media.Photo.(*tg.PhotoEmpty)
		return empty
	case *tg.MessageMediaDocument:
		if media.TTLSeconds != 0 || media.Document == nil {
			return true
		}
		_, empty := media.Document.(*tg.DocumentEmpty)
		return empty
	case *tg.MessageMediaPaidMedia:
		return true
	default:
		return false
	}
}

func (r *receiver) records(messages []tg.MessageClass, live bool, fence uint64) error {
	rows := make([]Record, 0, len(messages))
	for _, item := range messages {
		m, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		key := peerKey(m.PeerID)
		if key == "" {
			continue
		}
		if ephemeral(m) {
			if err := r.archive.remove(key, []int{m.ID}, live, fence); err != nil {
				return err
			}
			continue
		}
		p := PeerInfo{}
		switch peer := m.PeerID.(type) {
		case *tg.PeerUser:
			p = PeerInfo{Kind: "user", ID: peer.UserID}
		case *tg.PeerChat:
			p = PeerInfo{Kind: "chat", ID: peer.ChatID}
		case *tg.PeerChannel:
			p = PeerInfo{Kind: "channel", ID: peer.ChannelID}
		}
		if err := r.archive.register(key, r.remembered(key, p)); err != nil {
			return err
		}
		row := Record{Schema: 2, AccountID: r.self, Peer: key, MessageID: m.ID, Sender: peerKey(m.FromID), Date: time.Unix(int64(m.Date), 0).UTC(), Outgoing: m.Out, Text: m.Message, MessageURL: messageURL(m), Media: mediaDetails(m.Media)}
		if m.GroupedID != 0 {
			row.AlbumID = strconv.FormatInt(m.GroupedID, 10)
		}
		if row.Sender == "" {
			if m.Out {
				row.Sender = fmt.Sprintf("user-%d", r.self)
			} else if p.Kind == "user" {
				row.Sender = key
			}
		}
		if m.EditDate != 0 {
			t := time.Unix(int64(m.EditDate), 0).UTC()
			row.EditedAt = &t
		}
		if reply, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			row.ReplyTo = reply.ReplyToMsgID
		}
		if media, ok := m.Media.(interface{ TypeName() string }); ok {
			row.MediaType = media.TypeName()
		}
		rows = append(rows, row)
	}
	return r.archive.upsert(rows, live, fence)
}

func (r *receiver) handler() telegram.UpdateHandler {
	d := tg.NewUpdateDispatcher()
	d.OnNewMessage(func(_ context.Context, _ tg.Entities, u *tg.UpdateNewMessage) error {
		return r.records([]tg.MessageClass{u.Message}, true, 0)
	})
	d.OnNewChannelMessage(func(_ context.Context, _ tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return r.records([]tg.MessageClass{u.Message}, true, 0)
	})
	d.OnEditMessage(func(_ context.Context, _ tg.Entities, u *tg.UpdateEditMessage) error {
		return r.records([]tg.MessageClass{u.Message}, true, 0)
	})
	d.OnEditChannelMessage(func(_ context.Context, _ tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return r.records([]tg.MessageClass{u.Message}, true, 0)
	})
	d.OnDeleteMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteMessages) error {
		return r.archive.remove("", u.Messages, true, 0)
	})
	d.OnDeleteChannelMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteChannelMessages) error {
		return r.archive.remove(fmt.Sprintf("channel-%d", u.ChannelID), u.Messages, true, 0)
	})
	d.OnPeerHistoryTTL(func(_ context.Context, _ tg.Entities, u *tg.UpdatePeerHistoryTTL) error {
		if u.TTLPeriod > 0 {
			return r.archive.block(peerKey(u.Peer), "automatic deletion enabled")
		}
		return nil
	})
	d.OnMessagePoll(func(_ context.Context, _ tg.Entities, u *tg.UpdateMessagePoll) error { return r.archive.updatePoll(u) })
	return updateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
		if err := r.failure.check(); err != nil {
			return err
		}
		var users []tg.UserClass
		var chats []tg.ChatClass
		switch u := u.(type) {
		case *tg.Updates:
			users = u.Users
			chats = u.Chats
		case *tg.UpdatesCombined:
			users = u.Users
			chats = u.Chats
		}
		if err := r.observe(users, chats); err != nil {
			return r.failure.report(err)
		}
		return r.failure.report(d.Handle(ctx, u))
	})
}

type messageResult interface {
	GetMessages() []tg.MessageClass
	GetChats() []tg.ChatClass
	GetUsers() []tg.UserClass
}

func unpack(result tg.MessagesMessagesClass) (messageResult, error) {
	r, ok := result.(messageResult)
	if !ok {
		return nil, fmt.Errorf("unexpected messages response %T", result)
	}
	return r, nil
}

func (r *receiver) discover(ctx context.Context) error {
	// Explicitly enumerate both the main folder and Telegram's archived folder.
	for _, folder := range []int{0, 1} {
		iter := dialogs.NewQueryBuilder(r.api).GetDialogs().FolderID(folder).BatchSize(r.batch).Iter()
		for iter.Next(ctx) {
			d := iter.Value()
			key := peerKey(d.Dialog.GetPeer())
			if key == "" {
				continue
			}
			var p PeerInfo
			switch v := d.Peer.(type) {
			case *tg.InputPeerUser:
				p = PeerInfo{Kind: "user", ID: v.UserID, AccessHash: v.AccessHash}
			case *tg.InputPeerSelf:
				p = PeerInfo{Kind: "user", ID: r.self}
			case *tg.InputPeerChat:
				p = PeerInfo{Kind: "chat", ID: v.ChatID}
			case *tg.InputPeerChannel:
				p = PeerInfo{Kind: "channel", ID: v.ChannelID, AccessHash: v.AccessHash}
			default:
				continue
			}
			if err := r.archive.register(key, p); err != nil {
				return err
			}
			var chats []tg.ChatClass
			for _, c := range d.Entities.Chats() {
				chats = append(chats, c)
			}
			for _, c := range d.Entities.Channels() {
				chats = append(chats, c)
			}
			if err := r.observe(nil, chats); err != nil {
				return err
			}
		}
		if err := iter.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (r *receiver) reconcile(ctx context.Context, key string, p PeerInfo) error {
	ids := r.archive.ids(key)
	for start := 0; start < len(ids); start += r.batch {
		if r.archive.blocked(key) {
			return nil
		}
		end := min(start+r.batch, len(ids))
		batch := ids[start:end]
		input := make([]tg.InputMessageClass, len(batch))
		for i, id := range batch {
			input[i] = &tg.InputMessageID{ID: id}
		}
		fence := r.archive.fence()
		var result tg.MessagesMessagesClass
		var err error
		if p.Kind == "channel" {
			result, err = r.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash}, ID: input})
		} else {
			result, err = r.api.MessagesGetMessages(ctx, input)
		}
		if err != nil {
			return err
		}
		messages, err := unpack(result)
		if err != nil {
			return err
		}
		if err := r.observe(messages.GetUsers(), messages.GetChats()); err != nil {
			return err
		}
		seen := map[int]bool{}
		for _, m := range messages.GetMessages() {
			if m, ok := m.(*tg.Message); ok && peerKey(m.PeerID) == key {
				seen[m.ID] = true
			}
		}
		var deleted []int
		for _, id := range batch {
			if !seen[id] {
				deleted = append(deleted, id)
			}
		}
		if err := r.archive.remove(key, deleted, false, fence); err != nil {
			return err
		}
		if err := r.records(messages.GetMessages(), false, fence); err != nil {
			return err
		}
	}
	return nil
}

// Incremental scans commit their high-water mark only after the entire range is read.
func (r *receiver) history(ctx context.Context, key string, p PeerInfo, incremental bool) error {
	if r.historyPolicy.Disabled {
		return nil
	}
	offset, minID, newest := p.Offset, 0, p.Newest
	if incremental {
		offset = 0
		minID = p.Newest
	}
	for {
		if r.archive.blocked(key) {
			return nil
		}
		fence := r.archive.fence()
		peer := inputPeer(p)
		if p.Kind == "user" && p.ID == r.self {
			peer = &tg.InputPeerSelf{}
		}
		result, err := r.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offset, MinID: minID, Limit: r.batch})
		if err != nil {
			return err
		}
		messages, err := unpack(result)
		if err != nil {
			return err
		}
		if err := r.observe(messages.GetUsers(), messages.GetChats()); err != nil {
			return err
		}
		page := messages.GetMessages()
		eligible := make([]tg.MessageClass, 0, len(page))
		reachedBoundary := false
		for _, m := range page {
			if msg, ok := m.AsNotEmpty(); ok && !r.historyPolicy.Since.IsZero() && int64(msg.GetDate()) < r.historyPolicy.unix() {
				reachedBoundary = true
				continue
			}
			eligible = append(eligible, m)
		}
		if err := r.records(eligible, false, fence); err != nil {
			return err
		}
		next := 0
		for _, m := range messages.GetMessages() {
			id := m.GetID()
			if id <= minID {
				continue
			}
			if id > newest {
				newest = id
			}
			if next == 0 || id < next {
				next = id
			}
		}
		if next == 0 || reachedBoundary {
			if incremental {
				return r.archive.checkpoint(key, p.Offset, p.HistoryDone, newest)
			}
			return r.archive.checkpoint(key, offset, true, newest)
		}
		if offset != 0 && next >= offset {
			return errors.New("history pagination made no progress")
		}
		offset = next
		if !incremental {
			if err := r.archive.checkpoint(key, offset, false, newest); err != nil {
				return err
			}
		}
	}
}

func inaccessible(err error) bool {
	return tgerr.Is(err, "CHANNEL_PRIVATE", "CHAT_FORBIDDEN", "USER_BANNED_IN_CHANNEL")
}

func (r *receiver) sync(ctx context.Context) error {
	if err := r.discover(ctx); err != nil {
		return err
	}
	if !r.historyPolicy.Disabled {
		if err := r.archive.setHistoryScope(r.historyPolicy.unix()); err != nil {
			return err
		}
	}
	peers := r.archive.peers()
	keys := make([]string, 0, len(peers))
	for key := range peers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	log.Printf("Synchronizing %d accessible peers", len(keys))
	// Purge missed deletions and edits before spending time on the initial history backlog.
	for _, key := range keys {
		p := peers[key]
		if p.Kind != "chat" && p.AccessHash == 0 && p.ID != r.self {
			continue
		}
		if err := r.reconcile(ctx, key, p); err != nil {
			if inaccessible(err) {
				if err := r.archive.block(key, "access revoked"); err != nil {
					return err
				}
				delete(peers, key)
				continue
			}
			return fmt.Errorf("reconcile %s: %w", key, err)
		}
	}
	for _, key := range keys {
		p, ok := peers[key]
		if !ok {
			continue
		}
		if r.historyPolicy.Disabled {
			continue
		}
		if p.Kind != "chat" && p.AccessHash == 0 && p.ID != r.self {
			continue
		}
		// A resumed historical scan also checks messages newer than its first page.
		if p.Newest > 0 || p.HistoryDone {
			if err := r.history(ctx, key, p, true); err != nil {
				if inaccessible(err) {
					if err := r.archive.block(key, "access revoked"); err != nil {
						return err
					}
					continue
				}
				return fmt.Errorf("new history %s: %w", key, err)
			}
		}
		if !p.HistoryDone {
			if err := r.history(ctx, key, p, false); err != nil {
				if inaccessible(err) {
					if err := r.archive.block(key, "access revoked"); err != nil {
						return err
					}
					continue
				}
				return fmt.Errorf("history %s: %w", key, err)
			}
		}
	}
	log.Print("History and deletion reconciliation complete")
	return nil
}

func (r *receiver) syncLoop(ctx context.Context, every time.Duration, once bool) error {
	for {
		if err := r.sync(ctx); err != nil {
			return err
		}
		if once {
			return nil
		}
		timer := time.NewTimer(every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-r.resync:
			timer.Stop()
		case <-timer.C:
		}
	}
}
