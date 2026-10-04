# Releasing

[English](releasing.md) | [简体中文](releasing.zh-CN.md)

[Back to contributing](../CONTRIBUTING.md)

## Maintainer Workflow

Publishing starts when a version tag is pushed to GitHub. It does not require
a separately installed webhook server. The workflow tests the tagged source,
cross-compiles the standalone executable, packages artifacts, and creates the
GitHub release using its repository-scoped token.

Review and commit the intended source first. Use a version not already released;
the following version is an example, not a claim that a release exists:

```sh
go test -race ./...
go vet ./...
git status --short
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
an accurate source tree and includes project notices in the package.

## Failed Runs

Inspect the failed job and fix the source or repository policy before publishing
again. Do not reuse an already published version for different source. Removing
or replacing public tags can break provenance; prefer a new version. Keep
Telegram credentials out of Actions secrets: builds and tests do not need them.
