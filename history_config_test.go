package main

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestParseHistoryConfig(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cfg, err := parseHistoryConfig(30, "", false, now)
	requireOK(t, err)
	if !cfg.Since.Equal(now.Add(-30*24*time.Hour)) || cfg.Disabled {
		t.Fatal("default window is not 30 days")
	}
	cfg, err = parseHistoryConfig(0, "", true, now)
	requireOK(t, err)
	if !cfg.Disabled {
		t.Fatal("zero days did not disable history")
	}
	cfg, err = parseHistoryConfig(-1, "", true, now)
	requireOK(t, err)
	if !cfg.Since.IsZero() || cfg.Disabled {
		t.Fatal("explicit full history failed")
	}
	cfg, err = parseHistoryConfig(30, "2026-09-01", false, now)
	requireOK(t, err)
	if cfg.Since.Format(time.RFC3339) != "2026-09-01T00:00:00Z" {
		t.Fatal("date boundary is not UTC midnight")
	}
	cfg, err = parseHistoryConfig(30, "2026-09-01T08:00:00+08:00", false, now)
	requireOK(t, err)
	if cfg.Since.Format(time.RFC3339) != "2026-09-01T00:00:00Z" {
		t.Fatal("timezone offset not respected")
	}
	for _, invalid := range []struct {
		days     int
		since    string
		explicit bool
	}{{-2, "", true}, {36501, "", true}, {7, "2026-09-01", true}, {30, "invalid-date", false}, {30, "2027-01-01", false}} {
		if _, err := parseHistoryConfig(invalid.days, invalid.since, invalid.explicit, now); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", invalid)
		}
	}
}

func TestHistoryWindowStopsAtBoundary(t *testing.T) {
	a, r := fixture(t)
	p := PeerInfo{Kind: "user", ID: 7, AccessHash: 123}
	requireOK(t, a.register("user-7", p))
	r.historyPolicy = historyConfig{Since: time.Unix(1700000000, 0).UTC()}
	newer := message(&tg.PeerUser{UserID: 7}, 5, "newer")
	newer.Date = 1700000001
	boundary := message(&tg.PeerUser{UserID: 7}, 4, "on-boundary")
	old := message(&tg.PeerUser{UserID: 7}, 3, "old-must-not-archive")
	old.Date = 1699999999
	calls := 0
	r.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		if calls > 1 {
			t.Fatal("history continued beyond the configured date")
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{newer, boundary, old}}
		return nil
	}))
	requireOK(t, r.history(context.Background(), "user-7", p, false))
	if ids := a.ids("user-7"); len(ids) != 2 || ids[0] != 4 || ids[1] != 5 {
		t.Fatalf("date boundary filter wrong: %v", ids)
	}
	p, _ = a.peer("user-7")
	if !p.HistoryDone || p.Newest != 5 || calls != 1 {
		t.Fatal("bounded history progress not committed")
	}
}

func TestHistoryWindowAlsoLimitsIncrementalCatchup(t *testing.T) {
	a, r := fixture(t)
	p := PeerInfo{Kind: "user", ID: 7, AccessHash: 123}
	requireOK(t, a.register("user-7", p))
	requireOK(t, a.checkpoint("user-7", 1, true, 5))
	p, _ = a.peer("user-7")
	r.historyPolicy = historyConfig{Since: time.Unix(1700000000, 0).UTC()}
	newer := message(&tg.PeerUser{UserID: 7}, 7, "newer")
	old := message(&tg.PeerUser{UserID: 7}, 6, "older-offline-backlog")
	old.Date = 1699999999
	calls := 0
	r.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		req := in.(*tg.MessagesGetHistoryRequest)
		if req.MinID != 5 || req.OffsetID != 0 || calls > 1 {
			t.Fatal("incorrect bounded incremental request")
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{newer, old}}
		return nil
	}))
	requireOK(t, r.history(context.Background(), "user-7", p, true))
	p, _ = a.peer("user-7")
	if !p.HistoryDone || p.Offset != 1 || p.Newest != 7 || len(a.ids("user-7")) != 1 {
		t.Fatal("incremental range or cursor is incorrect")
	}
}

func TestHistoryDisabledKeepsLiveUpdatesAndDeletionReconciliation(t *testing.T) {
	a, r := fixture(t)
	p := PeerInfo{Kind: "user", ID: 7, AccessHash: 123}
	requireOK(t, a.register("user-7", p))
	r.historyPolicy = historyConfig{Disabled: true}
	r.api = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetMessagesRequest); !ok {
			t.Fatalf("history RPC made despite disabled backfill: %T", in)
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 1}}}
		return nil
	}))
	requireOK(t, r.history(context.Background(), "user-7", p, false))
	requireOK(t, r.history(context.Background(), "user-7", p, true))
	requireOK(t, r.records([]tg.MessageClass{message(&tg.PeerUser{UserID: 7}, 1, "live-body")}, true, 0))
	if len(a.ids("user-7")) != 1 {
		t.Fatal("disabling backfill also disabled live messages")
	}
	requireOK(t, r.reconcile(context.Background(), "user-7", p))
	if len(a.ids("user-7")) != 0 {
		t.Fatal("disabling backfill also disabled deletion checks")
	}
}

func TestHistoryScopeWideningResetsPersistedProgress(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	requireOK(t, a.setHistoryScope(1700000000))
	requireOK(t, a.checkpoint("user-7", 20, true, 100))
	requireOK(t, a.upsert([]Record{row("user-7", 99, "existing")}, true, 0))
	a, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	requireOK(t, a.setHistoryScope(1690000000))
	p, _ := a.peer("user-7")
	if p.HistoryDone || p.Offset != 0 || p.Newest != 100 || p.HistorySince != 1690000000 {
		t.Fatal("widened scope did not reset historic cursor")
	}
	requireOK(t, a.checkpoint("user-7", 10, true, 100))
	requireOK(t, a.setHistoryScope(0))
	p, _ = a.peer("user-7")
	if p.HistoryDone || p.Offset != 0 {
		t.Fatal("switching to full history did not reset cursor")
	}
	if len(a.ids("user-7")) != 1 {
		t.Fatal("changing history scope deleted existing archive")
	}
}

func TestHistoryScopeNarrowingKeepsProgressAndRecords(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	requireOK(t, a.checkpoint("user-7", 10, true, 100))
	requireOK(t, a.upsert([]Record{row("user-7", 99, "previously-archived")}, true, 0))
	requireOK(t, a.setHistoryScope(1700000001))
	p, _ := a.peer("user-7")
	if !p.HistoryDone || p.Offset != 10 || p.Newest != 100 || len(a.ids("user-7")) != 1 {
		t.Fatal("narrowing range rewrote archive or reset complete progress")
	}
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7, AccessHash: 456}))
	p, _ = a.peer("user-7")
	if p.HistorySince != 1700000001 {
		t.Fatal("entity refresh lost persisted history scope")
	}
}
