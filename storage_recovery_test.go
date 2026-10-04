package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gotd/td/telegram/updates"
)

func totalChanges(t *testing.T, s *sqliteStore) int64 {
	t.Helper()
	var n int64
	requireOK(t, s.db.QueryRow("SELECT total_changes()").Scan(&n))
	return n
}

func writeReceipt(t *testing.T, a *archive, peer string, data []byte) {
	t.Helper()
	sum := sha256.Sum256(data)
	requireOK(t, a.state.WriteJSON("jsonl_intent:"+peer, jsonlIntent{ExpectedSHA256: hex.EncodeToString(sum[:])}))
}

func requireNoReceipt(t *testing.T, a *archive, peer string) {
	t.Helper()
	var receipt jsonlIntent
	if err := a.state.ReadJSON("jsonl_intent:"+peer, &receipt); !os.IsNotExist(err) {
		t.Fatalf("replacement receipt was not cleared: %v", err)
	}
}

func TestStorageSplitsHotStateAndColdProperties(t *testing.T) {
	a, r := fixture(t)
	p := PeerInfo{Kind: "channel", ID: 9, Name: "synthetic name", Username: "sample", AccessHash: 123}
	requireOK(t, a.register("channel-9", p))
	requireOK(t, r.protocol.SetState(context.Background(), 42, updates.State{Pts: 10}))
	requireOK(t, r.protocol.SetChannelPts(context.Background(), 42, 9, 20))
	stateBefore := totalChanges(t, a.state)
	requireOK(t, a.register("channel-9", PeerInfo{Kind: "channel", ID: 9, Name: "renamed", AccessHash: 456}))
	if totalChanges(t, a.state) != stateBefore {
		t.Fatal("cold profile change rewrote runtime metadata")
	}
	protocolBefore := totalChanges(t, r.protocol.store)
	requireOK(t, r.protocol.SetChannelAccessHash(context.Background(), 42, 9, 789))
	if totalChanges(t, r.protocol.store) != protocolBefore {
		t.Fatal("channel hash update rewrote runtime cursors")
	}
	var raw string
	requireOK(t, a.state.db.QueryRow("SELECT value_json FROM state WHERE key='archive_metadata'").Scan(&raw))
	for _, cold := range []string{"access_hash", "username", "name"} {
		if strings.Contains(raw, cold) {
			t.Fatalf("hot archive metadata contains cold field %q", cold)
		}
	}
	requireOK(t, r.protocol.store.db.QueryRow("SELECT value_json FROM state WHERE key='updates'").Scan(&raw))
	if strings.Contains(raw, "access_hash") {
		t.Fatal("hot protocol JSON contains channel access hashes")
	}
	var tables int
	requireOK(t, a.state.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables))
	if tables != 1 {
		t.Fatal("runtime database includes attribute tables")
	}
	var meta archiveMeta
	requireOK(t, readArchiveMeta(a.dir, &meta))
	if meta.Peers["channel-9"].Name != "renamed" || meta.Peers["channel-9"].AccessHash != 789 {
		t.Fatal("read-only archive load did not join current attributes")
	}
	propsBefore := totalChanges(t, a.index)
	requireOK(t, a.checkpoint("channel-9", 5, false, 10))
	if totalChanges(t, a.index) != propsBefore {
		t.Fatal("history cursor rewrote cold properties")
	}
}

func TestStorageUnchangedPropertiesDoNotWrite(t *testing.T) {
	s, _ := testSQLite(t)
	p := PeerInfo{Kind: "user", ID: 7, Name: "sample", Username: "Sample", AccessHash: 99}
	r := row("user-7", 1, "message stays in JSONL")
	r.Media = &MediaDetails{Kind: "document", File: &FileMetadata{ID: "55", Name: "file.pdf", MIME: "application/pdf", Size: 8}}
	requireOK(t, s.UpsertPeer(r.Peer, p, true))
	requireOK(t, s.WriteJSON("archive_account", int64(42)))
	requireOK(t, s.SyncMessages(r.Peer, map[int]Record{1: r}))
	before := totalChanges(t, s)
	requireOK(t, s.UpsertPeer(r.Peer, p, true))
	requireOK(t, s.WriteJSON("archive_account", int64(42)))
	requireOK(t, s.SyncMessages(r.Peer, map[int]Record{1: r}))
	if totalChanges(t, s) != before {
		t.Fatal("unchanged property or media projection wrote database rows")
	}
}

func TestProtocolHashUpdatesDoNotRestoreBlockedMappings(t *testing.T) {
	a, r := fixture(t)
	requireOK(t, a.register("channel-9", PeerInfo{Kind: "channel", ID: 9, Name: "sample", AccessHash: 123}))
	requireOK(t, a.block("channel-9", "protected content"))
	requireOK(t, r.protocol.SetChannelAccessHash(context.Background(), 42, 9, 456))
	var count int
	requireOK(t, a.index.db.QueryRow("SELECT COUNT(*) FROM peers WHERE peer='channel-9'").Scan(&count))
	if count != 0 {
		t.Fatal("protocol hash update recreated an excluded peer mapping")
	}
}

