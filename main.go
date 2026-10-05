package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

type terminalAuth struct{}

var errUnexpectedArchiveStop = errors.New("archive client stopped without a signal or failure")

func classifyArchiveExit(runErr, signalErr, failureErr error) (error, bool) {
	if failureErr != nil {
		return failureErr, false
	}
	if signalErr != nil {
		return nil, true
	}
	if runErr == nil {
		return errUnexpectedArchiveStop, false
	}
	return runErr, false
}

func readSecret(ctx context.Context, prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("interactive login requires a terminal")
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, state)
	fmt.Fprint(os.Stderr, prompt)
	type answer struct {
		v   string
		err error
	}
	result := make(chan answer, 1)
	go func() {
		v, err := readMaskedSecret(ctx, os.Stdin, os.Stderr)
		result <- answer{v, err}
	}()
	defer fmt.Fprint(os.Stderr, "\r\n")
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-result:
		if a.err != nil {
			return "", a.err
		}
		v := a.v
		if v == "" {
			return "", errors.New("empty login input")
		}
		return v, nil
	}
}

func (terminalAuth) Phone(ctx context.Context) (string, error) {
	v, err := readSecret(ctx, "Phone number (international format, masked): ")
	return strings.TrimSpace(v), err
}
func (terminalAuth) Password(ctx context.Context) (string, error) {
	return readSecret(ctx, "Two-factor password (masked): ")
}
func (terminalAuth) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	v, err := readSecret(ctx, "Telegram login code (masked): ")
	return strings.TrimSpace(v), err
}
func (terminalAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errors.New("account registration is unsupported; use an existing account")
}
func (terminalAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("account registration is unsupported")
}

