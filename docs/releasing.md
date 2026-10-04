# Releasing

[English](releasing.md) | [简体中文](releasing.zh-CN.md)

[Back to contributing](../CONTRIBUTING.md)

## Maintainer Workflow

Publishing starts when a version tag is pushed to GitHub. It does not require
a separately installed webhook server. The workflow tests the tagged source,
cross-compiles the standalone executable, packages artifacts, and creates the
GitHub release using its repository-scoped token.

Develop on `dev/<topic>` and commit each verified atomic change locally.
When preparing an upload, rebase unpublished commits onto the verified remote's
latest `main`, resolve conflicts, and run the affected checks. The following
preparation example assumes the upstream remote has been verified as `origin`
and the current branch is an unpublished development branch:

```fish
git fetch origin
git rebase origin/main
go test -race ./...
go vet ./...
git status --short
git log origin/main..HEAD --oneline
```

Present the rebased commits, exact changes, check results, intended destination,
and any application credentials in tracked source or history for maintainer
review. Wait for explicit upload approval before pushing. Rebase and local
commit authorization do not authorize publishing. Never force-push or rewrite
published history without separate authorization.

After approval, upload the reviewed source to the agreed destination. Create and
push a release tag only when a release and its tag have also been requested and
approved. Use a version not already released; this example assumes the current
HEAD is the reviewed release commit, and does not claim a release exists:

```fish
git tag v0.1.0
git push origin v0.1.0
```

Stable tags follow `vMAJOR.MINOR.PATCH`. Suffixes such as `v0.1.0-rc.1` produce
prereleases. The workflow validates the version format before publishing.
The project must first be hosted on GitHub, with Actions enabled and release
writes permitted by repository policy. Do not push tags from a working tree
containing uncommitted release changes; tags identify committed source.

## Artifacts

The release matrix covers Linux, macOS, and Windows on `amd64` and `arm64`.
Unix packages are `.tar.gz`; Windows packages are `.zip`. `SHA256SUMS` records
the package checksums. Packages include the executable, project documentation,
license, and upstream/dependency notices. A source archive is generated from the
tagged Git tree rather than the workspace, excluding ignored local account state.
Tracked application credentials are included in that source; `.gitignore` does
not redact tracked files or Git history.

Checksums detect accidental changes but are not independent publisher signatures.
Cross-compilation verifies builds; it does not establish real-account behavior
or run each binary on each target platform.

## Local Packaging

`scripts/release.sh` builds a selected target without publishing. In fish:

```fish
set -gx RELEASE_TAG v0.1.0
set -gx BUILD_COMMIT (git rev-parse HEAD)
set -gx GOOS linux
set -gx GOARCH amd64
bash scripts/release.sh build
```

The helper disables CGO and uses the selected version and commit metadata.
Run it from the repository root with the Go toolchain available. It requires
a clean, committed source tree matching `BUILD_COMMIT` and `HEAD`. The binary and
root documents come from the working tree, while `docs/` and `licenses/` come
from `HEAD`; local edits would produce a package with mismatched contents.

## Failed Runs

Inspect the failed job and fix the source or repository policy before publishing
again. Do not reuse an already published version for different source. Removing
or replacing public tags can break provenance; prefer a new version. Keep
Telegram credentials out of Actions secrets: builds and tests do not need them.

## Homebrew Formula

Stable-tag releases additionally attach a generated `telegram-jsonl.rb` formula.
It targets this repository's tap and uses the four macOS/Linux `.tar.gz` assets;
Windows remains available through its release packages. Prerelease tags do not
produce a stable formula. Generation checks the actual archives against their
SHA-256 values; it must not invent a version or checksum for an unreleased build.

For local generation, obtain the four actual release archives and `SHA256SUMS`
in `./dist`. Use the existing release tag; `v0.1.0` below is only an example:

```sh
go run ./scripts/homebrew \
  --repository ObsidianArch02/telegram-jsonl \
  --tag v0.1.0 \
  --checksums ./dist/SHA256SUMS \
  --assets ./dist \
  --output ./Formula/telegram-jsonl.rb
```

The formula's binary URLs refer to that tag's existing release assets and retain
a `HEAD` source-build fallback. To adopt the release-generated attachment,
retrieve it from the approved release and inspect it before replacing
`Formula/telegram-jsonl.rb` on `dev/<topic>`. Review repository URLs, supported
targets, archive names, and checksums, then commit the complete formula update.
In that same atomic adoption commit, update both README installation commands
to ordinary `brew install` by removing `--HEAD`.
Rebase unpublished changes onto the latest `main` and obtain approval for the
exact upload as described above. CI generates the attachment but does not commit
to the tap, update `main`, push a branch, or open a pull request automatically.

Before a stable formula is adopted, the bootstrap formula requires `--HEAD`.
See [Homebrew installation](homebrew.md) for the explicit same-repository tap URL
and development/stable commands. Installation requires the reviewed source and
formula on published `main`; publishing that state still requires approval.
