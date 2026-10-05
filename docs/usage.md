# Usage

[English](usage.md) | [简体中文](usage.zh-CN.md)

[Back to README](../README.md)

Examples use `telegram-jsonl` installed on `PATH`, as with Homebrew. For a local
source build or extracted release, use `./telegram-jsonl` instead; Windows uses
`telegram-jsonl.exe` with your shell's executable invocation syntax.

## Login and Sessions

`archive` and `fetch` are separate components of the same executable. They use
separate account authorizations so they can run concurrently. Both must log in
to the same Telegram account. A data directory is bound to one account and
component; an account or session-namespace mismatch stops the command.

```sh
telegram-jsonl archive --data ./data-tdl --check-client
telegram-jsonl archive --data ./data-tdl --login
```

`--check-client` checks the integrated client locally without contacting Telegram.
For QR login, scan with **Telegram > Settings > Devices > Link Desktop Device**.
Verify the device authorization on your phone. QR codes contain short-lived login
tokens; do not share terminal recordings. `--login-method code` uses a phone
number and login code, with a two-factor password when required. These inputs
display one `*` per character, including pasted text. Backspace deletes the last
character, Ctrl-U clears the input, and Enter submits it.

Reuse the same data directory after login and omit `--login`. Existing authorized
sessions are reused even when `--login` is present. Only use
`--login --tdl-relogin` when you explicitly intend to replace the tdl authorization.
The application does not register new accounts or automatically retry failed logins.
Ctrl-C stops the process without logging the account out.

### Upgrading from JSON State

This storage version does not import earlier JSON metadata or runtime state.
Existing exports, sessions, downloads, and old state files are preserved, but old
JSON state is not read. An old archive without its new SQLite metadata cannot
be reopened by treating the JSONL files as a complete state database. Use fresh
directories and log in explicitly:

```sh
telegram-jsonl archive --data ./data-sqlite --login --history-days 7
telegram-jsonl fetch --archive ./data-sqlite/archive --data ./fetch-sqlite --login --pattern '(?i)\.pdf$' --limit 1
```

Do not replace or delete the earlier data directories to force an upgrade.
The [data format](data-format.md) describes the SQLite layout and query examples.
For a one-time conversion of an existing schema 1 directory, use the standalone
script at `/tmp/telegram-jsonl-migration-20261004/migrate.py`. Stop archive and
fetch first, run its `--dry-run`, then convert into fresh destination directories;
the source directories remain unchanged. Its `usage.txt` documents lock checks,
WAL-aware snapshots, session preservation, and validation limits.

## Runtime Logs

Operational logs use the standard library's structured text logger and go to
stderr with timestamps in the computer's local timezone. A signal produces an
explicit clean-stop message; an unexpected client return is reported as an
error instead of as a completed synchronization.
Set `TZ=Asia/Shanghai` in the process environment to select a different timezone.
History boundaries, next synchronization times, and FLOOD_WAIT deadlines in logs
use that timezone too. JSONL dates and JSON output on stdout remain in UTC.

Logs report authorization, dialog discovery, history pages, reconciliation,
successful JSONL changes, and attachment results. The archive reports stored
peer/message counts every minute. Download transfers report byte progress at
most once every five seconds. Logs omit message text, captions, passwords,
login tokens, and application credentials; peer/message identifiers are still
personal metadata. Reasons for skipped or failed downloads are in stdout JSON.

## Configuration

Without credential environment variables, archive's default `--client auto`
selects tdl. If either `TG_API_ID` or `TG_API_HASH` is present, it selects native
mode and requires both valid values. Fetch defaults to `--client tdl`; use
`--client native` explicitly for native downloads. The tdl mode uses a source-level port of its login integration and its
project-configured application identity. It does not execute a helper process. Telegram
may still reject the application identity; no account or API access is guaranteed.

