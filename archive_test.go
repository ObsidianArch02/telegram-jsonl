package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (*archive, *receiver) {
	t.Helper()
	fail := &failure{}
	a, err := openArchive(filepath.Join(t.TempDir(), "archive"), fail)
	requireOK(t, err)
	requireOK(t, a.bind(42))
	s, err := openProtocolStore(filepath.Join(t.TempDir(), "updates.json"), fail)
	requireOK(t, err)
	requireOK(t, s.bind(42))
	return a, &receiver{archive: a, protocol: s, self: 42, batch: 2, failure: fail, resync: make(chan struct{}, 1)}
}

func row(peer string, id int, text string) Record {
	return Record{Schema: 1, AccountID: 42, Peer: peer, MessageID: id, Date: time.Unix(1700000000, 0).UTC(), Text: text}
}
func message(peer tg.PeerClass, id int, text string) *tg.Message {
	return &tg.Message{ID: id, PeerID: peer, Date: 1700000000, Message: text}
}

func fileText(t *testing.T, a *archive, peer string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.dir, peer+".jsonl"))
	if os.IsNotExist(err) {
		return ""
	}
	requireOK(t, err)
	return string(b)
}

func TestEditDeleteRestartAndStaleFetch(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	original := row("user-7", 10, "original-private-body")
	requireOK(t, a.upsert([]Record{original, original}, true, 0))
	if len(a.ids("user-7")) != 1 {
		t.Fatal("duplicate stored")
	}
	fence := a.fence()
	edited := original
	edited.Text = "current-body"
	now := time.Now().UTC()
	edited.EditedAt = &now
	requireOK(t, a.upsert([]Record{edited}, true, 0))
	requireOK(t, a.upsert([]Record{original}, false, fence))
	text := fileText(t, a, "user-7")
	if strings.Contains(text, "original-private-body") || !strings.Contains(text, "current-body") {
		t.Fatal("old edit retained or overwritten")
	}
	requireOK(t, a.remove("user-7", []int{10}, true, 0))
	requireOK(t, a.upsert([]Record{edited}, false, a.fence()))
	reopened, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	requireOK(t, reopened.upsert([]Record{edited}, true, 0))
	if fileText(t, reopened, "user-7") != "" {
		t.Fatal("deleted content resurrected")
	}
	metadata, err := os.ReadFile(a.metaPath())
	requireOK(t, err)
	if strings.Contains(string(metadata), "body") {
		t.Fatal("tombstone contains body")
	}
}

func TestDeletionScopes(t *testing.T) {
	a, _ := fixture(t)
	for _, p := range []PeerInfo{{Kind: "user", ID: 7}, {Kind: "chat", ID: 8}, {Kind: "channel", ID: 9}, {Kind: "channel", ID: 10}} {
		key := p.Kind + "-" + map[int64]string{7: "7", 8: "8", 9: "9", 10: "10"}[p.ID]
		requireOK(t, a.register(key, p))
		requireOK(t, a.upsert([]Record{row(key, 11, "body")}, true, 0))
	}
	requireOK(t, a.remove("", []int{11}, true, 0))
	if len(a.ids("user-7")) != 0 || len(a.ids("chat-8")) != 0 {
		t.Fatal("account-wide non-channel deletion not applied")
	}
	if len(a.ids("channel-9")) != 1 || len(a.ids("channel-10")) != 1 {
		t.Fatal("channel ID collision deleted unrelated content")
	}
	requireOK(t, a.remove("channel-9", []int{11}, true, 0))
	if len(a.ids("channel-10")) != 1 {
		t.Fatal("channel deletion leaked into another channel")
	}
}

func TestFetchDoesNotDeleteConcurrentEdit(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	fence := a.fence()
	requireOK(t, a.upsert([]Record{row("user-7", 10, "live")}, true, 0))
	requireOK(t, a.remove("user-7", []int{10}, false, fence))
	if len(a.ids("user-7")) != 1 {
		t.Fatal("old snapshot erased live update")
	}
}

