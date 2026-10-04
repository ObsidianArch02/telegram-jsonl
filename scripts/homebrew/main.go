// Command homebrew generates a stable formula from verified release archives.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

//go:embed formula.rb.tmpl
var formulaTemplate string

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	stableTagPattern  = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	assetNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type formulaAsset struct {
	URL    string
	SHA256 string
}

type formulaData struct {
	Homepage    string
	Version     string
	DarwinARM   formulaAsset
	DarwinIntel formulaAsset
	LinuxARM    formulaAsset
	LinuxIntel  formulaAsset
}

type options struct {
	repository string
	tag        string
	checksums  string
	assets     string
	output     string
}

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var opts options
	fs := flag.NewFlagSet("homebrew-formula", flag.ContinueOnError)
	fs.StringVar(&opts.repository, "repository", "", "GitHub repository in OWNER/REPO form")
	fs.StringVar(&opts.tag, "tag", "", "stable release tag such as v0.1.0")
	fs.StringVar(&opts.checksums, "checksums", "dist/SHA256SUMS", "release checksum manifest")
	fs.StringVar(&opts.assets, "assets", "dist", "directory containing release archives")
	fs.StringVar(&opts.output, "output", "dist/telegram-jsonl.rb", "generated stable Homebrew formula")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	content, err := generate(opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.output), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(opts.output), ".formula-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), opts.output)
}

func generate(opts options) ([]byte, error) {
	if !repositoryPattern.MatchString(opts.repository) {
		return nil, errors.New("repository must be a valid GitHub OWNER/REPO name")
	}
	if len(opts.tag) > 128 || !stableTagPattern.MatchString(opts.tag) {
		return nil, errors.New("tag must be a stable semantic version such as v0.1.0; prereleases are not supported")
	}
	manifest, err := os.Open(opts.checksums)
	if err != nil {
		return nil, err
	}
	defer manifest.Close()
	checksums, err := parseChecksums(manifest)
	if err != nil {
		return nil, err
	}
	homepage := "https://github.com/" + opts.repository
	data := formulaData{Homepage: homepage, Version: strings.TrimPrefix(opts.tag, "v")}
	for _, target := range []struct {
		os, arch string
		asset    *formulaAsset
	}{
		{"darwin", "arm64", &data.DarwinARM},
		{"darwin", "amd64", &data.DarwinIntel},
		{"linux", "arm64", &data.LinuxARM},
		{"linux", "amd64", &data.LinuxIntel},
	} {
		name := "telegram-jsonl_" + opts.tag + "_" + target.os + "_" + target.arch + ".tar.gz"
		expected, ok := checksums[name]
		if !ok {
			return nil, fmt.Errorf("checksum manifest is missing %s", name)
		}
		if err := verifyArchive(filepath.Join(opts.assets, name), expected); err != nil {
			return nil, fmt.Errorf("verify %s: %w", name, err)
		}
		*target.asset = formulaAsset{URL: homepage + "/releases/download/" + opts.tag + "/" + name, SHA256: expected}
	}
	tmpl, err := template.New("formula").Parse(formulaTemplate)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func parseChecksums(r io.Reader) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for line := 1; scanner.Scan(); line++ {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid checksum manifest at line %d", line)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("invalid SHA256 at line %d", line)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if !assetNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid archive filename at line %d", line)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate checksum for %s", name)
		}
		result[name] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("checksum manifest is empty")
	}
	return result, nil
}

func verifyArchive(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("release archive must be a regular file, not a symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("archive does not match its SHA256 checksum")
	}
	return nil
}
