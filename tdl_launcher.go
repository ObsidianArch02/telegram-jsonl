package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const hostVersion = "v0.20.4-jsonl-host.1"
const hostNamespace = "local-jsonl"

type tdlLaunchOptions struct {
	Data        string
	Binary      string
	Login       bool
	Relogin     bool
	LoginMethod string
	CheckOnly   bool
	Proxy       string
	Once        bool
	Interval    time.Duration
	Batch       int
	SyncEvery   time.Duration
	History     historyConfig
	Namespace   string
	ChildArgs   []string
}

func replaceEnvironment(original []string, values map[string]string) []string {
	result := make([]string, 0, len(original)+len(values))
	for _, entry := range original {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := values[key]; !replace && key != "TDL_EXTENSION" {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func storageArgument(path string) string {
	var b bytes.Buffer
	writer := csv.NewWriter(&b)
	_ = writer.Write([]string{"type=bolt", "path=" + path})
	writer.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

func launchTDL(ctx context.Context, o tdlLaunchOptions) error {
	if o.LoginMethod != "qr" && o.LoginMethod != "code" {
		return errors.New("TDL login method must be qr or code")
	}
	if o.Relogin && !o.Login {
		return errors.New("--tdl-relogin requires --login")
	}
	if _, err := tdlResolver(o.Proxy); err != nil {
		return err
	}
	root, err := filepath.Abs(o.Data)
	if err != nil {
		return err
	}
	if err := privateDir(root); err != nil {
		return err
	}
	lock, err := lockFile(root, ".tdl-launch.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	runtimeDir := filepath.Join(root, "tdl-runtime")
	namespace := o.Namespace
	if namespace == "" {
		namespace = hostNamespace
	}
	if !safeNamespace.MatchString(namespace) {
		return errors.New("invalid TDL namespace")
	}
	for _, dir := range []string{runtimeDir, filepath.Join(runtimeDir, "tmp"), filepath.Join(runtimeDir, "storage")} {
		if err := privateDir(dir); err != nil {
			return err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	binary := o.Binary
	if binary == "" {
		binary = filepath.Join(filepath.Dir(self), "tools", "tdl")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("TDL host not found; build integrations/tdl host or set --tdl-bin")
	}
	environment := replaceEnvironment(os.Environ(), map[string]string{"TDL_DATA_DIR": runtimeDir, "TMPDIR": filepath.Join(runtimeDir, "tmp")})
	global := []string{"--storage", storageArgument(filepath.Join(runtimeDir, "storage")), "-n", namespace}
	if o.Proxy != "" {
		global = append(global, "--proxy", o.Proxy)
	}
	version := exec.CommandContext(ctx, binary, append(append([]string{}, global...), "version")...)
	version.Env = environment
	output, err := version.CombinedOutput()
	if err != nil || !strings.Contains(string(output), hostVersion) {
		return errors.New("TDL host version mismatch; use the bundled v0.20.4-jsonl-host.1 build")
	}
	if o.CheckOnly {
		fmt.Println("TDL host verified: " + hostVersion)
		return nil
	}
	run := func(args ...string) error {
		command := exec.CommandContext(ctx, binary, append(append([]string{}, global...), args...)...)
		command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
		command.WaitDelay = 10 * time.Second
		command.Env = environment
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		if len(o.ChildArgs) > 0 && (args[0] == "login" || args[0] == "extension") {
			command.Stdout = os.Stderr
		}
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("TDL %s failed: %w", args[0], err)
		}
		return nil
	}
	marker := filepath.Join(runtimeDir, "login.json")
	var saved struct {
		Namespace string `json:"namespace"`
	}
	readErr := readJSON(marker, &saved)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	loggedIn := readErr == nil && saved.Namespace == namespace
	if !loggedIn || o.Relogin {
		if !o.Login {
			return errors.New("TDL session not initialized; run with --client tdl --login")
		}
		if err := run("login", "-T", o.LoginMethod); err != nil {
			return err
		}
		saved.Namespace = namespace
		if err := writeJSON(marker, saved); err != nil {
			return err
		}
	} else if o.Login {
		log.Print("Existing TDL session retained; --tdl-relogin is required to replace it")
	}
	// Register only this local executable, using the host's documented extension mechanism.
	if err := run("extension", "install", "--force", self); err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(self), filepath.Ext(self))
	name = strings.TrimPrefix(name, "tdl-")
	if len(o.ChildArgs) > 0 {
		return run(append([]string{name}, o.ChildArgs...)...)
	}
	args := []string{name, "archive", "--client", "tdl", "--data", root, "--interval", o.Interval.String(), "--batch", strconv.Itoa(o.Batch), "--sync-every", o.SyncEvery.String()}
	args = append(args, o.History.args()...)
	if o.Once {
		args = append(args, "--once")
	}
	return run(args...)
}
