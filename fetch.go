package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const fetchNamespace = "local-fetch"

var errAttachmentUnavailable = errors.New("attachment unavailable")

var safeExtension = regexp.MustCompile(`^\.[a-zA-Z0-9]{1,10}$`)

type fetchResult struct {
	Peer      string `json:"peer"`
	MessageID int    `json:"message_id"`
	Status    string `json:"status"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type fetchRunner struct {
	api        *tg.Client
	fileClient func(context.Context, int) (downloader.Client, func(), error)
	catalog    *sqliteStore
}

type attachment struct {
	ID        int64
	Kind      string
	DC        int
	Size      int64
	Extension string
	Location  tg.InputFileLocationClass
}

func isInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent := abs
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(parent) == parent {
			return "", err
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
}

func fetchDirectories(archiveDir, dataDir, outputDir string) (string, string, string, error) {
	a, err := canonicalPath(archiveDir)
	if err != nil {
		return "", "", "", err
	}
	d, err := canonicalPath(dataDir)
	if err != nil {
		return "", "", "", err
	}
	if outputDir == "" {
		outputDir = filepath.Join(d, "attachments")
	}
	out, err := canonicalPath(outputDir)
	if err != nil {
		return "", "", "", err
	}
	if isInside(a, d) || isInside(d, a) || d == filepath.Dir(a) || isInside(a, out) || isInside(out, a) {
		return "", "", "", errors.New("fetch state/output must be separate from the archive directory and archiver state")
	}
	return a, d, out, nil
}

func fileExtension(name, mimetype string) string {
	if ext := filepath.Ext(name); safeExtension.MatchString(ext) {
		return strings.ToLower(ext)
	}
	if mimetype == "image/jpeg" {
		return ".jpg"
	}
	if exts, err := mime.ExtensionsByType(mimetype); err == nil {
		for _, ext := range exts {
			if safeExtension.MatchString(ext) {
				return strings.ToLower(ext)
			}
		}
	}
	return ".bin"
}

func attachmentFrom(m *tg.Message, maxBytes int64) (attachment, error) {
	if ephemeral(m) {
		return attachment{}, errors.New("self-destructing, protected, or restricted content")
	}
	var a attachment
	switch media := m.Media.(type) {
	case *tg.MessageMediaDocument:
		a.Kind = "document"
		d, ok := media.Document.(*tg.Document)
		if !ok {
			return a, errors.New("document is unavailable")
		}
		a.ID, a.DC, a.Size = d.ID, d.DCID, d.Size
		name := ""
		for _, attr := range d.Attributes {
			if attr, ok := attr.(*tg.DocumentAttributeFilename); ok {
				name = attr.FileName
			}
		}
		a.Extension = fileExtension(name, d.MimeType)
		a.Location = &tg.InputDocumentFileLocation{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}
	case *tg.MessageMediaPhoto:
		a.Kind = "photo"
		p, ok := media.Photo.(*tg.Photo)
		if !ok {
			return a, errors.New("photo is unavailable")
		}
		var sizeType string
		var area int64
		for _, size := range p.Sizes {
			var width, height int
			var sizeBytes int64
			var kind string
			switch s := size.(type) {
			case *tg.PhotoSize:
				width, height, sizeBytes, kind = s.W, s.H, int64(s.Size), s.Type
			case *tg.PhotoSizeProgressive:
				width, height, kind = s.W, s.H, s.Type
				for _, n := range s.Sizes {
					sizeBytes = max(sizeBytes, int64(n))
				}
			}
			if candidate := int64(width) * int64(height); kind != "" && candidate > area {
				area, sizeType, a.Size = candidate, kind, sizeBytes
			}
		}
		if sizeType == "" {
			return a, errors.New("no downloadable photo size")
		}
		a.ID, a.DC, a.Extension = p.ID, p.DCID, ".jpg"
		a.Location = &tg.InputPhotoFileLocation{ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileReference, ThumbSize: sizeType}
	default:
		return a, errors.New("message has no supported downloadable attachment")
	}
	if a.ID <= 0 || a.DC <= 0 || a.Size < 0 {
		return a, errors.New("invalid attachment metadata")
	}
	if a.Size > maxBytes {
		return a, fmt.Errorf("attachment exceeds --max-file-bytes (%d)", maxBytes)
	}
	return a, nil
}

func archivedAttachmentKind(record Record) string {
	if record.Media != nil {
		switch record.Media.Kind {
		case "photo":
			return "photo"
		case "document", "voice", "audio", "video", "video_note", "animation", "sticker":
			return "document"
		}
	}
	switch record.MediaType {
	case "messageMediaPhoto":
		return "photo"
	case "messageMediaDocument":
		return "document"
	}
	return ""
}

func (f *fetchRunner) message(ctx context.Context, record Record, peer PeerInfo) (*tg.Message, error) {
	input := []tg.InputMessageClass{&tg.InputMessageID{ID: record.MessageID}}
	var result tg.MessagesMessagesClass
	var err error
	if peer.Kind == "channel" {
		result, err = f.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.AccessHash}, ID: input})
	} else {
		result, err = f.api.MessagesGetMessages(ctx, input)
	}
	if err != nil {
		if inaccessible(err) {
			return nil, fmt.Errorf("%w: message access revoked", errAttachmentUnavailable)
		}
		return nil, err
	}
	messages, err := unpack(result)
	if err != nil {
		return nil, err
	}
	for _, chat := range messages.GetChats() {
		switch c := chat.(type) {
		case *tg.Chat:
			if peer.Kind == "chat" && c.ID == peer.ID && c.Noforwards {
				return nil, fmt.Errorf("%w: chat has protected content", errAttachmentUnavailable)
			}
		case *tg.Channel:
			if peer.Kind == "channel" && c.ID == peer.ID && c.Noforwards {
				return nil, fmt.Errorf("%w: chat has protected content", errAttachmentUnavailable)
			}
		}
	}
	for _, item := range messages.GetMessages() {
		if m, ok := item.(*tg.Message); ok && m.ID == record.MessageID && peerKey(m.PeerID) == record.Peer {
			if ephemeral(m) {
				return nil, fmt.Errorf("%w: self-destructing, protected, or restricted content", errAttachmentUnavailable)
			}
			return m, nil
		}
	}
	return nil, fmt.Errorf("%w: message was deleted or is unavailable", errAttachmentUnavailable)
}

type byteLimitWriter struct {
	Writer    io.Writer
	Remaining int64
	Written   int64
	Progress  func(int64)
}

func (w *byteLimitWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.Remaining {
		return 0, errors.New("attachment stream exceeds --max-file-bytes")
	}
	n, err := w.Writer.Write(b)
	w.Remaining -= int64(n)
	w.Written += int64(n)
	if n > 0 && w.Progress != nil {
		w.Progress(w.Written)
	}
	return n, err
}

func (f *fetchRunner) download(ctx context.Context, record Record, peer PeerInfo, output string, maxBytes int64) (result fetchResult) {
	started := time.Now()
	logPrintf("Attachment check started: peer=%s message_id=%d", record.Peer, record.MessageID)
	defer func() {
		logPrintf("Attachment result: peer=%s message_id=%d status=%s bytes=%d elapsed=%s", result.Peer, result.MessageID, result.Status, result.Size, time.Since(started).Round(time.Millisecond))
	}()
	result = fetchResult{Peer: record.Peer, MessageID: record.MessageID, Status: "skipped"}
	m, err := f.message(ctx, record, peer)
	if err != nil {
		if !errors.Is(err, errAttachmentUnavailable) {
			result.Status = "error"
		}
		result.Reason = err.Error()
		return
	}
	a, err := attachmentFrom(m, maxBytes)
	if err != nil {
		result.Reason = err.Error()
		return
	}
	if record.Media != nil && record.Media.File != nil && record.Media.File.ID != strconv.FormatInt(a.ID, 10) {
		result.Reason = "attachment changed; refresh the JSONL archive before downloading"
		return
	}
	if kind := archivedAttachmentKind(record); kind != "" && kind != a.Kind {
		result.Reason = "attachment type changed; refresh the JSONL archive before downloading"
		return
	}
	if !validPeer.MatchString(record.Peer) {
		result.Status = "error"
		result.Reason = "invalid peer"
		return
	}
	if err := privateDir(output); err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	tmp, err := os.CreateTemp(output, ".fetch-*")
	if err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0600); err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	var bytes int64
	var digest string
	for attempt := 0; attempt < 2; attempt++ {
		logPrintf("Attachment transfer starting: peer=%s message_id=%d attempt=%d expected_bytes=%d", record.Peer, record.MessageID, attempt+1, a.Size)
		if err := tmp.Truncate(0); err != nil {
			result.Status = "error"
			result.Reason = err.Error()
			return
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			result.Status = "error"
			result.Reason = err.Error()
			return
		}
		client, closeClient, err := f.fileClient(ctx, a.DC)
		if err != nil {
			result.Status = "error"
			result.Reason = err.Error()
			return
		}
		hash := sha256.New()
		writer := &byteLimitWriter{Writer: io.MultiWriter(tmp, hash), Remaining: maxBytes}
		lastProgress := time.Now()
		writer.Progress = func(written int64) {
			if time.Since(lastProgress) >= 5*time.Second {
				logPrintf("Attachment transfer progress: peer=%s message_id=%d bytes=%d expected_bytes=%d", record.Peer, record.MessageID, written, a.Size)
				lastProgress = time.Now()
			}
		}
		_, err = downloader.NewDownloader().Download(client, a.Location).Stream(ctx, writer)
		closeClient()
		if err == nil {
			bytes, digest = writer.Written, hex.EncodeToString(hash.Sum(nil))
			break
		}
		if attempt == 0 && tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID") {
			logPrintf("Attachment reference expired; refreshing once: peer=%s message_id=%d", record.Peer, record.MessageID)
			refreshed, refreshErr := f.message(ctx, record, peer)
			if refreshErr != nil {
				if !errors.Is(refreshErr, errAttachmentUnavailable) {
					result.Status = "error"
				}
				result.Reason = refreshErr.Error()
				return
			}
			fresh, refreshErr := attachmentFrom(refreshed, maxBytes)
			if refreshErr != nil || fresh.ID != a.ID || fresh.Kind != a.Kind {
				result.Reason = "attachment changed or became unavailable during reference refresh"
				return
			}
			a = fresh
			continue
		}
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	if a.Size > 0 && bytes != a.Size {
		result.Status = "error"
		result.Reason = "incomplete attachment download"
		return
	}
	// Validate again immediately before committing the file, including a deletion
	// or protection change while transferring. This component never writes JSONL.
	logPrintf("Attachment transfer complete; rechecking availability: peer=%s message_id=%d bytes=%d", record.Peer, record.MessageID, bytes)
	current, err := f.message(ctx, record, peer)
	if err != nil {
		if !errors.Is(err, errAttachmentUnavailable) {
			result.Status = "error"
		}
		result.Reason = err.Error()
		return
	}
	currentAttachment, err := attachmentFrom(current, maxBytes)
	if err != nil || currentAttachment.ID != a.ID || currentAttachment.Kind != a.Kind {
		result.Reason = "attachment changed or became unavailable during download"
		return
	}
	if err := tmp.Sync(); err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	if err := tmp.Close(); err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	path := filepath.Join(output, fmt.Sprintf("%s-%d-%s-%d%s", record.Peer, record.MessageID, a.Kind, a.ID, a.Extension))
	if err := replaceFile(tmp.Name(), path); err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	if err := syncDirectory(output); err != nil {
		result.Status = "error"
		result.Reason = err.Error()
		return
	}
	result.Status, result.Path, result.Size, result.SHA256 = "downloaded", path, bytes, digest
	if f.catalog != nil {
		completedRecord := record
		completedRecord.Media = mediaDetails(current.Media)
		completedRecord.MediaType = current.Media.TypeName()
		if err := f.catalog.RecordDownload(completedRecord, strconv.FormatInt(a.ID, 10), result); err != nil {
			result.Status = "error"
			result.Reason = fmt.Sprintf("file downloaded, but SQLite file ledger was not updated: %v", err)
		}
	}
	return
}

func runFetch(args []string) error {
	fs := flag.NewFlagSet("telegram-jsonl fetch", flag.ContinueOnError)
	archiveDir := fs.String("archive", "./data-tdl/archive", "JSONL directory; SQLite file ledger updated")
	data := fs.String("data", "./fetch-data", "downloader's separate stable session directory")
	output := fs.String("output", "", "download directory (default: fetch-data/attachments)")
	pattern := fs.String("pattern", "", "regular expression; run search first to preview matches")
	peerFilter := fs.String("peer", "", "optional peer filter, e.g. channel-123")
	limit := fs.Int("limit", 20, "maximum matched messages (1-1000)")
	maxBytes := fs.Int64("max-file-bytes", 256*1024*1024, "maximum bytes per downloaded attachment")
	interval := fs.Duration("interval", 2*time.Second, "minimum RPC interval (at least 1s)")
	clientMode := fs.String("client", "tdl", "client backend: tdl or native")
	login := fs.Bool("login", false, "allow first login for the downloader's own session")
	relogin := fs.Bool("tdl-relogin", false, "with --login, explicitly replace the downloader's TDL session")
	loginMethod := fs.String("login-method", "qr", "TDL login: qr or code")
	checkOnly := fs.Bool("check-client", false, "verify source-integrated downloader without accessing Telegram")
	proxyAddress := fs.String("proxy", "", "TDL SOCKS5 proxy URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *interval < time.Second || *maxBytes < 1 || *maxBytes > 10*1024*1024*1024 {
		return errors.New("require interval >= 1s and max-file-bytes from 1 to 10 GiB")
	}
	if *clientMode != "tdl" && *clientMode != "native" {
		return errors.New("client must be tdl or native")
	}
	cfg, err := resolveSourceConfig(*clientMode, fetchNamespace)
	if err != nil {
		return err
	}
	if err := validateLoginOptions(cfg, *login, *relogin, *loginMethod); err != nil {
		return err
	}
	resolver, err := tdlResolver(*proxyAddress)
	if err != nil {
		return err
	}
	if *checkOnly {
		printSourceCheck(cfg)
		return nil
	}
	a, d, out, err := fetchDirectories(*archiveDir, *data, *output)
	if err != nil {
		return err
	}
	snapshot, err := readArchiveSnapshot(a)
	if err != nil && !*checkOnly {
		return err
	}
	var matches []Record
	if !*checkOnly {
		matches, err = snapshot.search(*pattern, *peerFilter, *limit)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			logPrint("No local messages matched")
			return nil
		}
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logPrintf("Fetch starting: client=%s matches=%d output=%q interval=%s max_file_bytes=%d timezone=%s", cfg.Mode, len(matches), out, *interval, *maxBytes, time.Now().Format("MST -07:00"))
	if err := privateDir(d); err != nil {
		return err
	}
	lock, err := lockDirectory(d)
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := context.WithCancelCause(signalCtx)
	defer cancel(nil)
	fail := &failure{cancel: cancel}
	gate, err := openGate(filepath.Join(d, "state.sqlite"), *interval, fail)
	if err != nil {
		return err
	}
	defer gate.Close()
	storage, err := sourceStorage(d, cfg, fail, *relogin)
	if err != nil {
		return err
	}
	loginDispatcher := tg.NewUpdateDispatcher()
	tokens := qrlogin.OnLoginToken(loginDispatcher)
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{SessionStorage: storage, Resolver: resolver, Middlewares: []telegram.Middleware{gate}, UpdateHandler: loginDispatcher, Device: telegram.DeviceConfig{DeviceModel: "Local attachment fetch", AppVersion: "0.2.0", SystemLangCode: "en", LangCode: "en"}})
	logPrint("Connecting downloader to Telegram")
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := loginSource(ctx, client, cfg, *login, *loginMethod, tokens)
		if err != nil {
			return err
		}
		if status.User == nil || status.User.Bot || status.User.ID != snapshot.Meta.AccountID {
			return errors.New("downloader must log in to the same user account as the archive")
		}
		catalog, err := openSQLite(filepath.Join(a, "index.sqlite"))
		if err != nil {
			return err
		}
		defer catalog.Close()
		var archiveAccount int64
		if err := catalog.ReadJSON("archive_account", &archiveAccount); err != nil {
			return err
		}
		if archiveAccount != status.User.ID {
			return errors.New("archive account changed before attachment download")
		}
		runner := &fetchRunner{api: client.API(), catalog: catalog, fileClient: func(ctx context.Context, dc int) (downloader.Client, func(), error) {
			conn, err := client.DC(ctx, dc, 1)
			if err != nil {
				return nil, nil, err
			}
			return tg.NewClient(gate.Handle(conn)), func() { _ = conn.Close() }, nil
		}}
		enc := json.NewEncoder(os.Stdout)
		failed, downloaded, skipped := 0, 0, 0
		for index, record := range matches {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p, ok := snapshot.Meta.Peers[record.Peer]
			if !ok {
				return errors.New("peer missing from archive metadata")
			}
			logPrintf("Processing attachment: %d/%d peer=%s message_id=%d", index+1, len(matches), record.Peer, record.MessageID)
			result := runner.download(ctx, record, p, out, *maxBytes)
			switch result.Status {
			case "error":
				failed++
			case "downloaded":
				downloaded++
			case "skipped":
				skipped++
			}
			if err := enc.Encode(result); err != nil {
				return err
			}
		}
		logPrintf("Fetch complete: downloaded=%d skipped=%d failed=%d", downloaded, skipped, failed)
		if failed > 0 {
			return fmt.Errorf("%d attachment downloads failed; see result statuses", failed)
		}
		return nil
	})
	if failureErr := fail.check(); failureErr != nil {
		return failureErr
	}
	if signalCtx.Err() != nil {
		logPrint("Fetch stopped by signal")
		return nil
	}
	return err
}
