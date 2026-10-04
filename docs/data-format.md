# Data Format

[English](data-format.md) | [简体中文](data-format.zh-CN.md)

[Back to README](../README.md)

## Files

Each component keeps its state in its own data directory:

```text
data-tdl/
  .lock                 Single-process session lock
  session.json          Account authorization; treat as a credential
  state.sqlite          Hot runtime state: client binding, update cursors,
                        synchronization jobs, receipts, tombstones, cooldown
  archive/
    index.sqlite        Cold properties: peer names/usernames/access hashes,
                        media index, downloaded-file ledger
    user-123.jsonl      Private conversation
    chat-456.jsonl      Basic group
    channel-789.jsonl   Supergroup or channel
```

Fetch keeps its own `session.json` and `state.sqlite` in `fetch-data/`, with
downloaded files in `fetch-data/attachments/` unless `--output` specifies another
directory. It records completed downloads in the shared archive's `files` table,
without changing messages, archive metadata, peers, or the media index.

Only login authorization stays in JSON outside the message files. Other runtime
and index data is stored in SQLite. The pure-Go SQLite driver preserves the
single-executable build without CGO or a separately installed SQLite library.
The optional `sqlite3` CLI is useful for inspecting the database; the program
does not require it.

Older `metadata.json`, `updates.json`, `cooldown.json`, and `client.json` files
are neither imported nor read. They are preserved rather than deleted. Archives
with old JSON metadata and no SQLite index require a fresh data directory;
see [upgrading](usage.md#upgrading-from-json-state).

Do not share state as a diagnostic attachment. Internal metadata includes access
information not intended for downstream consumers. Restrictive file permissions
are not encryption; use the operating system's access controls and disk protection.
`state.sqlite` is the hot runtime database. `index.sqlite` is the cold properties
database containing names, usernames, conversation access hashes, media and file
paths. They are intentionally separate: frequent cursor/job writes do not rewrite
stable peer properties. Neither is a public peer-name export; do not publish either
database indiscriminately.
Protect both databases and their `-wal`/`-shm` sidecar files. SQLite's backup API
can produce a consistent database snapshot. To back up JSONL and databases as a
complete archive, stop the archiver and downloader before copying their state.
Copying only a live `.sqlite` file can lose committed data still in its WAL.
Windows users must check the folder's NTFS permissions.
SQLite readers can create WAL/SHM coordination sidecars even when opened read-only.
Search performs no logical database writes and does not modify the main database
or JSONL, but this does not guarantee an entirely unchanged directory.

## SQLite Index

`archive/index.sqlite` contains these tables:

| Table | Purpose |
| --- | --- |
| `state` | Hot serialized state; `archive_metadata` stores account binding and peer IDs/history progress, while `sync_cycle`, `sync_job:*`, and `jsonl_intent:*` store resumable work and crash receipts. Names and access hashes are joined from `index.sqlite`. |
| `peers` | `peer`, `kind`, numeric `id`, display `name`, `username`, and `is_dialog`. |
| `media` | `peer`, `message_id`, `media_id`, `media_kind`, `file_name`, `mime_type`, and `size_bytes` for current attachment records. |
| `files` | Completed downloads: `peer`, `message_id`, `media_id`, `media_kind`, local `path`, `size_bytes`, `sha256`, and `downloaded_at`. |

The `files` primary key is `(peer, message_id, media_id, media_kind, path)`.
`media_kind` comes from the actual downloaded attachment's metadata; it separates
different attachment classes whose numeric IDs could coincide. `downloaded_at`
is a UTC RFC3339 timestamp with available fractional-second precision.

Display names and usernames are populated from discovered Telegram entities;
they may be unknown before discovery or absent on the service. UTF-8 names are
stored alongside stable IDs; a name or username is not a unique account identity.
The index does not duplicate message bodies and does not provide SQLite FTS.
Search continues to evaluate local JSONL messages with regular expressions.
The component's separate `state.sqlite` stores its client binding, update state,
cooldown, synchronization jobs and crash receipts in keyed `state` rows; login
authorization stays in `session.json`. SQLite schema version 2 is explicit; old
JSON state or schema version 1 is not auto-migrated.

If `sqlite3` is installed, list conversation names without changing records:

```sh
sqlite3 -readonly ./data-tdl/archive/index.sqlite 'SELECT peer, kind, id, name, username FROM peers ORDER BY peer;'
```

To inspect saved files and any current matching attachment metadata, open the
same database with `sqlite3 -readonly` and run:

```sql
SELECT f.peer, f.message_id, m.file_name, f.path, f.size_bytes, f.sha256
FROM files AS f
LEFT JOIN media AS m
  ON m.peer = f.peer AND m.message_id = f.message_id AND m.media_id = f.media_id
  AND m.media_kind = f.media_kind
WHERE f.peer = 'channel-789'
ORDER BY f.downloaded_at DESC;
```

Replace the illustrative peer ID with one from your database. A completed file
record remains after remote deletion or attachment replacement, so joined media
fields may be `NULL`. The ledger records a completed download; it does not prove
the local file still exists or has remained unchanged.

## JSONL Records

Each line is one ordinary cloud message, including received and sent messages.
Conversation files are sorted by message ID. New records use schema 2; reading
schema 1 is still supported.

```json
{"schema":2,"account_id":42,"peer":"user-123","message_id":100,"sender":"user-123","date":"2026-10-04T08:00:00Z","outgoing":false,"text":"Example message","reply_to":99}
```

Optional fields include `edited_at`, `media_type`, `message_url`, `album_id`, and
`media`. Timestamps use UTC. Attachment captions are stored in `text`. A photo
or voice message can have an empty `text` field. `media_type` retains the
Telegram protocol type, while `media.kind` identifies the useful subtype.

**JSONL is a current-state snapshot, not an append-only event log.** Changes
atomically replace a conversation file, retaining the current version of each
message. Consumers should reopen the path instead of retaining an old file
descriptor. Do not assume that tailing a file observes all changes.

## Non-Text Content

| Content | Stored metadata |
| --- | --- |
| Photos, documents, videos, voice, music, stickers, animation | Type, subtype, attachment ID, available name/MIME/size/dimensions/duration; audio title/artist and sticker text when available. |
| Albums | Separate member messages linked by `album_id`. |
| Polls | Question, options, known counts, total voters, current account selection, closed/multiple-choice/quiz flags, and available solution. No voter list. |
| Locations, live locations, venues | Coordinates, accuracy, live-location period/heading, venue name/address/provider when available. |
| Contacts | Phone number, name, available user ID, and at most 8 KiB of vCard. |
| Web previews | URL, title, description, site, and author when available. No web fetch or Instant View body. |
| Dice | Emoji and result. |
| Replies | Referenced message ID, without copying the quoted message body. |
| Self-destructing, protected, restricted, or paid media | Entire message skipped; an existing record is removed. |
| Secret chats and service messages | Not archived. |

The encoded `media` object is limited to 32 KiB. An oversized object is replaced
with `kind` and `omission_reason`. An oversized vCard alone is omitted and listed
in `omitted_fields`. Thumbnails, audio waveforms, binary attachments, full web
caches, and raw protocol messages are not stored. Edits and deletions apply to
the entire record, including structured metadata. Poll updates replace the
current result rather than adding an event history.

Stored metadata excludes expiring media `file_reference` values and media
authorization `access_hash` values. Conversation access hashes are retained only
in internal state for normal authorized requests. Downloaders re-fetch the
original message to obtain current file references.

## Message Links

Supergroup/channel `message_url` fields use the official
`https://t.me/c/<channel_id>/<message_id>` format. Album members use `?single`.
These are message entry points, not HTTP attachment download links. Access still
depends on membership, account permissions, and whether the message exists.
Private chats and basic groups have identifiers without fabricated HTTPS links.

References: [message links](https://core.telegram.org/api/links#message-links)
and [authorized file downloads](https://core.telegram.org/api/files#downloading-files).

## Sensitive Content

Records can contain other people's messages, phone numbers, vCards, and exact
locations. Account authorization does not grant unrestricted rights to this
content. Exports, backups, and downstream copies remain your responsibility;
see the [Telegram disclaimer](../DISCLAIMER.md).