func TestArchiveCleanRestartDoesNotRebuildMedia(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7, Name: "sample"}))
	r := row("user-7", 1, "synthetic")
	r.Media = &MediaDetails{Kind: "document", File: &FileMetadata{ID: "55"}}
	requireOK(t, a.upsert([]Record{r}, true, 0))
	requireNoReceipt(t, a, r.Peer)
	// A catalog fault without a receipt is not silently repaired on every start.
	_, err := a.index.db.Exec("DELETE FROM media")
	requireOK(t, err)
	reopened, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	defer reopened.Close()
	if sqliteCount(t, reopened.index, "media") != 0 {
		t.Fatal("clean startup rebuilt the media table")
	}
}

func TestArchiveReceiptRecoversEitherDurableFile(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_replace", true: "after_replace"}[replaced], func(t *testing.T) {
			a, _ := fixture(t)
			requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
			r := row("user-7", 1, "old-durable-body")
			r.Media = &MediaDetails{Kind: "document", File: &FileMetadata{ID: "11"}}
			requireOK(t, a.upsert([]Record{r}, true, 0))
			requireOK(t, a.checkpoint(r.Peer, 10, false, 1))
			if replaced {
				// A closed catalog interrupts flush after the durable JSONL replacement.
				requireOK(t, a.index.Close())
				next := row(r.Peer, 2, "new-durable-body")
				next.Media = &MediaDetails{Kind: "photo", File: &FileMetadata{ID: "22"}}
				if err := a.upsert([]Record{next}, true, 0); err == nil {
					t.Fatal("catalog failure did not interrupt flush")
				}
			} else {
				writeReceipt(t, a, r.Peer, []byte("replacement never committed"))
				_, err := a.index.db.Exec("DELETE FROM media")
				requireOK(t, err)
			}
			reopened, err := openArchive(a.dir, &failure{})
			requireOK(t, err)
			defer reopened.Close()
			requireNoReceipt(t, reopened, r.Peer)
			want := 1
			if replaced {
				want = 2
			}
			if sqliteCount(t, reopened.index, "media") != want || len(reopened.ids(r.Peer)) != want {
				t.Fatal("recovery did not project the durable file")
			}
			p, _ := reopened.peer(r.Peer)
			if p.Offset != 10 || p.HistoryDone {
				t.Fatal("projection recovery advanced the history cursor")
			}
			rows, err := reopened.state.db.Query("SELECT value_json FROM state")
			requireOK(t, err)
			defer rows.Close()
			for rows.Next() {
				var value string
				requireOK(t, rows.Scan(&value))
				if strings.Contains(value, "durable-body") {
					t.Fatal("replacement recovery stored message text in SQLite")
				}
			}
			requireOK(t, rows.Err())
		})
	}
}

func TestArchiveReceiptFailureDoesNotReplaceJSONL(t *testing.T) {
	a, _ := fixture(t)
	requireOK(t, a.register("user-7", PeerInfo{Kind: "user", ID: 7}))
	requireOK(t, a.upsert([]Record{row("user-7", 1, "durable")}, true, 0))
	before := fileText(t, a, "user-7")
	requireOK(t, a.state.Close())
	if err := a.upsert([]Record{row("user-7", 1, "not committed")}, true, 0); err == nil {
		t.Fatal("receipt failure did not stop replacement")
	}
	if fileText(t, a, "user-7") != before {
		t.Fatal("JSONL changed without a durable replacement receipt")
	}
}

func TestSQLiteRolesLegacyVersionAndAtomicStateBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := openStateSQLite(path)
	requireOK(t, err)
	defer s.Close()
	requireOK(t, s.WriteJSON("remove", 1))
	requireOK(t, s.WriteJSONBatch(map[string]any{"phase": "done", "cursor": 123}, []string{"remove"}))
	var cursor int
	requireOK(t, s.ReadJSON("cursor", &cursor))
	if cursor != 123 {
		t.Fatal("atomic phase cursor missing")
	}
	if err := s.ReadJSON("remove", &cursor); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("atomic state batch did not remove obsolete key")
	}
	if err := s.WriteJSONBatch(map[string]any{"cursor": 999, "invalid": make(chan int)}, []string{"phase"}); err == nil {
		t.Fatal("invalid batch was accepted")
	}
	requireOK(t, s.ReadJSON("cursor", &cursor))
	if cursor != 123 {
		t.Fatal("invalid state batch partially advanced cursor")
	}
	if attrs, err := openSQLite(path); err == nil {
		attrs.Close()
		t.Fatal("runtime database accepted as attributes")
	}
	_, err = s.db.Exec("PRAGMA user_version=1")
	requireOK(t, err)
	if opened, err := openStateSQLite(path); err == nil {
		opened.Close()
		t.Fatal("version1 database automatically converted")
	}
	var version int
	requireOK(t, s.db.QueryRow("PRAGMA user_version").Scan(&version))
	if version != 1 {
		t.Fatal("legacy schema marker was modified")
	}
}

func TestSQLiteConcurrentAttributesWritesUseBusyTimeout(t *testing.T) {
	s, path := testSQLite(t)
	requireOK(t, s.WriteJSON("archive_account", int64(42)))
	other, err := openSQLite(path)
	requireOK(t, err)
	defer other.Close()
	r := row("user-7", 1, "synthetic")
	r.Media = &MediaDetails{Kind: "document", File: &FileMetadata{ID: "55"}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	for _, store := range []*sqliteStore{s, other} {
		go func(catalog *sqliteStore) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				if err := catalog.SyncMessages(r.Peer, map[int]Record{1: r}); err != nil {
					errs <- err
					return
				}
			}
		}(store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if sqliteCount(t, s, "media") != 1 {
		t.Fatal("concurrent attributes projection lost media")
	}
}
