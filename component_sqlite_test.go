package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

func TestComponentSQLitePersistsIndependentRuntimeKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	c := sourceConfig{clientIdentity: clientIdentity{Mode: "native", APIID: 12345, Namespace: hostNamespace}, APIHash: strings.Repeat("a", 32)}
	_, err := sourceStorage(dir, c, &failure{}, false)
	requireOK(t, err)
	protocol, err := openProtocolStore(path, &failure{})
	requireOK(t, err)
	requireOK(t, protocol.bind(42))
	requireOK(t, protocol.SetState(context.Background(), 42, updates.State{Pts: 10, Qts: 11, Date: 12, Seq: 13}))
	requireOK(t, protocol.SetChannelPts(context.Background(), 42, 9, 100))
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	requireOK(t, protocol.store.WriteJSON("cooldown", deadline))
	requireOK(t, protocol.Close())

	protocol, err = openProtocolStore(path, &failure{})
	requireOK(t, err)
	defer protocol.Close()
	state, ok, err := protocol.GetState(context.Background(), 42)
	requireOK(t, err)
	if !ok || state.Pts != 10 || state.Qts != 11 || state.Seq != 13 || state.Date != 12 {
		t.Fatal("protocol state was not restored")
	}
	pts, ok, err := protocol.GetChannelPts(context.Background(), 42, 9)
	requireOK(t, err)
	if !ok || pts != 100 {
		t.Fatal("channel state was not restored")
	}
	gate, err := openGate(path, time.Second, &failure{})
	requireOK(t, err)
	defer gate.Close()
	if !gate.cooldown.Equal(deadline) {
		t.Fatal("cooldown did not share the component database")
	}
	identity, err := currentSessionIdentity(dir)
	requireOK(t, err)
	if identity != c.clientIdentity {
		t.Fatal("runtime state overwrote the client identity")
	}
	var stored string
	requireOK(t, protocol.store.db.QueryRow("SELECT value_json FROM state WHERE key='client'").Scan(&stored))
	if strings.Contains(stored, c.APIHash) || strings.Contains(stored, "auth_key") {
		t.Fatal("client state includes credentials or authorization")
	}
}

func TestComponentSQLiteIgnoresLegacyJSONWithoutImport(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`{"mode":"native","api_id":9876,"namespace":"old"}`)
	requireOK(t, atomicWrite(filepath.Join(dir, "client.json"), legacy))
	requireOK(t, writeJSON(filepath.Join(dir, "updates.json"), protocolData{UserID: 99, Channels: map[int64]channelState{}}))
	requireOK(t, writeJSON(filepath.Join(dir, "cooldown.json"), time.Now().Add(time.Hour)))
	c := sourceConfig{clientIdentity: clientIdentity{Mode: "native", APIID: 12345, Namespace: hostNamespace}}
	_, err := sourceStorage(dir, c, &failure{}, false)
	requireOK(t, err)
	other := c
	other.Namespace = fetchNamespace
	if _, err := sourceStorage(dir, other, &failure{}, false); err == nil {
		t.Fatal("a different component rebound the client identity")
	}
	saved, err := currentSessionIdentity(dir)
	requireOK(t, err)
	if saved != c.clientIdentity {
		t.Fatal("failed identity check overwrote SQLite state")
	}
	protocol, err := openProtocolStore(filepath.Join(dir, "state.sqlite"), &failure{})
	requireOK(t, err)
	defer protocol.Close()
	if protocol.data.UserID != 0 || protocol.data.State != nil {
		t.Fatal("legacy protocol JSON was imported")
	}
	gate, err := openGate(filepath.Join(dir, "state.sqlite"), time.Second, &failure{})
	requireOK(t, err)
	defer gate.Close()
	if !gate.cooldown.IsZero() {
		t.Fatal("legacy cooldown JSON was imported")
	}
	b, err := os.ReadFile(filepath.Join(dir, "client.json"))
	requireOK(t, err)
	if !bytes.Equal(b, legacy) {
		t.Fatal("retired private state was modified")
	}
}

func TestComponentSQLiteRejectsUnboundExistingSession(t *testing.T) {
	dir := t.TempDir()
	session := validSession(t)
	requireOK(t, atomicWrite(filepath.Join(dir, "session.json"), session))
	c := sourceConfig{clientIdentity: clientIdentity{Mode: "native", APIID: 12345, Namespace: hostNamespace}}
	if _, err := sourceStorage(dir, c, &failure{}, false); err == nil || !strings.Contains(err.Error(), "no SQLite client identity") {
		t.Fatal("existing authorization was silently rebound")
	}
	var identity clientIdentity
	if err := readStateJSON(filepath.Join(dir, "state.sqlite"), "client", &identity); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected authorization wrote client state")
	}
	b, err := os.ReadFile(filepath.Join(dir, "session.json"))
	requireOK(t, err)
	if !bytes.Equal(b, session) {
		t.Fatal("rejected authorization modified the session")
	}
}

