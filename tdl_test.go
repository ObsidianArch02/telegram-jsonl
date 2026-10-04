package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/session"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("JSONL_TEST_HOST_LOG"); path != "" {
		if strings.Contains(strings.Join(os.Args[1:], " "), " version") {
			if os.Getenv("JSONL_TEST_HOST_BAD_VERSION") != "" {
				fmt.Println("upstream v0.20.4")
			} else {
				fmt.Println(hostVersion)
			}
			os.Exit(0)
		}
		entry := struct {
			Args    []string `json:"args"`
			DataDir string   `json:"data_dir"`
			Tmp     string   `json:"tmp"`
		}{os.Args[1:], os.Getenv("TDL_DATA_DIR"), os.Getenv("TMPDIR")}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(2)
		}
		_ = json.NewEncoder(f).Encode(entry)
		_ = f.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func validTDLEnv(t *testing.T) tdlEnvironment {
	t.Helper()
	storage := &session.StorageMemory{}
	requireOK(t, (&session.Loader{Storage: storage}).Save(context.Background(), &session.Data{DC: 2, Addr: "127.0.0.1:443", AuthKey: make([]byte, 256), AuthKeyID: make([]byte, 8)}))
	b, err := storage.Bytes(nil)
	requireOK(t, err)
	return tdlEnvironment{Name: "telegram-jsonl", Namespace: hostNamespace, AppID: tdlBuiltinID, AppHash: strings.Repeat("a", 32), Session: b, DataDir: t.TempDir()}
}

func TestTDLEnvironmentValidatesHostIdentityAndSession(t *testing.T) {
	env := validTDLEnv(t)
	path := filepath.Join(t.TempDir(), "env.json")
	requireOK(t, writeJSON(path, env))
	loaded, err := loadTDLEnvironment(path)
	requireOK(t, err)
	if loaded.AppID != tdlBuiltinID || loaded.Namespace != hostNamespace {
		t.Fatal("host identity changed")
	}
	env.AppID = 2040
	requireOK(t, writeJSON(path, env))
	if _, err := loadTDLEnvironment(path); err == nil {
		t.Fatal("Desktop application identity accepted")
	}
	env.AppID = tdlBuiltinID
	env.Session = []byte("secret-corrupt-session")
	requireOK(t, writeJSON(path, env))
	if _, err := loadTDLEnvironment(path); err == nil || strings.Contains(err.Error(), "secret-corrupt-session") {
		t.Fatal("invalid session accepted or exposed")
	}
}

func TestTDLEnvironmentRejectsPublicFileAndTraversal(t *testing.T) {
	env := validTDLEnv(t)
	path := filepath.Join(t.TempDir(), "env.json")
	requireOK(t, writeJSON(path, env))
	requireOK(t, os.Chmod(path, 0644))
	if _, err := loadTDLEnvironment(path); err == nil {
		t.Fatal("public session file accepted")
	}
	requireOK(t, os.Chmod(path, 0600))
	env.Namespace = "../../other"
	requireOK(t, writeJSON(path, env))
	if _, err := loadTDLEnvironment(path); err == nil {
		t.Fatal("namespace traversal accepted")
	}
	env.Namespace = hostNamespace
	env.NTP = "external-server"
	requireOK(t, writeJSON(path, env))
	if _, err := loadTDLEnvironment(path); err == nil {
		t.Fatal("unsupported NTP silently ignored")
	}
}

func launchFixture(t *testing.T) (tdlLaunchOptions, string) {
	t.Helper()
	binary, err := os.Executable()
	requireOK(t, err)
	logPath := filepath.Join(t.TempDir(), "host-log.jsonl")
	t.Setenv("JSONL_TEST_HOST_LOG", logPath)
	return tdlLaunchOptions{Data: filepath.Join(t.TempDir(), "data, with spaces"), Binary: binary, LoginMethod: "qr", Login: true, Interval: 2 * time.Second, Batch: 50, SyncEvery: 6 * time.Hour}, logPath
}

