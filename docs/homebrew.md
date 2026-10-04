# Homebrew Installation

[English](homebrew.md) | [简体中文](homebrew.zh-CN.md)

[Back to README](../README.md)

## Availability

The project and its Homebrew tap share
[ObsidianArch02/telegram-jsonl](https://github.com/ObsidianArch02/telegram-jsonl).
The commands below require reviewed source and `Formula/telegram-jsonl.rb` on
the repository's published `main` branch. Creating a repository alone does not
make the tool installable. The bootstrap instructions use HEAD source builds;
stable installation additionally requires an adopted formula and its release assets.

## Development Installation

Install [Homebrew](https://brew.sh/) first. On macOS or Linux, run:

```sh
brew tap obsidianarch02/telegram-jsonl https://github.com/ObsidianArch02/telegram-jsonl.git
brew install --HEAD obsidianarch02/telegram-jsonl/telegram-jsonl
telegram-jsonl --version
```

The explicit tap URL is required because this repository is named `telegram-jsonl`,
not `homebrew-telegram-jsonl`. Its formula lives in `Formula/telegram-jsonl.rb`.
Until a stable formula is adopted, the bootstrap formula has only a `HEAD` source
definition, so `--HEAD` is required.
It builds the repository's `main` branch rather than a fixed release.

The formula supports macOS and Linux on Intel/AMD (`amd64`) and ARM (`arm64`).
Homebrew installs its Go build dependency; the source requires the Go version
specified by [go.mod](../go.mod), currently 1.25 or newer. The installed result
is one standalone executable and does not need Go, a separate tdl executable,
TDLib, or a SQLite installation at runtime.

To update a development installation after reviewed source changes are published:

```sh
brew upgrade --fetch-HEAD obsidianarch02/telegram-jsonl/telegram-jsonl
```

`--fetch-HEAD` asks Homebrew to check upstream development source rather than
relying only on its local HEAD revision. Updates do not import older JSON state;
review the [storage upgrade instructions](usage.md#upgrading-from-json-state).
Keep account data in your own private directories, separate from Homebrew's Cellar.

## Stable Installation

Stable releases can provide a generated formula with verified SHA-256 values
for the four macOS/Linux binary packages. After the maintainer reviews that
formula, commits it, and approves its upload to this same tap, ordinary
installation and upgrades become available:

```sh
brew update
brew install obsidianarch02/telegram-jsonl/telegram-jsonl
brew upgrade obsidianarch02/telegram-jsonl/telegram-jsonl
```

Stable installations use the precompiled executable and do not require Go.
To switch an existing HEAD installation to stable, uninstall the formula with
`brew uninstall obsidianarch02/telegram-jsonl/telegram-jsonl`, then run the stable
install command above. Keep your account data outside the package directory.
Release automation attaches the generated formula to the release; it does not
automatically replace the tap's formula or push to `main`. Generation and review
instructions are in [releasing](releasing.md#homebrew-formula).

## Source or Windows

For a source checkout, build from the repository root using the Go version in
`go.mod`:

```sh
go build -trimpath -o telegram-jsonl .
./telegram-jsonl --version
```

After source publication, you can obtain that checkout with:

```sh
git clone https://github.com/ObsidianArch02/telegram-jsonl.git
cd telegram-jsonl
```

Run a local build as `./telegram-jsonl`. The detailed [usage examples](usage.md)
use `telegram-jsonl` from `PATH`; substitute `./telegram-jsonl` for a local build.

Homebrew installation here does not cover Windows. When a tagged release exists,
download the matching Windows archive from
[Releases](https://github.com/ObsidianArch02/telegram-jsonl/releases), verify its
checksum against `SHA256SUMS`, and run `telegram-jsonl.exe`. Alternatively, build
from source with `go build -trimpath -o telegram-jsonl.exe .` and use your shell's
executable invocation syntax. Release packages cover Windows `amd64` and `arm64`.