func componentCatalog(t *testing.T, accountID int64) *sqliteStore {
	t.Helper()
	catalog, err := openSQLite(filepath.Join(t.TempDir(), "index.sqlite"))
	requireOK(t, err)
	t.Cleanup(func() { _ = catalog.Close() })
	requireOK(t, catalog.WriteJSON("archive_metadata", archiveMeta{AccountID: accountID}))
	return catalog
}

func TestFetchSQLiteRecordsOnlyCompletedFilesAndKeepsDeletionHistory(t *testing.T) {
	catalog := componentCatalog(t, 42)
	record := row("user-7", 1, "synthetic body stays outside SQLite")
	record.Media = &MediaDetails{Kind: "document", File: &FileMetadata{ID: "55", Name: "invoice.pdf", Size: 8}}
	requireOK(t, catalog.SyncMessages(record.Peer, map[int]Record{1: record}))
	m := attachmentMessage(8)
	f, _ := fakeFetcher(t, func(n int) *tg.Message {
		if n == 2 {
			// Simulate archive deletion during the transfer without reviving its media index.
			requireOK(t, catalog.SyncMessages(record.Peer, nil))
		}
		return m
	}, []byte("pdf-data"), false)
	f.catalog = catalog
	result := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
	if result.Status != "downloaded" {
		t.Fatalf("download did not complete: %+v", result)
	}
	var path, digest, downloadedAt, kind string
	var size int64
	requireOK(t, catalog.db.QueryRow("SELECT path,size_bytes,sha256,downloaded_at,media_kind FROM files WHERE peer=? AND message_id=? AND media_id=?", record.Peer, 1, "55").Scan(&path, &size, &digest, &downloadedAt, &kind))
	if path != result.Path || size != 8 || digest != result.SHA256 || !filepath.IsAbs(path) || kind != "document" {
		t.Fatal("completed file mapping does not match the saved file")
	}
	when, err := time.Parse(time.RFC3339Nano, downloadedAt)
	requireOK(t, err)
	if _, offset := when.Zone(); offset != 0 {
		t.Fatal("download time was not stored in UTC")
	}
	var count int
	requireOK(t, catalog.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&count))
	if count != 0 {
		t.Fatal("download resurrected deleted media")
	}
	requireOK(t, catalog.RemovePeer(record.Peer))
	requireOK(t, catalog.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count))
	if count != 1 {
		t.Fatal("remote peer removal discarded completed-file history")
	}
	f, _ = fakeFetcher(t, func(int) *tg.Message { return nil }, nil, false)
	f.catalog = catalog
	record.MessageID = 2
	if result := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100); result.Status != "skipped" {
		t.Fatal("unavailable message was not skipped")
	}
	requireOK(t, catalog.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count))
	if count != 1 {
		t.Fatal("skipped download wrote a completed-file mapping")
	}
}

func TestFetchSQLiteLedgerFailurePreservesCompletedFile(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed_database", true: "wrong_account"}[mismatch], func(t *testing.T) {
			accountID := int64(42)
			if mismatch {
				accountID = 99
			}
			catalog := componentCatalog(t, accountID)
			if !mismatch {
				requireOK(t, catalog.Close())
			}
			f, _ := fakeFetcher(t, func(int) *tg.Message { return attachmentMessage(8) }, []byte("pdf-data"), false)
			f.catalog = catalog
			result := f.download(context.Background(), row("user-7", 1, "synthetic"), PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
			if result.Status != "error" || !strings.Contains(result.Reason, "file downloaded") || result.Path == "" || result.Size != 8 || len(result.SHA256) != 64 {
				t.Fatalf("ledger failure hid the completed file: %+v", result)
			}
			b, err := os.ReadFile(result.Path)
			requireOK(t, err)
			if string(b) != "pdf-data" {
				t.Fatal("ledger failure removed or altered a completed file")
			}
			if mismatch {
				var count int
				requireOK(t, catalog.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count))
				if count != 0 {
					t.Fatal("wrong-account download was recorded")
				}
			}
		})
	}
}