func readHostCalls(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	requireOK(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var calls []string
	for _, line := range lines {
		var entry struct {
			Args    []string `json:"args"`
			DataDir string   `json:"data_dir"`
			Tmp     string   `json:"tmp"`
		}
		requireOK(t, json.Unmarshal([]byte(line), &entry))
		if !strings.HasSuffix(entry.DataDir, "tdl-runtime") || entry.Tmp != filepath.Join(entry.DataDir, "tmp") {
			t.Fatal("host escaped isolated directory")
		}
		calls = append(calls, strings.Join(entry.Args, " "))
	}
	return calls
}

func TestTDLLauncherLoginDispatchAndSessionReuse(t *testing.T) {
	o, path := launchFixture(t)
	o.Once = true
	o.History = historyConfig{Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	requireOK(t, launchTDL(context.Background(), o))
	calls := readHostCalls(t, path)
	if len(calls) != 3 || !strings.Contains(calls[0], "login -T qr") || !strings.Contains(calls[1], "extension install --force") || !strings.Contains(calls[2], "--client tdl") || !strings.Contains(calls[2], "--once") {
		t.Fatalf("invalid dispatch: %v", calls)
	}
	if !strings.Contains(calls[2], "--history-since 2026-09-01T00:00:00Z") {
		t.Fatal("TDL extension did not receive the resolved history boundary")
	}
	requireOK(t, launchTDL(context.Background(), o))
	calls = readHostCalls(t, path)
	if len(calls) != 5 {
		t.Fatal("saved session was logged in again")
	}
	o.Relogin = true
	requireOK(t, launchTDL(context.Background(), o))
	calls = readHostCalls(t, path)
	if len(calls) != 8 || !strings.Contains(calls[5], "login -T qr") {
		t.Fatal("explicit relogin missing")
	}
}

func TestTDLLauncherPreflightAndVersionMismatch(t *testing.T) {
	o, path := launchFixture(t)
	o.CheckOnly = true
	requireOK(t, launchTDL(context.Background(), o))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("preflight logged in or dispatched extension")
	}
	t.Setenv("JSONL_TEST_HOST_BAD_VERSION", "1")
	if err := launchTDL(context.Background(), o); err == nil {
		t.Fatal("unpatched host accepted")
	}
}

func TestTDLLauncherRequiresExplicitLogin(t *testing.T) {
	o, path := launchFixture(t)
	o.Login = false
	if err := launchTDL(context.Background(), o); err == nil {
		t.Fatal("login unexpectedly authorized")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("host performed login")
	}
}

func TestTDLStoragePathQuotesAndProxy(t *testing.T) {
	path := "/tmp/a,b with spaces"
	fields, err := csv.NewReader(strings.NewReader(storageArgument(path))).Read()
	requireOK(t, err)
	if len(fields) != 2 || fields[1] != "path="+path {
		t.Fatal("storage path was split at comma")
	}
	if _, err := tdlResolver("socks5://127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	if _, err := tdlResolver("https://secret@proxy.invalid"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("unsupported proxy accepted or leaked credentials")
	}
}

func TestTDLFetchUsesIndependentNamespaceAndChildCommand(t *testing.T) {
	o, path := launchFixture(t)
	o.Namespace = fetchNamespace
	o.ChildArgs = []string{"fetch", "--archive", "/some/read-only/archive", "--data", o.Data, "--pattern", "invoice"}
	requireOK(t, launchTDL(context.Background(), o))
	calls := readHostCalls(t, path)
	if len(calls) != 3 || !strings.Contains(calls[0], "-n local-fetch") || !strings.Contains(calls[2], " fetch --archive") || strings.Contains(calls[2], "--history-days") {
		t.Fatalf("fetch dispatched as archiver: %v", calls)
	}
	var marker struct {
		Namespace string `json:"namespace"`
	}
	requireOK(t, readJSON(filepath.Join(o.Data, "tdl-runtime", "login.json"), &marker))
	if marker.Namespace != fetchNamespace {
		t.Fatal("fetch reused archive namespace")
	}
}