For your own application, obtain credentials at [my.telegram.org](https://my.telegram.org).
The following environment commands are for **fish**; use your shell's equivalent
environment export if you use another shell:

```fish
set -gx TG_API_ID 'YOUR_APP_ID'
set -gx TG_API_HASH 'YOUR_APP_HASH'
telegram-jsonl archive --client native --data ./data-native --login
```

Keep contributor-specific credentials in private environment configuration,
not source files, issues, terminal recordings, or commits. Changes to the
project's default application identity require explicit maintainer authorization;
review its inclusion in source and Git history before any public upload.

| Archive flag | Default | Meaning |
| --- | --- | --- |
| `--data` | `./data-tdl` | Account and session state, plus the `archive/` directory. |
| `--client` | `auto` | Choose from environment, or explicitly select `tdl`/`native`. |
| `--login` | `false` | Permit interactive login if authorization is missing. |
| `--login-method` | `qr` | Login method: `qr` or `code`. |
| `--tdl-relogin` | `false` | Explicitly replace tdl authorization with `--login`. |
| `--check-client` | `false` | Check the local integrated client without connecting. |
| `--proxy` | Empty | SOCKS5 proxy URL for either backend. |
| `--interval` | `2s` | Minimum start interval for business RPCs; minimum `1s`. |
| `--batch` | `50` | Messages or dialogs per request; range `1` to `100`. |
| `--sync-every` | `6h` | Periodic history and deletion checks; minimum `10m`. |
| `--reconcile-window` | `1h` | Edit/deletion checks look back one hour; duration or `all`. |
| `--history-days` | `30` | Backfill window; `0` disables it, `-1` requests all history. |
| `--history-since` | Empty | Start date in UTC or an RFC3339 timestamp. |

Run `telegram-jsonl archive --help`, `search --help`, or `fetch --help` for
the authoritative options accepted by your build. Running without a subcommand
defaults to `archive`.

### History Windows

```sh
telegram-jsonl archive --data ./data-tdl --history-days 7
telegram-jsonl archive --data ./data-tdl --history-days 0
telegram-jsonl archive --data ./data-tdl --history-since 2026-09-01
```

The window is fixed at process startup and includes messages exactly at the
start time. `YYYY-MM-DD` means midnight UTC; use RFC3339 for an explicit timezone.
Do not explicitly combine `--history-days` with `--history-since`.
Pagination stops after reaching earlier messages; the boundary request can still
return an older page, which is not archived. There is no total message-count cap
inside the window. Expanding the window resets earlier history scan progress;
shrinking it does not delete existing records.

`archive` is a resident service. The history window limits history pagination
only; it does not disable live updates, update-gap recovery, or periodic
edit/deletion checks. There is no one-shot archive mode.

This setting limits history pagination, not retention. Live updates and update
gap recovery continue, and can include older messages. Existing records are
still checked for edits and deletions. `FLOOD_WAIT` deadlines are persisted;
restarting does not skip the required wait.

`--reconcile-window` limits the existing-message edit/deletion pass. Its default
is one hour before the synchronization cycle starts. The cycle stores its start
time and peer position in runtime SQLite, so a restart resumes the same window
instead of silently moving the boundary. Use `--reconcile-window all` to check
all retained messages. A shorter window can miss an older remote edit or deletion
until a wider window is used.

## Search and Download a PDF

Suppose `archive` is running with `--data ./data-tdl` and you want a received
`invoice.pdf`. Open another terminal in the executable's directory.

### 1. Preview Local Matches

```sh
telegram-jsonl search --archive ./data-tdl/archive --pattern '(?i)\.pdf$' --limit 5
```

This command is offline and does not download anything. `(?i)` ignores case and
`\.pdf$` matches filenames ending in `.pdf`. Results are one JSON object per line;
the example below is expanded and shortened for readability:

```json
{
  "peer": "user-7",
  "message_id": 456,
  "text": "This month's invoice",
  "media": {
    "kind": "document",
    "file": {"id": "9001", "name": "invoice.pdf", "mime_type": "application/pdf", "size_bytes": 18024}
  }
}
```

`peer` identifies the conversation. `media.file.name` is the received filename.
These fields describe the attachment; they do not mean it has been downloaded.

### 2. Download the Selected Match

Replace `user-7` with the actual peer from the preview:

```sh
telegram-jsonl fetch \
  --archive ./data-tdl/archive \
  --data ./fetch-data \
  --peer user-7 \
  --pattern '^invoice\.pdf$' \
  --limit 1 \
  --output ./attachments \
  --login
```

| Option | Effect in this example |
| --- | --- |
| `--archive` | Read matches from the archiver's JSONL; do not modify it. |
| `--data ./fetch-data` | Store the downloader's independent session. |
| `--peer user-7` | Restrict the search to the selected conversation. |
| `--pattern` | Match the filename; the same regex also checks other fields. |
| `--limit 1` | Process only the newest matching message. |
| `--output` | Save attachment bytes in `./attachments`. |
| `--login` | Allow the downloader's first login to the same account. |

The downloader needs its own first authorization even if the archiver is already
logged in. Reuse `./fetch-data` and omit `--login` on subsequent runs.
The data and output directories must be separate from the archiver's state and
JSONL directories. Their separation is checked, including resolved symlinks.

### 3. Read the Result

```json
{"peer":"user-7","message_id":456,"status":"downloaded","path":"/your/project/attachments/user-7-456-document-9001.pdf","size_bytes":18024,"sha256":"..."}
```

`path` is the local absolute filename; `/your/project` is illustrative.
Files use conversation, message, and attachment IDs plus the media type rather than sender-provided
paths. Successful results include bytes and SHA-256. `skipped` contains a reason
such as deletion, protection, replacement, or exceeding the size limit.
`error` indicates a transfer or write failure. The command exits after processing
its matches. It does not advance archive cursors or change JSONL. A `downloaded`
result means the file is saved and recorded in `archive/index.sqlite`'s `files` table. Fetch writes only
that download ledger in the shared index; the archiver owns its other metadata.
It also writes its own client binding and cooldown to `fetch-data/state.sqlite`.

If the file was saved but the SQLite ledger write fails, the result is `error`
with the saved `path`, size, SHA-256, and a reason identifying the ledger failure.
The file is retained, but it has no confirmed ledger entry. Check that path and
resolve the database error before deciding to download again.

Downloading the same unchanged attachment again replaces the same local path.
For several files, preview the intended conditions first, then increase `--limit`.
Once saved, files are explicit local copies; no process monitors their later
remote deletion. See [lifecycle limits](limitations.md).

## Search and Fetch Options

Regexes use Go/RE2 syntax. They match text, message and conversation IDs, media
types, filenames, MIME types, audio titles/artists, poll questions/options, and
structured metadata JSON. An ID regex such as `^456$` can also match another
field with that value; narrow by `--peer` and preview the result.

`--limit` is `1` to `1000`, with newest messages first. Local search can run
alongside the archiver without taking its writer lock. Multi-conversation results
are not a single transaction snapshot; fetch validates messages again online.
Search reads SQLite metadata and JSONL without changing records or main database
contents. SQLite can still create WAL/SHM coordination sidecars for a read-only
connection. Fetch reads the same message data but requires permission to update
the shared SQLite download ledger.

Fetch defaults to `--data ./fetch-data`, output at `fetch-data/attachments`,
`--interval 2s` (minimum `1s`), and `--max-file-bytes 268435456` (256 MiB per file).
The size limit accepts `1` byte through `10` GiB. Fetch downloads sequentially and
persists its own `FLOOD_WAIT` cooldown. It supports photos and document-type
attachments, including voice, video, music, stickers, and animation; it does not
follow third-party URLs in web previews.

Fetch re-reads the original message and protection state before transfer, obtains
a fresh file reference through Telegram's authorized interfaces, and checks the
message again before committing the completed file. An expired reference can be
refreshed once. Interrupted failures clean partial files when possible; a forced
kill can leave `.fetch-*` files in the output directory.
