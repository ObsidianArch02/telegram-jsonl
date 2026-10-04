# telegram-jsonl

[English](README.md) | [简体中文](README.zh-CN.md)

A single-account Telegram CLI for local JSONL archives and on-demand attachment downloads.

AI assisted in generating this project; review the code before use.

[Usage](docs/usage.md) · [Data Format](docs/data-format.md) · [License](LICENSE)

## Install

On macOS or Linux, once the reviewed source and formula have been published:

```sh
brew tap obsidianarch02/telegram-jsonl https://github.com/ObsidianArch02/telegram-jsonl.git
brew install --HEAD obsidianarch02/telegram-jsonl/telegram-jsonl
```

The bootstrap formula builds one standalone executable from published `main`;
use `--HEAD` until a stable formula is adopted. No separate tdl binary or TDLib is required.
See [Homebrew, source, and Windows installation](docs/homebrew.md) for details.

## Quick Start

Run the archive command in one terminal. Scan the QR code with your existing
Telegram account under **Settings > Devices > Link Desktop Device**.
Login supports **QR code** (default, `--login-method qr`) and **phone/code**
(`--login-method code`). To use a phone number and Telegram login code instead:

```sh
telegram-jsonl archive --data ./data-tdl --login --login-method code --history-days 7
```

Both modes request your two-factor password when required. `fetch` accepts the
same login options.

```sh
# Archive recent messages and keep receiving updates.
telegram-jsonl archive --data ./data-tdl --login --history-days 7

# In another terminal, search the local archive without logging in.
telegram-jsonl search --archive ./data-tdl/archive --pattern '(?i)\.pdf$' --limit 5

# Download matching PDFs using a separate session for the same account.
telegram-jsonl fetch --archive ./data-tdl/archive --data ./fetch-data --login --pattern '(?i)\.pdf$' --limit 1
```

After the first login, reuse each command's data directory and omit `--login`.
On Windows, use `telegram-jsonl.exe` with the command syntax appropriate to your shell.

## Commands

| Command | Purpose |
| --- | --- |
| `archive` | Receive updates, backfill recent history, and maintain JSONL snapshots. |
| `search` | Search local text, filenames, and structured metadata with Go regular expressions. |
| `fetch` | Download selected attachments, then exit without modifying JSONL. |

Messages stay in JSONL. SQLite stores conversation names, attachment indexes,
download records, and runtime state; see the [data layout](docs/data-format.md).

The default history window is 30 days. `--history-days 0` disables backfill;
`--history-days -1` explicitly requests all accessible history. Existing records
are not removed when the window shrinks. Editing and deletion updates still apply.

Without credential environment variables, the client integrates modified [tdl](https://github.com/iyear/tdl) login
code at source level and uses the project's configured application identity. End users do
not need to apply for API credentials. Telegram still requires an application
identity and can reject it. Archive's `--client auto` selects native mode when
credential environment variables are present. You may explicitly use your credentials with
`--client native`; see [configuration](docs/usage.md#configuration).

## Boundaries

The archiver stores small media metadata, not attachment bytes. It skips
self-destructing, protected, restricted, and paid-media messages. Secret chats
and service messages are not archived.

JSONL files reflect the current archived state rather than an immutable event log.
Deletion cannot be synchronized while the program is offline. Files saved by
`fetch` do not follow future remote deletions. Sessions are account credentials;
protect both the archive and download directories.

This is an independent project, not affiliated with Telegram or endorsed by tdl.
Using official interfaces and rate limits does not guarantee account safety or
legal compliance. Review the [Telegram disclaimer](DISCLAIMER.md) before use.

## Documentation

- [Homebrew, source builds, and Windows installation](docs/homebrew.md)
- [Usage, login, and a complete PDF download example](docs/usage.md)
- [JSONL format and non-text message coverage](docs/data-format.md)
- [Lifecycle behavior and operational limits](docs/limitations.md)
- [Security reporting](SECURITY.md)
- [Third-party attribution](THIRD_PARTY_NOTICES.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development checks and contribution rules.
Real-account integration has not yet been verified; local tests use simulated inputs.

## License

[GNU Affero General Public License v3.0](LICENSE). Attribution and upstream
modifications are documented in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
