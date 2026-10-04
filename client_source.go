// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/dcs"
	"go.etcd.io/bbolt"
	"golang.org/x/net/proxy"
	"telegram-jsonl/internal/tdllogin"
)

const hostNamespace = "local-jsonl"

type clientIdentity struct {
	Mode      string `json:"mode"`
	APIID     int    `json:"api_id"`
	Namespace string `json:"namespace"`
}

type sourceConfig struct {
	clientIdentity
	APIHash string
}

func resolveSourceConfig(mode, namespace string) (sourceConfig, error) {
	if mode == "auto" {
		mode = "tdl"
		if os.Getenv("TG_API_ID") != "" || os.Getenv("TG_API_HASH") != "" {
			mode = "native"
		}
	}
	if mode != "tdl" && mode != "native" {
		return sourceConfig{}, errors.New("client must be auto, tdl, or native")
	}
	c := sourceConfig{clientIdentity: clientIdentity{Mode: mode, Namespace: namespace}}
	if mode == "tdl" {
		credentials := tdllogin.Builtin()
		c.APIID, c.APIHash = credentials.ID, credentials.Hash
		return c, nil
	}
	id, err := strconv.Atoi(os.Getenv("TG_API_ID"))
	hash := os.Getenv("TG_API_HASH")
	if err != nil || id <= 0 || len(hash) != 32 {
		return c, errors.New("set TG_API_ID and TG_API_HASH for native client")
	}
	c.APIID, c.APIHash = id, hash
	return c, nil
}

func validateSessionBytes(data []byte) error {
	if len(data) == 0 || len(data) > 2*1024*1024 {
		return errors.New("invalid session data")
	}
	storage := &session.StorageMemory{}
	if err := storage.StoreSession(context.Background(), data); err != nil {
		return err
	}
	saved, err := (&session.Loader{Storage: storage}).Load(context.Background())
	if err != nil || saved.DC <= 0 || len(saved.AuthKey) != 256 || len(saved.AuthKeyID) != 8 {
		return errors.New("invalid session data")
	}
	return nil
}

// The previous host's database is only opened read-only. The original is retained.
func migrateLegacySession(dataDir, namespace string) (bool, error) {
	target := filepath.Join(dataDir, "session.json")
	if _, err := os.Lstat(target); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	legacy := filepath.Join(dataDir, "tdl-runtime", "storage", namespace)
	if _, err := os.Lstat(legacy); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	db, err := bbolt.Open(legacy, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return false, errors.New("cannot read legacy session; stop the previous host and retry")
	}
	defer db.Close()
	var encoded []byte
	if err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(namespace))
		if bucket == nil {
			return errors.New("legacy namespace missing")
		}
		if app := bucket.Get([]byte("app")); string(app) != "builtin" {
			return errors.New("legacy session does not use tdl builtin application identity")
		}
		encoded = append([]byte(nil), bucket.Get([]byte("session"))...)
		return validateSessionBytes(encoded)
	}); err != nil {
		return false, err
	}
	if err := atomicWrite(target, encoded); err != nil {
		return false, err
	}
	return true, nil
}

func sourceStorage(dataDir string, c sourceConfig, fail *failure, reset bool) (*stableSession, error) {
	path := filepath.Join(dataDir, "client.json")
	var identity clientIdentity
	err := readJSON(path, &identity)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && identity != c.clientIdentity {
		return nil, errors.New("session directory belongs to a different client identity or component")
	}
	if c.Mode == "tdl" && !reset {
		if _, err := migrateLegacySession(dataDir, c.Namespace); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(path, c.clientIdentity); err != nil {
		return nil, err
	}
	return &stableSession{path: filepath.Join(dataDir, "session.json"), failure: fail, ignoreExisting: reset}, nil
}

func validateLoginOptions(c sourceConfig, login, reset bool, method string) error {
	if method != "qr" && method != "code" {
		return errors.New("login-method must be qr or code")
	}
	if reset && (!login || c.Mode != "tdl") {
		return errors.New("--tdl-relogin requires --client tdl and --login")
	}
	return nil
}

func loginSource(ctx context.Context, client *telegram.Client, c sourceConfig, login bool, method string, tokens qrlogin.LoggedIn) (*auth.Status, error) {
	status, err := client.Auth().Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.Authorized {
		return status, nil
	}
	if !login {
		return nil, errors.New("session unauthorized; explicitly run with --login")
	}
	if method == "qr" {
		err = tdllogin.QRLogin(ctx, client, tokens, terminalAuth{}, os.Stderr)
	} else {
		err = auth.NewFlow(terminalAuth{}, auth.SendCodeOptions{}).Run(ctx, client.Auth())
	}
	if err != nil {
		return nil, err
	}
	return client.Auth().Status(ctx)
}

func printSourceCheck(c sourceConfig) {
	if c.Mode == "tdl" {
		fmt.Printf("Client verified: source-integrated tdl %s; no external executable required\n", tdllogin.UpstreamVersion)
	} else {
		fmt.Println("Client verified: native application credentials configured")
	}
}

func tdlResolver(address string) (dcs.Resolver, error) {
	if address == "" {
		return nil, nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "socks5" || u.Host == "" {
		return nil, errors.New("only socks5:// proxies are supported")
	}
	d, err := proxy.FromURL(u, &net.Dialer{})
	if err != nil {
		return nil, errors.New("invalid SOCKS5 proxy")
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("proxy lacks context cancellation")
	}
	return dcs.Plain(dcs.PlainOptions{Dial: cd.DialContext}), nil
}

func currentSessionIdentity(dataDir string) (clientIdentity, error) {
	var c clientIdentity
	b, err := os.ReadFile(filepath.Join(dataDir, "client.json"))
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}
