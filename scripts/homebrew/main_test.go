package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseFixture(t *testing.T) (options, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	opts := options{repository: "example/telegram-jsonl", tag: "v1.2.3", assets: dir, checksums: filepath.Join(dir, "SHA256SUMS"), output: filepath.Join(dir, "telegram-jsonl.rb")}
	hashes := make(map[string]string)
	var manifest strings.Builder
	for _, target := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		name := "telegram-jsonl_" + opts.tag + "_" + target + ".tar.gz"
		// Fixtures exercise checksum verification without executing an application.
		content := []byte("synthetic release bytes for " + target)
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		hashes[name] = hash
		fmt.Fprintf(&manifest, "%s  %s\n", hash, name)
	}
	if err := os.WriteFile(opts.checksums, []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return opts, hashes
}

func TestGenerateFourPlatformURLsAndVerifiedHashes(t *testing.T) {
	opts, hashes := releaseFixture(t)
	content, err := generate(opts)
	if err != nil {
		t.Fatal(err)
	}
	formula := string(content)
	for name, hash := range hashes {
		url := "https://github.com/example/telegram-jsonl/releases/download/v1.2.3/" + name
		if !strings.Contains(formula, "url \""+url+"\"\n      sha256 \""+hash+"\"") {
			t.Fatalf("formula omitted a verified platform asset: %s", name)
		}
	}
	for _, fragment := range []string{
		"class TelegramJsonl < Formula", `version "1.2.3"`, `license "AGPL-3.0-only"`,
		`url "https://github.com/example/telegram-jsonl.git", branch: "main"`,
		`depends_on "go" => :build`, `ENV["CGO_ENABLED"] = "0"`,
		`pkgshare.install "LICENSE", "NOTICE", "go.mod", "go.sum", "docs", "licenses"`,
		`pkgshare.install Dir["*.md"]`, `telegram-jsonl --version`, `telegram-jsonl --help`,
	} {
		if !strings.Contains(formula, fragment) {
			t.Fatalf("formula is missing %q", fragment)
		}
	}
	if strings.Contains(formula, "--login") || strings.Contains(formula, "TG_API") {
		t.Fatal("formula tests request credentials or account access")
	}
}

func TestGenerateRejectsInvalidRepositoryAndTag(t *testing.T) {
	for _, repository := range []string{"", "owner", "owner/repo/extra", "-owner/repo", "owner-/repo", "owner/repo#injection", "owner/repo\nother", "owner/../repo"} {
		t.Run("repository_"+repository, func(t *testing.T) {
			_, err := generate(options{repository: repository, tag: "v1.2.3"})
			if err == nil || !strings.Contains(err.Error(), "repository") {
				t.Fatal("invalid repository was accepted")
			}
		})
	}
	for _, tag := range []string{"", "1.2.3", "v1.2", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-rc.1", "v1.2.3+build", "v1.2.3\n", "v1.2.3\""} {
		t.Run("tag_"+tag, func(t *testing.T) {
			_, err := generate(options{repository: "owner/repo", tag: tag})
			if err == nil || !strings.Contains(err.Error(), "stable semantic version") {
				t.Fatal("invalid or prerelease tag was accepted")
			}
		})
	}
}

func TestChecksumManifestValidation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for name, manifest := range map[string]string{
		"empty":        "\n",
		"short_hash":   "abc archive.tar.gz\n",
		"nonhex":       strings.Repeat("g", 64) + " archive.tar.gz\n",
		"duplicate":    hash + " archive.tar.gz\n" + hash + " archive.tar.gz\n",
		"traversal":    hash + " ../archive.tar.gz\n",
		"windows_path": hash + " dir\\archive.tar.gz\n",
		"extra_field":  hash + " archive.tar.gz extra\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseChecksums(strings.NewReader(manifest)); err == nil {
				t.Fatal("invalid checksum manifest was accepted")
			}
		})
	}
	checksums, err := parseChecksums(strings.NewReader(strings.ToUpper(hash) + " *archive.tar.gz\n"))
	if err != nil || checksums["archive.tar.gz"] != hash {
		t.Fatal("standard binary-mode checksum entry was not accepted")
	}
}

func TestGenerateRejectsMissingOrCorruptedAssets(t *testing.T) {
	for _, failure := range []string{"missing_checksum", "missing_archive", "corrupt_archive"} {
		t.Run(failure, func(t *testing.T) {
			opts, _ := releaseFixture(t)
			name := "telegram-jsonl_v1.2.3_darwin_arm64.tar.gz"
			switch failure {
			case "missing_checksum":
				manifest, err := os.ReadFile(opts.checksums)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(string(manifest), "\n")
				if err := os.WriteFile(opts.checksums, []byte(strings.Join(lines[1:], "\n")), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing_archive":
				if err := os.Remove(filepath.Join(opts.assets, name)); err != nil {
					t.Fatal(err)
				}
			case "corrupt_archive":
				if err := os.WriteFile(filepath.Join(opts.assets, name), []byte("modified bytes"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := generate(opts); err == nil {
				t.Fatal("unverified release asset was accepted")
			}
		})
	}
}

func TestCLILeavesExistingFormulaUntouchedWhenVerificationFails(t *testing.T) {
	opts, _ := releaseFixture(t)
	prior := []byte("existing reviewed formula\n")
	if err := os.WriteFile(opts.output, prior, 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--repository", opts.repository, "--tag", opts.tag, "--checksums", opts.checksums, "--assets", opts.assets, "--output", opts.output}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(opts.output)
	if err != nil || !strings.Contains(string(generated), "class TelegramJsonl") {
		t.Fatal("CLI did not write a verified formula")
	}
	if err := os.WriteFile(filepath.Join(opts.assets, "telegram-jsonl_v1.2.3_linux_amd64.tar.gz"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(args); err == nil {
		t.Fatal("CLI accepted a corrupted archive")
	}
	unchanged, err := os.ReadFile(opts.output)
	if err != nil || string(unchanged) != string(generated) {
		t.Fatal("failed verification changed the existing formula")
	}
}