func TestCrashRecoveryPurgesBodyAfterTombstoneCommit(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	requireOK(t, a.upsert([]Record{row("user-7", 10, "must-disappear")}, true, 0))
	// Simulate termination between metadata commit and the JSONL replacement.
	a.meta.Deleted["user-7"] = map[int]bool{10: true}
	requireOK(t, a.saveMeta())
	requireOK(t, os.WriteFile(filepath.Join(a.dir, ".write-interrupted"), []byte("old-private-body"), 0600))
	b, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	if fileText(t, b, "user-7") != "" {
		t.Fatal("crash recovery kept deleted body")
	}
	if _, err := os.Stat(filepath.Join(a.dir, ".write-interrupted")); !os.IsNotExist(err) {
		t.Fatal("unfinished replacement retained")
	}
}

func TestProtectedAndEphemeralContent(t *testing.T) {
	a, r := fixture(t)
	peer := &tg.PeerChannel{ChannelID: 9}
	requireOK(t, r.records([]tg.MessageClass{message(peer, 1, "ordinary")}, true, 0))
	protected := message(peer, 1, "protected")
	protected.Noforwards = true
	ttl := message(peer, 2, "auto-delete")
	ttl.TTLPeriod = 3600
	photo := message(peer, 3, "view-once")
	photo.Media = &tg.MessageMediaPhoto{TTLSeconds: 10, Photo: &tg.Photo{ID: 1}}
	document := message(peer, 4, "self-destruct")
	document.Media = &tg.MessageMediaDocument{TTLSeconds: 10, Document: &tg.Document{ID: 1}}
	requireOK(t, r.records([]tg.MessageClass{protected, ttl, photo, document}, true, 0))
	if fileText(t, a, "channel-9") != "" {
		t.Fatal("ephemeral or protected content persisted")
	}
	requireOK(t, r.records([]tg.MessageClass{message(peer, 5, "previously-normal")}, true, 0))
	requireOK(t, r.observe(nil, []tg.ChatClass{&tg.Channel{ID: 9, Noforwards: true}}))
	requireOK(t, r.records([]tg.MessageClass{message(peer, 6, "must-skip")}, true, 0))
	if fileText(t, a, "channel-9") != "" || !a.blocked("channel-9") {
		t.Fatal("protected chat not purged")
	}
}

func TestTTLSettingPurgesAndBlocksPeer(t *testing.T) {
	a, r := fixture(t)
	peer := &tg.PeerUser{UserID: 7}
	requireOK(t, r.records([]tg.MessageClass{message(peer, 1, "old-body")}, true, 0))
	requireOK(t, r.handler().Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdatePeerHistoryTTL{Peer: peer, TTLPeriod: 60}}))
	if fileText(t, a, "user-7") != "" || !a.blocked("user-7") {
		t.Fatal("TTL setting not respected")
	}
}

func TestGroupSendersAreNotDiscoveredAsDialogs(t *testing.T) {
	a, r := fixture(t)
	requireOK(t, r.observe([]tg.UserClass{&tg.User{ID: 99, AccessHash: 123}}, nil))
	if _, ok := a.peer("user-99"); ok {
		t.Fatal("group sender became an unrelated private dialog")
	}
	requireOK(t, r.records([]tg.MessageClass{message(&tg.PeerUser{UserID: 99}, 1, "actual-dialog")}, true, 0))
	p, ok := a.peer("user-99")
	if !ok || p.AccessHash != 123 {
		t.Fatal("actual dialog lost access hash")
	}
}

func TestSingleAccountAndPermissions(t *testing.T) {
	a, _ := fixture(t)
	if a.bind(99) == nil {
		t.Fatal("account switch accepted")
	}
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	requireOK(t, a.upsert([]Record{row("user-7", 1, "body")}, true, 0))
	for _, path := range []string{a.metaPath(), filepath.Join(a.dir, "user-7.jsonl")} {
		info, err := os.Stat(path)
		requireOK(t, err)
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe permission on %s", path)
		}
	}
	info, err := os.Stat(a.dir)
	requireOK(t, err)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal("unsafe directory permission")
	}
}

func TestLockRejectsSecondProcess(t *testing.T) {
	dir := t.TempDir()
	one, err := lockDirectory(dir)
	requireOK(t, err)
	defer one.Close()
	two, err := lockDirectory(dir)
	if err == nil {
		two.Close()
		t.Fatal("second instance acquired session lock")
	}
}

func TestConcurrentArchiveMutations(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := a.upsert([]Record{row("user-7", id, "body")}, true, 0); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(a.ids("user-7")) != 20 {
		t.Fatal("concurrent writes lost messages")
	}
}

