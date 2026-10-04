package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/dcs"
	"golang.org/x/net/proxy"
)

// TDL's documented extension environment. The host supplies its application
// identity and session; no credentials are copied out of another client's binary.
type tdlEnvironment struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	AppID     int    `json:"app_id"`
	AppHash   string `json:"app_hash"`
	Session   []byte `json:"session"`
	DataDir   string `json:"data_dir"`
	Proxy     string `json:"proxy"`
	NTP       string `json:"ntp"`
}

const tdlBuiltinID = 15055931

var safeNamespace = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func loadTDLEnvironment(path string) (*tdlEnvironment, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("cannot read TDL extension environment")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 2*1024*1024 {
		return nil, errors.New("TDL extension environment must be a private regular file, at most 2 MiB")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("TDL extension environment must belong to this user")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open TDL extension environment")
	}
	defer f.Close()
	var env tdlEnvironment
	decoder := json.NewDecoder(io.LimitReader(f, 2*1024*1024+1))
	if err := decoder.Decode(&env); err != nil {
		return nil, errors.New("invalid TDL extension environment JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("extra data in TDL extension environment")
	}
	if env.AppID != tdlBuiltinID {
		return nil, errors.New("TDL session must use tdl's builtin application identity; use the bundled host for login")
	}
	if _, err := hex.DecodeString(env.AppHash); err != nil || len(env.AppHash) != 32 {
		return nil, errors.New("invalid TDL application hash")
	}
	if !safeNamespace.MatchString(env.Namespace) || env.Name == "" || !filepath.IsAbs(env.DataDir) {
		return nil, errors.New("invalid TDL extension identity or directory")
	}
	if env.NTP != "" {
		return nil, errors.New("TDL NTP override is unsupported by this prototype")
	}
	if len(env.Session) == 0 {
		return nil, errors.New("TDL session is missing; log in with the bundled host first")
	}
	storage, err := env.sessionStorage()
	if err != nil {
		return nil, err
	}
	data, err := (&session.Loader{Storage: storage}).Load(context.Background())
	if err != nil || data.DC <= 0 || len(data.AuthKey) != 256 || len(data.AuthKeyID) != 8 {
		return nil, errors.New("invalid TDL session data")
	}
	return &env, nil
}

func (env *tdlEnvironment) sessionStorage() (*session.StorageMemory, error) {
	s := &session.StorageMemory{}
	if err := s.StoreSession(context.Background(), env.Session); err != nil {
		return nil, errors.New("cannot initialize TDL session")
	}
	return s, nil
}

func tdlResolver(address string) (dcs.Resolver, error) {
	if address == "" {
		return nil, nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "socks5" || u.Host == "" {
		return nil, errors.New("this prototype supports only socks5:// TDL proxies")
	}
	dialer, err := proxy.FromURL(u, &net.Dialer{})
	if err != nil {
		return nil, errors.New("invalid TDL SOCKS5 proxy")
	}
	ctxDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("TDL proxy lacks context cancellation")
	}
	return dcs.Plain(dcs.PlainOptions{Dial: ctxDialer.DialContext}), nil
}
