package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func attachmentMessage(size int64) *tg.Message {
	m := message(&tg.PeerUser{UserID: 7}, 1, "invoice")
	m.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: 55, DCID: 2, Size: size, MimeType: "application/pdf", FileReference: []byte("fresh-reference"), Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "../../invoice.PDF"}}}}
	return m
}

func fakeFetcher(t *testing.T, messages func(int) *tg.Message, body []byte, expireOnce bool) (*fetchRunner, *int) {
	t.Helper()
	messageCalls := 0
	fileCalls := 0
	api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetMessagesRequest); !ok {
			t.Fatalf("unexpected message RPC %T", in)
		}
		messageCalls++
		m := messages(messageCalls)
		var results []tg.MessageClass
		if m != nil {
			results = []tg.MessageClass{m}
		} else {
			results = []tg.MessageClass{&tg.MessageEmpty{ID: 1}}
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: results}
		return nil
	}))
	f := &fetchRunner{api: api, fileClient: func(_ context.Context, dc int) (downloader.Client, func(), error) {
		if dc != 2 {
			t.Fatal("download routed to wrong DC")
		}
		return tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			req, ok := in.(*tg.UploadGetFileRequest)
			if !ok {
				t.Fatalf("unexpected file RPC %T", in)
			}
			fileCalls++
			location := req.Location.(*tg.InputDocumentFileLocation)
			if string(location.FileReference) != "fresh-reference" {
				t.Fatal("download used stale persisted file reference")
			}
			if expireOnce && fileCalls == 1 {
				return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			out.(*tg.UploadFileBox).File = &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: body}
			return nil
		})), func() {}, nil
	}}
	return f, &fileCalls
}

func TestFetchDownloadsWithoutChangingJSONL(t *testing.T) {
	a, r := fixture(t)
	m := attachmentMessage(8)
	requireOK(t, r.records([]tg.MessageClass{m}, true, 0))
	p, _ := a.peer("user-7")
	before := fileText(t, a, "user-7")
	var metaBefore string
	err := a.index.db.QueryRow("SELECT value_json FROM state WHERE key='archive_metadata'").Scan(&metaBefore)
	requireOK(t, err)
	f, calls := fakeFetcher(t, func(int) *tg.Message { return m }, []byte("pdf-data"), false)
	result := f.download(context.Background(), a.rows["user-7"][1], p, t.TempDir(), 100)
	if result.Status != "downloaded" || result.Size != 8 || len(result.SHA256) != 64 || *calls != 1 {
		t.Fatalf("download failed: %+v", result)
	}
	if filepath.Base(result.Path) != "user-7-1-document-55.pdf" {
		t.Fatal("untrusted filename used as path")
	}
	b, err := os.ReadFile(result.Path)
	requireOK(t, err)
	if string(b) != "pdf-data" {
		t.Fatal("wrong attachment bytes")
	}
	info, err := os.Stat(result.Path)
	requireOK(t, err)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("unsafe attachment permissions")
	}
	var metaAfter string
	err = a.index.db.QueryRow("SELECT value_json FROM state WHERE key='archive_metadata'").Scan(&metaAfter)
	requireOK(t, err)
	if before != fileText(t, a, "user-7") || metaBefore != metaAfter {
		t.Fatal("fetch modified archiver files")
	}
}

func TestFetchRefreshesExpiredReferenceOnce(t *testing.T) {
	m := attachmentMessage(8)
	f, calls := fakeFetcher(t, func(int) *tg.Message { return m }, []byte("pdf-data"), true)
	result := f.download(context.Background(), row("user-7", 1, "invoice"), PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
	if result.Status != "downloaded" || *calls != 2 {
		t.Fatalf("reference refresh failed: %+v", result)
	}
}

func TestFetchChecksDeletionAfterTransfer(t *testing.T) {
	m := attachmentMessage(8)
	f, _ := fakeFetcher(t, func(n int) *tg.Message {
		if n > 1 {
			return nil
		}
		return m
	}, []byte("pdf-data"), false)
	dir := t.TempDir()
	result := f.download(context.Background(), row("user-7", 1, "invoice"), PeerInfo{Kind: "user", ID: 7}, dir, 100)
	files, err := os.ReadDir(dir)
	requireOK(t, err)
	if result.Status != "skipped" || len(files) != 0 {
		t.Fatal("deleted attachment or partial download was committed")
	}
}

func TestFetchRejectsProtectedAndOversizeAttachments(t *testing.T) {
	for _, kind := range []string{"ttl", "protected", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			m := attachmentMessage(8)
			switch kind {
			case "ttl":
				m.TTLPeriod = 60
			case "protected":
				m.Noforwards = true
			case "oversize":
				m.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).Size = 1000
			}
			f, calls := fakeFetcher(t, func(int) *tg.Message { return m }, []byte("pdf-data"), false)
			result := f.download(context.Background(), row("user-7", 1, "invoice"), PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
			if result.Status != "skipped" || *calls != 0 {
				t.Fatalf("blocked attachment fetched: %+v", result)
			}
		})
	}
}

func TestFetchBoundsUnexpectedStreamAndRemovesPartialFile(t *testing.T) {
	m := attachmentMessage(0)
	f, _ := fakeFetcher(t, func(int) *tg.Message { return m }, []byte("oversize-stream"), false)
	dir := t.TempDir()
	result := f.download(context.Background(), row("user-7", 1, "invoice"), PeerInfo{Kind: "user", ID: 7}, dir, 4)
	files, err := os.ReadDir(dir)
	requireOK(t, err)
	if result.Status != "error" || len(files) != 0 {
		t.Fatal("stream limit bypassed or partial file retained")
	}
}

