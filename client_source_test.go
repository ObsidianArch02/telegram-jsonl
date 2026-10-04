// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/session"
	"telegram-jsonl/internal/tdllogin"
)

func validSession(t *testing.T) []byte {
	t.Helper()
	storage := &session.StorageMemory{}
	requireOK(t, (&session.Loader{Storage: storage}).Save(context.Background(), &session.Data{DC: 2, Addr: "127.0.0.1:443", AuthKey: make([]byte, 256), AuthKeyID: make([]byte, 8)}))
	b, err := storage.Bytes(nil)
	requireOK(t, err)
	return b
}

func TestSourceIntegrationNeedsNoToolOrUserCredentials(t *testing.T) {
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_API_HASH", "")
	c, err := resolveSourceConfig("auto", hostNamespace)
	requireOK(t, err)
	if c.Mode != "tdl" || c.APIID != tdllogin.Builtin().ID || c.APIHash != tdllogin.Builtin().Hash {
		t.Fatal("builtin source identity was not selected")
	}
	if c.Namespace == fetchNamespace {
		t.Fatal("archive selected fetch identity")
	}
	if _, err := resolveSourceConfig("bad", hostNamespace); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestSourceIntegrationNativeCredentialsRequireCompletePair(t *testing.T) {
	t.Setenv("TG_API_ID", "12345")
	t.Setenv("TG_API_HASH", "")
	if _, err := resolveSourceConfig("auto", hostNamespace); err == nil {
		t.Fatal("partial native credentials silently fell back to builtin")
	}
	t.Setenv("TG_API_HASH", strings.Repeat("a", 32))
	c, err := resolveSourceConfig("auto", hostNamespace)
	requireOK(t, err)
	if c.Mode != "native" || c.APIID != 12345 {
		t.Fatal("native credentials not selected")
	}
}

func TestSourceIntegrationSeparatesArchiveAndFetchIdentity(t *testing.T) {
	c, err := resolveSourceConfig("tdl", hostNamespace)
	requireOK(t, err)
	dir := t.TempDir()
	_, err = sourceStorage(dir, c, &failure{}, false)
	requireOK(t, err)
	fetch, err := resolveSourceConfig("tdl", fetchNamespace)
	requireOK(t, err)
	if _, err := sourceStorage(dir, fetch, &failure{}, false); err == nil {
		t.Fatal("fetch reused archive session")
	}
	saved, err := currentSessionIdentity(dir)
	requireOK(t, err)
	if saved != c.clientIdentity {
		t.Fatal("failed bind changed client identity")
	}
}

func TestSourceIntegrationExplicitReloginDoesNotDeleteSavedSession(t *testing.T) {
	c, err := resolveSourceConfig("tdl", fetchNamespace)
	requireOK(t, err)
	dir := t.TempDir()
	data := validSession(t)
	requireOK(t, atomicWrite(filepath.Join(dir, "session.json"), data))
	storage, err := sourceStorage(dir, c, &failure{}, true)
	requireOK(t, err)
	if _, err := storage.LoadSession(context.Background()); err != session.ErrNotFound {
		t.Fatal("explicit relogin loaded old authorization")
	}
	b, err := os.ReadFile(filepath.Join(dir, "session.json"))
	requireOK(t, err)
	if !bytes.Equal(b, data) {
		t.Fatal("saved session deleted before successful replacement")
	}
}

func TestSourceIntegrationLoginOptionsAndProxy(t *testing.T) {
	c, err := resolveSourceConfig("tdl", hostNamespace)
	requireOK(t, err)
	if validateLoginOptions(c, false, true, "qr") == nil {
		t.Fatal("implicit relogin accepted")
	}
	if validateLoginOptions(c, true, false, "invalid") == nil {
		t.Fatal("invalid method accepted")
	}
	requireOK(t, validateLoginOptions(c, true, true, "code"))
	if _, err := tdlResolver("socks5://127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	if _, err := tdlResolver("https://secret@proxy.invalid"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("proxy rejected incorrectly")
	}
}