func TestFetchRejectsPhotoDocumentIDCollisions(t *testing.T) {
	photo := message(&tg.PeerUser{UserID: 7}, 1, "synthetic photo")
	photo.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 55, DCID: 2, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 2, H: 2, Size: 8}}}}
	for _, phase := range []string{"archive", "archive_media_type", "reference_refresh", "final_check"} {
		t.Run(phase, func(t *testing.T) {
			record := row("user-7", 1, "synthetic")
			expire := phase == "reference_refresh"
			if phase == "archive" {
				record.Media = &MediaDetails{Kind: "photo", File: &FileMetadata{ID: "55"}}
			}
			if phase == "archive_media_type" {
				record.MediaType = photo.Media.TypeName()
			}
			f, calls := fakeFetcher(t, func(n int) *tg.Message {
				if n > 1 {
					return photo
				}
				return attachmentMessage(8)
			}, []byte("pdf-data"), expire)
			out := t.TempDir()
			result := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, out, 100)
			if result.Status != "skipped" {
				t.Fatalf("different attachment namespace was accepted: %+v", result)
			}
			if strings.HasPrefix(phase, "archive") && *calls != 0 {
				t.Fatal("known archive media mismatch started a transfer")
			}
			files, err := os.ReadDir(out)
			requireOK(t, err)
			if len(files) != 0 {
				t.Fatal("changed media type left a committed or partial file")
			}
		})
	}
}

func TestFetchSQLiteLedgerUsesValidatedCurrentMediaKind(t *testing.T) {
	catalog := componentCatalog(t, 42)
	record := row("user-7", 1, "synthetic")
	record.Media = &MediaDetails{Kind: "video", File: &FileMetadata{ID: "55"}}
	f, _ := fakeFetcher(t, func(n int) *tg.Message {
		m := attachmentMessage(8)
		if n > 1 {
			document := m.Media.(*tg.MessageMediaDocument).Document.(*tg.Document)
			document.Attributes = append(document.Attributes, &tg.DocumentAttributeAudio{Voice: true, Duration: 1})
		}
		return m
	}, []byte("pdf-data"), false)
	f.catalog = catalog
	result := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
	if result.Status != "downloaded" {
		t.Fatalf("download failed: %+v", result)
	}
	var kind string
	requireOK(t, catalog.db.QueryRow("SELECT media_kind FROM files WHERE peer=? AND message_id=?", record.Peer, 1).Scan(&kind))
	if kind != "voice" {
		t.Fatal("file ledger used stale archive media subtype")
	}
	if record.Media.Kind != "video" {
		t.Fatal("file indexing changed the original JSONL snapshot")
	}
}

func TestFetchPhotoDocumentNamespacesKeepDistinctCompletedFiles(t *testing.T) {
	catalog := componentCatalog(t, 42)
	document := attachmentMessage(8)
	doc := document.Media.(*tg.MessageMediaDocument).Document.(*tg.Document)
	doc.MimeType = "image/jpeg"
	doc.Attributes = []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "image.jpg"}}
	photo := message(&tg.PeerUser{UserID: 7}, 1, "synthetic photo")
	photo.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 55, DCID: 2, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 2, H: 2, Size: 8}}}}
	current := document
	f := &fetchRunner{catalog: catalog, api: tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetMessagesRequest); !ok {
			t.Fatalf("unexpected message RPC %T", in)
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{current}}
		return nil
	})), fileClient: func(context.Context, int) (downloader.Client, func(), error) {
		return tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			if _, ok := in.(*tg.UploadGetFileRequest); !ok {
				t.Fatalf("unexpected file RPC %T", in)
			}
			out.(*tg.UploadFileBox).File = &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte("jpg-data")}
			return nil
		})), func() {}, nil
	}}
	out := t.TempDir()
	record := row("user-7", 1, "synthetic")
	first := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, out, 100)
	current = photo
	second := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, out, 100)
	if first.Status != "downloaded" || second.Status != "downloaded" || first.Path == second.Path {
		t.Fatal("photo and document namespaces reused the same completed file")
	}
	if filepath.Base(first.Path) != "user-7-1-document-55.jpg" || filepath.Base(second.Path) != "user-7-1-photo-55.jpg" {
		t.Fatal("completed filenames omit the attachment namespace")
	}
	for _, result := range []fetchResult{first, second} {
		b, err := os.ReadFile(result.Path)
		requireOK(t, err)
		if string(b) != "jpg-data" {
			t.Fatal("completed file content was overwritten")
		}
	}
	var count int
	requireOK(t, catalog.db.QueryRow("SELECT COUNT(*) FROM files WHERE peer=? AND message_id=? AND media_id=?", record.Peer, 1, "55").Scan(&count))
	if count != 2 {
		t.Fatal("file ledger collapsed distinct attachment namespaces")
	}
}
