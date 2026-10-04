package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

type terminalAuth struct{}

func readSecret(ctx context.Context, prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("interactive login requires a terminal")
	}
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, state)
	fmt.Fprint(os.Stderr, prompt)
	type answer struct {
		b   []byte
		err error
	}
	result := make(chan answer, 1)
	go func() { b, err := term.ReadPassword(fd); result <- answer{b, err} }()
	defer fmt.Fprintln(os.Stderr)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-result:
		if a.err != nil {
			return "", a.err
		}
		v := strings.TrimSpace(string(a.b))
		for i := range a.b {
			a.b[i] = 0
		}
		if v == "" {
			return "", errors.New("empty login input")
		}
		return v, nil
	}
}

func (terminalAuth) Phone(ctx context.Context) (string, error) {
	return readSecret(ctx, "Phone number (international format, hidden): ")
}
func (terminalAuth) Password(ctx context.Context) (string, error) {
	return readSecret(ctx, "Two-factor password (hidden): ")
}
func (terminalAuth) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return readSecret(ctx, "Telegram login code (hidden): ")
}
func (terminalAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errors.New("account registration is unsupported; use an existing account")
}
func (terminalAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("account registration is unsupported")
}

func runArchive(args []string) error {
	fs := flag.NewFlagSet("telegram-jsonl archive", flag.ContinueOnError)
	data := fs.String("data", "./data", "single-account session and archive directory")
	login := fs.Bool("login", false, "allow interactive login when the saved session is unauthorized")
	once := fs.Bool("once", false, "synchronize history and deletions, then exit")
	interval := fs.Duration("interval", 2*time.Second, "minimum interval between RPC calls (at least 1s)")
	batch := fs.Int("batch", 50, "messages/dialogs per request (1-100)")
	every := fs.Duration("sync-every", 6*time.Hour, "periodic history and deletion reconciliation (at least 10m)")
	clientMode := fs.String("client", "auto", "client backend: auto, native, or tdl")
	tdlBinary := fs.String("tdl-bin", "", "bundled TDL host path (default: tools/tdl beside this executable)")
	loginMethod := fs.String("login-method", "qr", "TDL login method: qr or code")
	relogin := fs.Bool("tdl-relogin", false, "with --login, explicitly replace the saved TDL session")
	checkClient := fs.Bool("check-client", false, "verify the bundled TDL host without contacting Telegram")
	proxyAddress := fs.String("proxy", "", "TDL SOCKS5 proxy URL")
	historyDays := fs.Int("history-days", 30, "history lookback in days; 0 disables backfill, -1 enables all history")
	historySince := fs.String("history-since", "", "history start: YYYY-MM-DD (UTC) or RFC3339; overrides default 30 days")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *interval < time.Second || *batch < 1 || *batch > 100 || *every < 10*time.Minute {
		return errors.New("require interval >= 1s, batch 1-100, sync-every >= 10m")
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
	var ext *tdlEnvironment
	if path := os.Getenv("TDL_EXTENSION"); path != "" {
		ext, err = loadTDLEnvironment(path)
		if err != nil {
			return err
		}
	}
	mode := *clientMode
	if mode == "auto" {
		mode = "native"
		if ext != nil {
			mode = "tdl"
		}
	}
	if mode != "native" && mode != "tdl" {
		return errors.New("client must be auto, native, or tdl")
	}
	if mode == "native" && ext != nil {
		return errors.New("TDL extension cannot select the native backend")
	}
	if mode == "tdl" && ext == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err := launchTDL(ctx, tdlLaunchOptions{Data: *data, Binary: *tdlBinary, Login: *login, Relogin: *relogin, LoginMethod: *loginMethod, CheckOnly: *checkClient, Proxy: *proxyAddress, Once: *once, Interval: *interval, Batch: *batch, SyncEvery: *every, History: historyPolicy})
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if *checkClient && ext != nil {
		fmt.Println("TDL extension environment verified; session loaded without contacting Telegram")
		return nil
	}
	if *checkClient || *relogin {
		return errors.New("--check-client and --tdl-relogin are TDL launcher options")
	}
	apiID, apiHash := 0, ""
	if ext != nil {
		apiID, apiHash = ext.AppID, ext.AppHash
		if *login {
			return errors.New("log in through the TDL host before running its extension")
		}
		dataExplicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "data" {
				dataExplicit = true
			}
		})
		if !dataExplicit {
			*data = filepath.Join(ext.DataDir, ext.Namespace)
		}
	} else {
		if *proxyAddress != "" {
			return errors.New("--proxy is currently supported only by the TDL backend")
		}
		apiID, err = strconv.Atoi(os.Getenv("TG_API_ID"))
		if err != nil || apiID <= 0 {
			return errors.New("set TG_API_ID, or select --client tdl for direct client login")
		}
		apiHash = os.Getenv("TG_API_HASH")
		if len(apiHash) != 32 {
			return errors.New("set TG_API_HASH to your application hash")
		}
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
	fail := &failure{cancel: cancel}
	store, err := openArchive(filepath.Join(*data, "archive"), fail)
	if err != nil {
		return err
	}
	protocol, err := openProtocolStore(filepath.Join(*data, "updates.json"), fail)
	if err != nil {
		return err
	}
	gate, err := openGate(filepath.Join(*data, "cooldown.json"), *interval, fail)
	if err != nil {
		return err
	}
	r := &receiver{archive: store, protocol: protocol, batch: *batch, failure: fail, resync: make(chan struct{}, 1), historyPolicy: historyPolicy}
	manager := updates.New(updates.Config{Handler: r.handler(), Storage: protocol, AccessHasher: protocol, OnChannelTooLong: func(id int64) {
		log.Printf("Update gap for channel-%d; scheduling history reconciliation", id)
		select {
		case r.resync <- struct{}{}:
		default:
		}
	}})
	var enabled atomic.Bool
	var sessionStorage telegram.SessionStorage = &stableSession{path: filepath.Join(*data, "session.json"), failure: fail}
	var resolverAddress string
	if ext != nil {
		var memory *session.StorageMemory
		memory, err = ext.sessionStorage()
		if err != nil {
			return err
		}
		sessionStorage = memory
		resolverAddress = ext.Proxy
	}
	resolver, err := tdlResolver(resolverAddress)
	if err != nil {
		return err
	}
	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: sessionStorage,
		Resolver:       resolver,
		Middlewares:    []telegram.Middleware{gate},
		UpdateHandler: updateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
			if !enabled.Load() {
				return nil
			}
			return manager.Handle(ctx, u)
		}),
		Device: telegram.DeviceConfig{DeviceModel: "Local JSONL archive", AppVersion: "0.1.0", SystemLangCode: "en", LangCode: "en"},
	})
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			if ext != nil {
				return errors.New("TDL session is unauthorized; use --client tdl --login --tdl-relogin explicitly")
			}
			if !*login {
				return errors.New("session is unauthorized; run once with --login in a terminal")
			}
			if err := auth.NewFlow(terminalAuth{}, auth.SendCodeOptions{}).Run(ctx, client.Auth()); err != nil {
				return err
			}
			status, err = client.Auth().Status(ctx)
			if err != nil {
				return err
			}
		}
		if status.User == nil || status.User.Bot {
			return errors.New("a personal user account is required")
		}
		r.self = status.User.ID
		r.api = client.API()
		log.Printf("History backfill: %s", historyPolicy)
		if err := store.bind(r.self); err != nil {
			return err
		}
		if err := protocol.bind(r.self); err != nil {
			return err
		}
		runCtx, finish := context.WithCancel(ctx)
		defer finish()
		group, gctx := errgroup.WithContext(runCtx)
		ready := make(chan struct{})
		var completed atomic.Bool
		group.Go(func() error {
			return manager.Run(gctx, r.api, r.self, updates.AuthOptions{OnStart: func(context.Context) { enabled.Store(true); close(ready) }})
		})
		group.Go(func() error {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case <-ready:
			}
			log.Print("Receiving live updates; saved session reused, no read receipts sent")
			if err := r.syncLoop(gctx, *every, *once); err != nil {
				return err
			}
			completed.Store(true)
			finish()
			return nil
		})
		err = group.Wait()
		enabled.Store(false)
		if *once && completed.Load() && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})
	if err := fail.check(); err != nil {
		return err
	}
	if signalCtx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
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
	log.SetFlags(log.LstdFlags | log.LUTC)
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Printf("Stopped: %v", err)
		os.Exit(1)
	}
}