func runArchive(args []string) error {
	fs := flag.NewFlagSet("telegram-jsonl archive", flag.ContinueOnError)
	data := fs.String("data", "./data-tdl", "single-account session and archive directory")
	login := fs.Bool("login", false, "allow interactive login when the saved session is unauthorized")
	interval := fs.Duration("interval", 2*time.Second, "minimum interval between RPC calls (at least 1s)")
	batch := fs.Int("batch", 50, "messages/dialogs per request (1-100)")
	every := fs.Duration("sync-every", time.Hour, "periodic history, deletion, and retention reconciliation (at least 10m)")
	clientMode := fs.String("client", "auto", "client backend: auto, native, or tdl")
	loginMethod := fs.String("login-method", "qr", "TDL login method: qr or code")
	relogin := fs.Bool("tdl-relogin", false, "with --login, explicitly replace the saved TDL session")
	checkClient := fs.Bool("check-client", false, "verify source-integrated client configuration without contacting Telegram")
	proxyAddress := fs.String("proxy", "", "SOCKS5 proxy URL")
	historyDays := fs.Int("history-days", 2, "rolling history lookback in days; 0 disables backfill, -1 enables all history")
	historySince := fs.String("history-since", "", "fixed history start: YYYY-MM-DD (UTC) or RFC3339; overrides rolling days")
	retentionDays := fs.String("retention-days", "auto", "JSONL retention: auto (history-days + 5), -1, or 1-36505 days")
	maxStorageBytes := fs.Int64("max-storage-bytes", 1<<30, "maximum active JSONL content in bytes; 0 disables the limit")
	reconcileWindowText := fs.String("reconcile-window", "1h", "edit/deletion reconciliation lookback; duration or all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *interval < time.Second || *batch < 1 || *batch > 100 || *every < 10*time.Minute || *maxStorageBytes < 0 {
		return errors.New("require interval >= 1s, batch 1-100, sync-every >= 10m, max-storage-bytes >= 0")
	}
	daysExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "history-days" {
			daysExplicit = true
		}
	})
	historyPolicy, err := parseHistoryConfig(*historyDays, *historySince, daysExplicit, time.Now())
	if err != nil {
		return err
	}
	historySinceOverride := time.Time{}
	if *historySince != "" {
		historySinceOverride = historyPolicy.Since
	}
	retentionPolicy, err := parseRetentionPolicy(*retentionDays, *historyDays)
	if err != nil {
		return err
	}
	reconcileWindow, err := parseReconcileWindow(*reconcileWindowText)
	if err != nil {
		return err
	}
	cfg, err := resolveSourceConfig(*clientMode, hostNamespace)
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
	if *checkClient {
		printSourceCheck(cfg)
		return nil
	}
	if err := privateDir(*data); err != nil {
		return err
	}
	lock, err := lockDirectory(*data)
	if err != nil {
		return err
	}
	defer lock.Close()
	// All managed writes use this prefix; only unfinished writes are removed.
	temps, err := filepath.Glob(filepath.Join(*data, ".write-*"))
	if err != nil {
		return err
	}
	for _, p := range temps {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancelCause(signalCtx)
	defer cancel(nil)
	logWarnf("Active JSONL storage limit: max_storage_bytes=%d; SQLite state, sessions, and completed downloads are excluded", *maxStorageBytes)
	logPrintf("Archive starting: client=%s data=%q interval=%s batch=%d sync_every=%s history_days=%d retention=%s max_storage_bytes=%d timezone=%s", cfg.Mode, *data, *interval, *batch, *every, *historyDays, retentionPolicy, *maxStorageBytes, time.Now().Format("MST -07:00"))
	fail := &failure{cancel: cancel}
	store, err := openArchive(filepath.Join(*data, "archive"), fail)
	if err != nil {
		return err
	}
	defer store.Close()
	peers, records := store.counts()
	logPrintf("Loaded archive: peers=%d records=%d", peers, records)
	protocol, err := openProtocolStore(filepath.Join(*data, "state.sqlite"), fail)
	if err != nil {
		return err
	}
	defer protocol.Close()
	gate, err := openGate(filepath.Join(*data, "state.sqlite"), *interval, fail)
	if err != nil {
		return err
	}
	defer gate.Close()
	r := &receiver{archive: store, protocol: protocol, batch: *batch, failure: fail, resync: make(chan struct{}, 1), historyPolicy: historyPolicy, historyDays: *historyDays, historySince: historySinceOverride, retention: retentionPolicy, maxStorageBytes: *maxStorageBytes, reconcileWindow: reconcileWindow}
	manager := updates.New(updates.Config{Handler: r.handler(), Storage: protocol, AccessHasher: protocol, OnChannelTooLong: func(id int64) {
		logPrintf("Update gap for channel-%d; scheduling history reconciliation", id)
		select {
		case r.resync <- struct{}{}:
		default:
		}
	}})
	var enabled atomic.Bool
	var bound atomic.Bool
	sessionStorage, err := sourceStorage(*data, cfg, fail, *relogin)
	if err != nil {
		return err
	}
	loginDispatcher := tg.NewUpdateDispatcher()
	tokens := qrlogin.OnLoginToken(loginDispatcher)
	// Difference responses are persisted through the archive handler before the
	// upstream update manager is allowed to advance its protocol cursor.
	clientOptions := []telegram.Middleware{gate, &reliableDifferenceMiddleware{
		active:  func() bool { return bound.Load() },
		handler: r.handler(),
		requestRepair: func(ctx context.Context, peer string) error {
			if err := store.requestRepair(peer); err != nil {
				return fail.report(err)
			}
			select {
			case r.resync <- struct{}{}:
			default:
			}
			return nil
		},
	}}
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{
		SessionStorage: sessionStorage,
		Resolver:       resolver,
		Middlewares:    clientOptions,
		UpdateHandler: updateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
			if err := loginDispatcher.Handle(ctx, u); err != nil {
				return err
			}
			if !enabled.Load() {
				return nil
			}
			return manager.Handle(ctx, u)
		}),
		Device: telegram.DeviceConfig{DeviceModel: "Local JSONL archive", AppVersion: "0.1.0", SystemLangCode: "en", LangCode: "en"},
	})
	logPrint("Connecting to Telegram")
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := loginSource(ctx, client, cfg, *login, *loginMethod, tokens)
		if err != nil {
			return err
		}
		if status.User == nil || status.User.Bot {
			return errors.New("a personal user account is required")
		}
		r.self = status.User.ID
		r.api = client.API()
		logPrintf("History backfill: %s; reconciliation window=%s", historyPolicy, *reconcileWindowText)
		if err := store.bind(r.self); err != nil {
			return err
		}
		if err := protocol.bind(r.self); err != nil {
			return err
		}
		bound.Store(true)
		runCtx, finish := context.WithCancel(ctx)
		defer finish()
		group, gctx := errgroup.WithContext(runCtx)
		ready := make(chan struct{})
		group.Go(func() error {
			return manager.Run(gctx, r.api, r.self, updates.AuthOptions{OnStart: func(context.Context) { enabled.Store(true); close(ready) }})
		})
		group.Go(func() error {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-gctx.Done():
					return gctx.Err()
				case <-ticker.C:
					peers, records := store.counts()
					logPrintf("Archive status: stored_peers=%d records=%d", peers, records)
				}
			}
		})
		group.Go(func() error {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case <-ready:
			}
			logPrint("Live update receiver started; no read receipts sent")
			return r.syncLoop(gctx, *every)
		})
		err = group.Wait()
		enabled.Store(false)
		return err
	})
	result, signaled := classifyArchiveExit(err, signalCtx.Err(), fail.check())
	if signaled {
		logPrint("Archive stopped by signal")
		return nil
	}
	return result
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version":
			fmt.Printf("telegram-jsonl %s (%s)\n", version, commit)
			return nil
		case "archive":
			return runArchive(args[1:])
		case "search":
			return runSearch(args[1:])
		case "fetch":
			return runFetch(args[1:])
		case "help", "--help", "-h":
			fmt.Println("Usage: telegram-jsonl <archive|search|fetch> [flags]\n  archive  persist messages and updates (resident)\n  search   search local JSONL with a regular expression (offline)\n  fetch    download matching attachments then exit (one shot)\nRun telegram-jsonl <command> --help for command flags.")
			return nil
		}
	}
	return runArchive(args)
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logErrorf("Stopped: %v", err)
		os.Exit(1)
	}
}
