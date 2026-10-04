package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func testSQLite(t *testing.T) (*sqliteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.sqlite")
	s, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, path
}

func sqliteCount(t *testing.T, s *sqliteStore, table string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestSQLiteStatePersistsAndReadsWithoutWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	var out map[string]int
	if err := readStateJSON(path, "account", &out); !os.IsNotExist(err) {
		t.Fatalf("missing state: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read created database: %v", err)
	}
	if err := writeStateJSON(path, "account", map[string]int{"id": 123}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := readStateJSON(path, "account", &out); err != nil || out["id"] != 123 {
		t.Fatalf("persisted state: %v, %v", out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("read changed database contents")
	}
	if err := readStateJSON(path, "unknown", &out); !os.IsNotExist(err) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestSQLitePeerNamesAndMetadataStaySeparate(t *testing.T) {
	s, _ := testSQLite(t)
	p := PeerInfo{Kind: "channel", ID: 2632177660, Name: "\u6d4b\u8bd5\u7fa4 \u00e9", Username: "example_group", AccessHash: 123456}
	if err := s.UpsertPeer("channel-2632177660", p, true); err != nil {
		t.Fatal(err)
	}
	p.Name = "\u66f4\u65b0\u540d\u79f0"
	if err := s.UpsertPeer("channel-2632177660", p, false); err != nil {
		t.Fatal(err)
	}
	var name, username string
	var id int64
	var dialog bool
	if err := s.db.QueryRow("SELECT id,name,username,is_dialog FROM peers").Scan(&id, &name, &username, &dialog); err != nil {
		t.Fatal(err)
	}
	if id != p.ID || name != p.Name || username != p.Username || !dialog {
		t.Fatalf("incorrect peer mapping: id=%d name=%q username=%q dialog=%v", id, name, username, dialog)
	}
	if err := s.UpsertPeer("channel-2632177660", PeerInfo{Kind: p.Kind, ID: p.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT name FROM peers WHERE username=?", "EXAMPLE_GROUP").Scan(&name); err != nil || name != p.Name {
		t.Fatalf("partial entity erased profile or case-insensitive alias lookup failed: %q, %v", name, err)
	}
	p.Username = ""
	if err := s.UpsertPeer("channel-2632177660", p, true); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT username FROM peers").Scan(&username); err != nil || username != "" {
		t.Fatalf("full entity did not remove old username: %q, %v", username, err)
	}
	if err := s.UpsertPeer("channel-1", p, true); err == nil {
		t.Fatal("accepted mismatched peer")
	}
	if err := s.WriteJSON("archive_account", int64(42)); err != nil {
		t.Fatal(err)
	}
	var accountID int64
	if err := s.ReadJSON("archive_account", &accountID); err != nil {
		t.Fatal(err)
	}
	props, err := s.PeerProperties()
	if err != nil {
		t.Fatal(err)
	}
	if accountID != 42 || props["channel-2632177660"].AccessHash != p.AccessHash {
		t.Fatal("account binding or peer properties did not round-trip")
	}
}

func TestSQLiteMediaEditsDeletesAndCompletedFiles(t *testing.T) {
	s, _ := testSQLite(t)
	peer := "channel-123"
	if err := s.WriteJSON("archive_account", int64(42)); err != nil {
		t.Fatal(err)
	}
	r := Record{AccountID: 42, Peer: peer, MessageID: 1, Text: "private message body", Media: &MediaDetails{Kind: "document", File: &FileMetadata{ID: "111", Name: "sample.pdf", MIME: "application/pdf", Size: 20, Title: "private caption"}}}
	if err := s.UpsertPeer(peer, PeerInfo{Kind: "channel", ID: 123}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncMessages(peer, map[int]Record{1: r}); err != nil {
		t.Fatal(err)
	}
	var mediaID, kind, name, mime string
	var size int64
	if err := s.db.QueryRow("SELECT media_id,media_kind,file_name,mime_type,size_bytes FROM media").Scan(&mediaID, &kind, &name, &mime, &size); err != nil {
		t.Fatal(err)
	}
	if mediaID != "111" || kind != "document" || name != "sample.pdf" || mime != "application/pdf" || size != 20 {
		t.Fatal("incorrect media mapping")
	}
	result := fetchResult{Peer: peer, MessageID: 1, Status: "downloaded", Path: "/synthetic/sample.pdf", Size: 20, SHA256: strings.Repeat("a", 64)}
	if err := s.RecordDownload(r, "111", result); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDownload(r, "111", result); err != nil {
		t.Fatal(err)
	}
	if sqliteCount(t, s, "files") != 1 {
		t.Fatal("duplicate completed file association")
	}
	r.Media.File.ID = "222"
	if err := s.SyncMessages(peer, map[int]Record{1: r}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT media_id FROM media").Scan(&mediaID); err != nil || mediaID != "222" {
		t.Fatalf("edit did not replace media association: %q, %v", mediaID, err)
	}
	if err := s.SyncMessages(peer, nil); err != nil {
		t.Fatal(err)
	}
	if sqliteCount(t, s, "media") != 0 || sqliteCount(t, s, "files") != 1 {
		t.Fatal("message deletion did not remove media while retaining completed files")
	}
	if err := s.RemovePeer(peer); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDownload(r, "222", result); err != nil {
		t.Fatal(err)
	}
	if sqliteCount(t, s, "peers") != 0 || sqliteCount(t, s, "media") != 0 || sqliteCount(t, s, "files") != 2 {
		t.Fatal("late completed download resurrected peer or media metadata")
	}
	result.Status = "skipped"
	if err := s.RecordDownload(r, "333", result); err != nil || sqliteCount(t, s, "files") != 2 {
		t.Fatalf("recorded skipped download: %v", err)
	}
	r.Media.Kind = "photo"
	result.Status = "downloaded"
	if err := s.RecordDownload(r, "222", result); err != nil {
		t.Fatal(err)
	}
	if sqliteCount(t, s, "files") != 3 {
		t.Fatal("photo and document numeric ID collision lost completed file association")
	}
	var schema string
	rows, err := s.db.Query("SELECT sql FROM sqlite_master WHERE name IN ('media','files')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sql string
		if err := rows.Scan(&sql); err != nil {
			t.Fatal(err)
		}
		schema += sql
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"caption", "text TEXT", "access_hash", "file_reference", "auth_key"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("catalog stores forbidden field %q", forbidden)
		}
	}
}

func TestSQLiteMediaSyncRollback(t *testing.T) {
	s, _ := testSQLite(t)
	r := Record{Peer: "user-1", MessageID: 1, Media: &MediaDetails{Kind: "photo", File: &FileMetadata{ID: "1"}}}
	if err := s.SyncMessages(r.Peer, map[int]Record{1: r}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncMessages(r.Peer, map[int]Record{2: r}); err == nil {
		t.Fatal("accepted mismatched message identity")
	}
	if sqliteCount(t, s, "media") != 1 {
		t.Fatal("invalid sync discarded prior media associations")
	}
}

func TestSQLiteDownloadAccountBinding(t *testing.T) {
	s, _ := testSQLite(t)
	r := Record{AccountID: 42, Peer: "channel-1", MessageID: 1}
	result := fetchResult{Peer: r.Peer, MessageID: r.MessageID, Status: "downloaded", Path: "/synthetic/file", Size: 1, SHA256: strings.Repeat("a", 64)}
	if err := s.RecordDownload(r, "1", result); err == nil {
		t.Fatal("recorded download in an unbound archive")
	}
	if err := s.WriteJSON("archive_account", int64(99)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDownload(r, "1", result); err == nil {
		t.Fatal("recorded download for another account")
	}
	if sqliteCount(t, s, "files") != 0 {
		t.Fatal("rejected download left a catalog record")
	}
}

func TestSQLiteConcurrentHandles(t *testing.T) {
	s, path := testSQLite(t)
	other, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i, store := range []*sqliteStore{s, other} {
		wg.Add(1)
		go func(i int, store *sqliteStore) {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				if err := store.WriteJSON(fmt.Sprintf("%d-%d", i, n), n); err != nil {
					errors <- err
					return
				}
			}
		}(i, store)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if sqliteCount(t, s, "properties") != 40 {
		t.Fatal("concurrent handles lost state")
	}
}

func TestSQLiteIgnoresRetiredJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "archive")
	if err := privateDir(dir); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "metadata.json")
	data := []byte(`{"account_id":999}`)
	if err := os.WriteFile(legacy, data, 0600); err != nil {
		t.Fatal(err)
	}
	var meta archiveMeta
	if err := readArchiveMeta(dir, &meta); !os.IsNotExist(err) {
		t.Fatalf("used retired JSON metadata: %v", err)
	}
	if err := writeStateJSON(filepath.Join(filepath.Dir(dir), "state.sqlite"), "archive_metadata", archiveMeta{AccountID: 123}); err != nil {
		t.Fatal(err)
	}
	s, err := openSQLite(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteJSON("archive_account", int64(123)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := readArchiveMeta(dir, &meta); err != nil || meta.AccountID != 123 {
		t.Fatalf("did not use SQLite: %v, %v", meta.AccountID, err)
	}
	preserved, err := os.ReadFile(legacy)
	if err != nil || string(preserved) != string(data) {
		t.Fatalf("changed old metadata file: %v", err)
	}
}

func TestSQLitePermissionsAndSymlinks(t *testing.T) {
	s, path := testSQLite(t)
	if err := s.WriteJSON("value", 1); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("database file is not private: %s %o", filepath.Base(file), info.Mode().Perm())
		}
	}
	link := filepath.Join(t.TempDir(), "linked.sqlite")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if opened, err := openSQLite(link); err == nil {
		opened.Close()
		t.Fatal("opened symlinked database")
	}
	if err := readStateJSON(link, "value", new(int)); err == nil {
		t.Fatal("read symlinked database")
	}
	otherPath := filepath.Join(t.TempDir(), "other.sqlite")
	if err := os.Symlink(path, otherPath+"-wal"); err != nil {
		t.Fatal(err)
	}
	if opened, err := openSQLite(otherPath); err == nil {
		opened.Close()
		t.Fatal("opened symlinked sidecar")
	}
}

func TestSQLiteFutureSchemaRejected(t *testing.T) {
	s, path := testSQLite(t)
	if _, err := s.db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if opened, err := openSQLite(path); err == nil {
		opened.Close()
		t.Fatal("opened unsupported future schema")
	}
	if err := readStateJSON(path, "missing", new(int)); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("future schema read: %v", err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("changed unsupported schema: %d, %v", version, err)
	}
}