func TestStorageFailureStopsCursorAdvancement(t *testing.T) {
	_, r := fixture(t)
	ctx := context.Background()
	requireOK(t, r.protocol.SetState(ctx, 42, updates.State{Pts: 10}))
	requireOK(t, r.protocol.SetChannelPts(ctx, 42, 9, 100))
	requireOK(t, r.protocol.SetChannelAccessHash(ctx, 42, 9, 123))
	reopened, err := openProtocolStore(r.protocol.path, &failure{})
	requireOK(t, err)
	n, ok, err := reopened.GetChannelPts(ctx, 42, 9)
	requireOK(t, err)
	if !ok || n != 100 {
		t.Fatal("channel cursor not restored")
	}
	r.failure.report(errors.New("disk failure"))
	if r.protocol.SetPts(ctx, 42, 20) == nil {
		t.Fatal("cursor advanced after archive failure")
	}
	var saved protocolData
	requireOK(t, readJSON(r.protocol.path, &saved))
	if saved.State.Pts != 10 {
		t.Fatal("disk cursor skipped failed message")
	}
}

func TestFloodWaitPersistsAndCancellationDoesNotRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cooldown.json")
	g, err := openGate(path, time.Millisecond, &failure{})
	requireOK(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	calls := 0
	invoke := g.Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return tgerr.New(420, "FLOOD_WAIT_10") }))
	err = invoke(ctx, &tg.UpdatesGetStateRequest{}, &tg.UpdatesState{})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("unexpected flood retry: calls=%d err=%v", calls, err)
	}
	reopened, err := openGate(path, time.Second, &failure{})
	requireOK(t, err)
	if time.Until(reopened.cooldown) < 9*time.Second {
		t.Fatal("restart discarded FLOOD_WAIT")
	}
}

func TestHistoryResumesWithoutSkippingPages(t *testing.T) {
	a, r := fixture(t)
	p := PeerInfo{Kind: "user", ID: 7, AccessHash: 123}
	requireOK(t, a.register("user-7", p))
	failSecond := true
	r.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req := in.(*tg.MessagesGetHistoryRequest)
		var ids []int
		switch req.OffsetID {
		case 0:
			ids = []int{4, 3}
		case 3:
			if failSecond {
				return errors.New("connection interrupted")
			}
			ids = []int{2, 1}
		case 1:
			ids = nil
		default:
			t.Fatalf("unexpected offset %d", req.OffsetID)
		}
		var msgs []tg.MessageClass
		for _, id := range ids {
			msgs = append(msgs, message(&tg.PeerUser{UserID: 7}, id, "body"))
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: msgs}
		return nil
	}))
	if r.history(context.Background(), "user-7", p, false) == nil {
		t.Fatal("interruption not returned")
	}
	a, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	r.archive = a
	p, _ = a.peer("user-7")
	if p.Offset != 3 || p.HistoryDone {
		t.Fatal("invalid persisted history cursor")
	}
	failSecond = false
	requireOK(t, r.history(context.Background(), "user-7", p, false))
	p, _ = a.peer("user-7")
	if !p.HistoryDone || len(a.ids("user-7")) != 4 {
		t.Fatal("resumed scan skipped history")
	}
}

func TestReconcilePurgesOfflineDeletionAndReplacesEdit(t *testing.T) {
	a, r := fixture(t)
	p := PeerInfo{Kind: "user", ID: 7, AccessHash: 123}
	requireOK(t, a.register("user-7", p))
	requireOK(t, a.upsert([]Record{row("user-7", 1, "offline-deleted"), row("user-7", 2, "before-edit")}, true, 0))
	r.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetMessagesRequest); !ok {
			t.Fatalf("unexpected RPC %T", in)
		}
		edited := message(&tg.PeerUser{UserID: 7}, 2, "after-edit")
		edited.EditDate = 1700000010
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 1}, edited}}
		return nil
	}))
	requireOK(t, r.reconcile(context.Background(), "user-7", p))
	text := fileText(t, a, "user-7")
	if strings.Contains(text, "offline-deleted") || strings.Contains(text, "before-edit") || !strings.Contains(text, "after-edit") {
		t.Fatal("offline changes not reconciled")
	}
}