func TestFetchRejectsChangedAttachment(t *testing.T) {
	m := attachmentMessage(8)
	f, calls := fakeFetcher(t, func(int) *tg.Message { return m }, []byte("pdf-data"), false)
	record := row("user-7", 1, "invoice")
	record.Media = &MediaDetails{Kind: "document", File: &FileMetadata{ID: "54", Name: "old.pdf"}}
	result := f.download(context.Background(), record, PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
	if result.Status != "skipped" || *calls != 0 {
		t.Fatal("stale filename match downloaded a different attachment")
	}
}

func TestFetchDirectorySeparationIncludesSymlinkParents(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "archiver", "archive")
	requireOK(t, privateDir(archive))
	if _, _, _, err := fetchDirectories(archive, filepath.Dir(archive), ""); err == nil {
		t.Fatal("shared archiver session allowed")
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(archive, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symbolic link privilege unavailable")
		}
		requireOK(t, err)
	}
	if _, _, _, err := fetchDirectories(archive, filepath.Join(alias, "not-created"), ""); err == nil {
		t.Fatal("symlink parent bypassed isolation")
	}
	if _, _, _, err := fetchDirectories(archive, filepath.Join(root, "fetch"), filepath.Join(archive, "files")); err == nil {
		t.Fatal("download output inside archive accepted")
	}
	_, _, _, err := fetchDirectories(archive, filepath.Join(root, "fetch"), "")
	requireOK(t, err)
}

func TestReadOnlyRegexSearchFiltersTombstonesAndFilenames(t *testing.T) {
	a, r := fixture(t)
	m := attachmentMessage(8)
	requireOK(t, r.records([]tg.MessageClass{m}, true, 0))
	n := message(&tg.PeerUser{UserID: 7}, 2, "unrelated")
	requireOK(t, r.records([]tg.MessageClass{n}, true, 0))
	before := fileText(t, a, "user-7")
	s, err := readArchiveSnapshot(a.dir)
	requireOK(t, err)
	matches, err := s.search(`(?i)\.pdf$`, "user-7", 1)
	requireOK(t, err)
	if len(matches) != 1 || matches[0].MessageID != 1 {
		t.Fatal("filename regex did not match")
	}
	if before != fileText(t, a, "user-7") {
		t.Fatal("search wrote archive")
	}
	if _, err := s.search("[", "", 20); err == nil {
		t.Fatal("invalid regex accepted")
	}
	a.meta.Deleted["user-7"] = map[int]bool{1: true}
	requireOK(t, a.saveMeta())
	s, err = readArchiveSnapshot(a.dir)
	requireOK(t, err)
	matches, err = s.search(`(?i)\.pdf$`, "", 20)
	requireOK(t, err)
	if len(matches) != 0 {
		t.Fatal("search exposed tombstoned content before JSONL rewrite")
	}
}

func TestFetchChoosesLargestPhotoAndDoesNotReturnAuthorization(t *testing.T) {
	m := message(&tg.PeerChannel{ChannelID: 9}, 1, "")
	m.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 66, DCID: 2, AccessHash: 123, FileReference: []byte("secret"), Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "s", W: 100, H: 100, Size: 100}, &tg.PhotoSizeProgressive{Type: "y", W: 1000, H: 1000, Sizes: []int{200, 300}}}}}
	a, err := attachmentFrom(m, 1000)
	requireOK(t, err)
	if a.Size != 300 || a.Location.(*tg.InputPhotoFileLocation).ThumbSize != "y" {
		t.Fatal("did not select largest photo")
	}
	result := fetchResult{Peer: "channel-9", MessageID: 1, Status: "downloaded", Path: "file.jpg"}
	b, err := json.Marshal(result)
	requireOK(t, err)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "access_hash") {
		t.Fatal("download result leaked credentials")
	}
}

func TestByteLimitWriterRejectsBeforeWriting(t *testing.T) {
	var b bytes.Buffer
	w := &byteLimitWriter{Writer: &b, Remaining: 2}
	_, err := w.Write([]byte("three"))
	if err == nil || b.Len() != 0 {
		t.Fatal("writer saved oversized chunk")
	}
}

func TestFetchRejectsProtectionOrReplacementDuringTransfer(t *testing.T) {
	for _, kind := range []string{"protected", "changed"} {
		t.Run(kind, func(t *testing.T) {
			f, _ := fakeFetcher(t, func(n int) *tg.Message {
				m := attachmentMessage(8)
				if n > 1 {
					if kind == "protected" {
						m.Noforwards = true
					} else {
						m.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).ID = 56
					}
				}
				return m
			}, []byte("pdf-data"), false)
			dir := t.TempDir()
			result := f.download(context.Background(), row("user-7", 1, "invoice"), PeerInfo{Kind: "user", ID: 7}, dir, 100)
			files, err := os.ReadDir(dir)
			requireOK(t, err)
			if result.Status != "skipped" || len(files) != 0 {
				t.Fatal("post-transfer protection/identity check bypassed")
			}
		})
	}
}

func TestFetchReportsMessageTransportFailureAsError(t *testing.T) {
	f := &fetchRunner{api: tg.NewClient(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return errors.New("network unavailable") }))}
	result := f.download(context.Background(), row("user-7", 1, "invoice"), PeerInfo{Kind: "user", ID: 7}, t.TempDir(), 100)
	if result.Status != "error" {
		t.Fatal("network failure reported as a harmless skip")
	}
}
