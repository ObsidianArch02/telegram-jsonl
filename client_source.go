// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/dcs"
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

func sourceStorage(dataDir string, c sourceConfig, fail *failure, reset bool) (*stableSession, error) {
	store, err := openSQLite(filepath.Join(dataDir, "state.sqlite"))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	var identity clientIdentity
	err = store.ReadJSON("client", &identity)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && identity != c.clientIdentity {
		return nil, errors.New("session directory belongs to a different client identity or component")
	}
	if os.IsNotExist(err) && !reset {
		_, sessionErr := os.Stat(filepath.Join(dataDir, "session.json"))
		if sessionErr == nil {
			return nil, errors.New("existing session has no SQLite client identity; use a fresh data directory or explicitly reauthorize with --login --tdl-relogin")
		}
		if !os.IsNotExist(sessionErr) {
			return nil, sessionErr
		}
	}
	if err := store.WriteJSON("client", c.clientIdentity); err != nil {
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
	log.Print("Checking saved Telegram authorization")
	status, err := client.Auth().Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.Authorized {
		log.Print("Saved Telegram authorization reused")
		return status, nil
	}
	if !login {
		return nil, errors.New("session unauthorized; explicitly run with --login")
	}
	log.Printf("Telegram authorization required; starting %s login", method)
	if method == "qr" {
		err = tdllogin.QRLogin(ctx, client, tokens, terminalAuth{}, os.Stderr)
	} else {
		err = auth.NewFlow(terminalAuth{}, auth.SendCodeOptions{}).Run(ctx, client.Auth())
	}
	if err != nil {
		return nil, err
	}
	status, err = client.Auth().Status(ctx)
	if err == nil && status.Authorized {
		log.Print("Telegram login successful; authorization saved")
	}
	return status, err
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
	err := readStateJSON(filepath.Join(dataDir, "state.sqlite"), "client", &c)
	return c, err
}
