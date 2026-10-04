// Command licenses collects upstream notices for the modules linked into release binaries.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *module
}

type packageInfo struct {
	Module *module
}

type notice struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type dependency struct {
	Path    string   `json:"module"`
	Version string   `json:"version,omitempty"`
	Notices []notice `json:"notices"`
}

type inventory struct {
	Targets      []string     `json:"targets"`
	Dependencies []dependency `json:"dependencies"`
}

var targets = []string{
	"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64",
}

func main() {
	output := flag.String("output", "licenses", "directory for copied notices and inventory.json")
	flag.Parse()
	if err := collect(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func collect(output string) error {
	modules := make(map[string]module)
	for _, target := range targets {
		parts := strings.Split(target, "/")
		cmd := exec.Command("go", "list", "-deps", "-json", ".")
		cmd.Env = targetEnv(parts[0], parts[1])
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		result, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("list dependencies for %s: %w: %s", target, err, stderr.String())
		}
		decoder := json.NewDecoder(bytes.NewReader(result))
		for {
			var pkg packageInfo
			if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return fmt.Errorf("decode dependencies for %s: %w", target, err)
			}
			if pkg.Module == nil || pkg.Module.Main {
				continue
			}
			dep := *pkg.Module
			if dep.Replace != nil {
				dep = *dep.Replace
			}
			if dep.Dir == "" {
				return fmt.Errorf("missing source directory for %s@%s", dep.Path, dep.Version)
			}
			if dep.Version == "" || filepath.IsAbs(dep.Path) || strings.Contains(dep.Path, "..") {
				return fmt.Errorf("release dependency %s must identify a versioned upstream module", dep.Path)
			}
			modules[dep.Path+"@"+dep.Version] = dep
		}
	}

	goRoot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("locate Go distribution: %w", err)
	}
	result := inventory{Targets: targets}
	root := strings.TrimSpace(string(goRoot))
	goLicense := filepath.Join(root, "LICENSE")
	if _, err := os.Stat(goLicense); errors.Is(err, os.ErrNotExist) {
		// Homebrew installs the distribution license beside its libexec GOROOT.
		goLicense = filepath.Join(filepath.Dir(root), "LICENSE")
	}
	goNotice, err := copyNotice(goLicense, filepath.Join(output, "go", "LICENSE"), "go/LICENSE")
	if err != nil {
		return err
	}
	result.Dependencies = append(result.Dependencies, dependency{Path: "Go standard library and runtime", Notices: []notice{goNotice}})

	keys := make([]string, 0, len(modules))
	for key := range modules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		mod := modules[key]
		notices, err := moduleNotices(mod, output)
		if err != nil {
			return err
		}
		result.Dependencies = append(result.Dependencies, dependency{Path: mod.Path, Version: mod.Version, Notices: notices})
	}
	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "inventory.json"), append(content, '\n'), 0o644); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	fmt.Printf("Collected notices for Go and %d linked modules across %d targets.\n", len(modules), len(targets))
	return nil
}

func targetEnv(goos, goarch string) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GOOS=") && !strings.HasPrefix(value, "GOARCH=") && !strings.HasPrefix(value, "CGO_ENABLED=") {
			env = append(env, value)
		}
	}
	return append(env, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
}

func moduleNotices(mod module, output string) ([]notice, error) {
	entries, err := os.ReadDir(mod.Dir)
	if err != nil {
		return nil, fmt.Errorf("read source for %s: %w", mod.Path, err)
	}
	var result []notice
	licenseFound := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToUpper(entry.Name())
		isLicense := matchesNoticeName(name, "LICENSE") || matchesNoticeName(name, "LICENCE") || matchesNoticeName(name, "COPYING") || matchesNoticeName(name, "UNLICENSE")
		if !isLicense && !matchesNoticeName(name, "NOTICE") && !matchesNoticeName(name, "COPYRIGHT") {
			continue
		}
		licenseFound = licenseFound || isLicense
		relative := filepath.ToSlash(filepath.Join(mod.Path+"@"+mod.Version, entry.Name()))
		copied, err := copyNotice(filepath.Join(mod.Dir, entry.Name()), filepath.Join(output, filepath.FromSlash(relative)), relative)
		if err != nil {
			return nil, err
		}
		result = append(result, copied)
	}
	if !licenseFound {
		return nil, fmt.Errorf("no upstream root license found for %s@%s; review its source layout", mod.Path, mod.Version)
	}
	return result, nil
}

func matchesNoticeName(name, prefix string) bool {
	return name == prefix || strings.HasPrefix(name, prefix+".") || strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, prefix+"_")
}

func copyNotice(source, destination, relative string) (notice, error) {
	content, err := os.ReadFile(source)
	if err != nil {
		return notice{}, fmt.Errorf("read upstream notice: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return notice{}, err
	}
	if err := os.WriteFile(destination, content, 0o644); err != nil {
		return notice{}, err
	}
	hash := sha256.Sum256(content)
	return notice{File: relative, SHA256: hex.EncodeToString(hash[:])}, nil
}
