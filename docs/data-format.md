# Data Format

[English](data-format.md) | [简体中文](data-format.zh-CN.md)

[Back to README](../README.md)

## Files

Each component keeps its state in its own data directory:

```text
data-tdl/
  .lock                 Single-process session lock
  session.json          Account authorization; treat as a credential
  client.json           Client application and component binding, without app hash
  updates.json          Update cursors and channel access hashes
  cooldown.json         Persisted FLOOD_WAIT deadline
  archive/
    metadata.json       Account binding, peers, scan progress, deletion markers
    user-123.jsonl      Private conversation
    chat-456.jsonl      Basic group
    channel-789.jsonl   Supergroup or channel
```

Fetch keeps its authorization and cooldown in `fetch-data/`, with downloaded
files in `fetch-data/attachments/` unless `--output` specifies another directory.

Do not share state as a diagnostic attachment. Internal metadata includes access
information not intended for downstream consumers. Restrictive file permissions
are not encryption; use the operating system's access controls and disk protection.

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
