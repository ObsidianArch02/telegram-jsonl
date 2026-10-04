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
	"go.etcd.io/bbolt"
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

func legacyFixture(t *testing.T, namespace, app string, data []byte) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "tdl-runtime", "storage", namespace)
	requireOK(t, privateDir(filepath.Dir(path)))
	db, err := bbolt.Open(path, 0600, nil)
	requireOK(t, err)
	requireOK(t, db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(namespace))
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte("app"), []byte(app)); err != nil {
			return err
		}
		return bucket.Put([]byte("session"), data)
	}))
	requireOK(t, db.Close())
	return root, path
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

func TestSourceIntegrationMigratesLegacySessionWithoutModifyingDatabase(t *testing.T) {
	data := validSession(t)
	root, path := legacyFixture(t, hostNamespace, "builtin", data)
	before, err := os.ReadFile(path)
	requireOK(t, err)
	migrated, err := migrateLegacySession(root, hostNamespace)
	requireOK(t, err)
	if !migrated {
		t.Fatal("legacy session not migrated")
	}
	after, err := os.ReadFile(path)
	requireOK(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("legacy database modified")
	}
	newSession, err := os.ReadFile(filepath.Join(root, "session.json"))
	requireOK(t, err)
	if !bytes.Equal(data, newSession) {
		t.Fatal("authorization changed during migration")
	}
	migrated, err = migrateLegacySession(root, hostNamespace)
	requireOK(t, err)
	if migrated {
		t.Fatal("migration overwrote current session")
	}
}

func TestSourceIntegrationRejectsWrongOrCorruptLegacySession(t *testing.T) {
	for _, app := range []string{"desktop", "builtin"} {
		t.Run(app, func(t *testing.T) {
			data := validSession(t)
			if app == "builtin" {
				data = []byte("corrupt-secret-session")
			}
			root, _ := legacyFixture(t, hostNamespace, app, data)
			if _, err := migrateLegacySession(root, hostNamespace); err == nil || strings.Contains(err.Error(), "corrupt-secret-session") {
				t.Fatal("invalid legacy session accepted or exposed")
			}
			if _, err := os.Stat(filepath.Join(root, "session.json")); !os.IsNotExist(err) {
				t.Fatal("invalid authorization committed")
			}
		})
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
