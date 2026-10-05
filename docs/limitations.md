# Lifecycle and Operational Limits

[English](limitations.md) | [简体中文](limitations.zh-CN.md)

[Back to README](../README.md)

## Editing, Deletion, and Protection

- Deletion updates remove records from the active JSONL. Private/basic-group
  deletion uses account message IDs; channel deletion uses both peer and message ID.
- Deletion markers are saved before replacing conversation files. Restart recovery
  applies those markers so later history responses cannot restore deleted records.
- Edits replace old text and metadata. Live edits take priority over an older
  history response arriving during backfill.
- Messages with `ttl_period`, media `ttl_seconds`, protection/restriction flags,
  or paid media are skipped rather than stored with a deletion timer.
- Discovering protection clears the archived conversation and blocks further
  storage. Auto-delete enablement updates trigger the same conservative behavior.
  Removing remote protection does not automatically unblock the local conversation.
- Startup and periodic checks reconcile stored message IDs with the service,
  updating offline edits and removing deletions. Explicitly revoked conversation
  access clears the archive and blocks further storage for that conversation.

The program cannot observe remote changes while stopped, disconnected, or waiting
for Telegram's rate limit. Reconnection must recover updates or finish reconciliation.
Long gaps can exceed the service's update retention; intermediate states and short-lived
messages may be unrecoverable.

The archiver updates the SQLite media index together with its managed metadata;
completed-download entries in `files` are separate and survive remote deletion.
Replacing JSONL and updating SQLite are separate writes. If an index update
fails, the archiver stops; on restart it rebuilds media associations from the
current JSONL while applying its persisted deletion and exclusion state.
JSONL messages remain the message store. SQLite is not a second copy of message
bodies, and a ledger entry does not restore an unavailable message.

Synchronization cycles persist their start time, phase, peer position, per-page
history cursor, reconciliation cursor, and JSONL replacement receipts. A restart
resumes the recorded cycle and does not silently move its time boundary. The
default edit/deletion reconciliation window is one hour; use `--reconcile-window all`
when a full retained-message check is required. A shorter window can intentionally
leave older remote edits or deletions undiscovered.

The update manager's protocol cursors are persisted separately from the archive
cycle. Difference responses are handled through a durable boundary before the
upstream manager advances its cursor; a failed archive callback leaves the response
available for retry. This still cannot make JSONL, two SQLite files, and Telegram's
in-memory update queues one cross-file transaction, so the persisted cycle and
receipts are required for crash recovery.

Removal covers the active files managed by this program. External copies, backups,
filesystem snapshots, old open file descriptors, and disk remnants are outside
its control. Atomic replacement is not secure erasure.

## Downloads Are Explicit Copies

Fetch checks deletion, protection, expiry, and attachment identity before transfer
and again before committing the completed file. These checks cannot predict a
deletion occurring after the download. Saved files have no background deletion
sync and are not lifecycle mirrors of Telegram messages. Manage their retention
and backups separately.
Fetch records successful downloads in the shared SQLite `files` ledger while
leaving JSONL and archiver metadata unchanged. The ledger can contain paths to
files you later moved or removed; it does not monitor the filesystem.

## Account and Process Boundaries

One process may use a component's data directory at a time. Archive and fetch
need distinct sessions for the same account. The program stops on account mismatch,
session failure, or managed archive/session/cursor write failure. It does not
automatically rotate accounts, authorizations, or application identities.

### Concurrent Commands and Snapshots

`archive` owns the archive data directory lock and only one archive process may
use that directory at a time. Its JSONL files are replaced through a temporary
file, `fsync`, and atomic rename. The runtime and archive SQLite databases use
WAL, a ten-second busy timeout, and one connection per store; concurrent SQLite
transactions therefore wait or fail with a timeout instead of overwriting one
another.

`search` is an offline, read-only reader. It does not take the archive directory
lock. It reads JSONL and SQLite metadata separately, then rechecks metadata, so
it cannot provide one transactionally consistent snapshot across the JSONL file,
`state.sqlite`, and `archive/index.sqlite`. It may return a slightly older view
while `archive` is running, or report an account/metadata consistency error if a
replacement crosses its read boundary. It never writes archive state.

`fetch` locks its own fetch data directory, not the archive directory. It reads a
local archive snapshot before contacting Telegram and may run alongside `archive`.
It writes only completed-download rows to the shared `files` table in
`archive/index.sqlite`; it does not modify JSONL or archive synchronization
metadata. SQLite transactions coordinate that ledger write with the archiver.
Completed local files and their ledger rows remain after a remote message is
deleted; they are explicit copies, not lifecycle mirrors.

Stop both `archive` and `fetch` before making a complete backup of JSONL and the
SQLite databases. Preserve WAL and SHM sidecars, and use SQLite's backup API for
database snapshots.

### Resident Synchronization

`archive` is always a resident service. It runs one history and deletion
reconciliation cycle, keeps receiving live updates, and starts the next cycle
after `--sync-every` or an update-gap repair request. `--history-days` and
`--history-since` limit history pagination; they do not disable live updates or
turn the command into a one-shot snapshot. Stopping the service requires an
explicit signal or an unrecoverable synchronization error.

Archived channels that are no longer in the current dialog folders are checked
with a one-message `messages.getHistory` request using the stored peer access
hash. The check is lazy and does not enumerate channel participants or usernames.
An admin-only response is treated as inconclusive; it does not block the channel.

The program does not send chat messages, join groups, or mark messages as read.
Normal MTProto operation still changes connection and authorization state; a user
authorization is not a server-enforced read-only permission.

All non-message, non-login state resides in SQLite. Old JSON metadata/state is
not imported; use fresh data directories for this storage version. Preserve
prior data and protect databases and WAL/SHM sidecars as private account data.
Use SQLite's backup API for database snapshots; stop archive and fetch before
copying JSONL and databases as one complete archive backup.

## Coverage and Scale

Secret chats, service messages, stories, full reaction details, and voter lists
are unsupported. Deleted, inaccessible, or hidden pre-join history cannot be recovered.
Group migrations keep separate basic-group and supergroup files instead of merging
them into one logical conversation.

This prototype loads its archive index and messages into memory and rewrites the
affected conversation file on each change. It is intended for evaluating the workflow
and modest archives. Large or long-running archives need a storage design that
supports transactional updates and deletion, plus explicit cleanup of old snapshots.

History uses ordinary authorized requests, not a Takeout session. Consider
[Telegram Desktop export](https://telegram.org/blog/export-and-more) or a separate
[Takeout API](https://core.telegram.org/api/takeout) implementation for large one-time exports.

Local automated tests use simulated inputs. A separate unauthenticated live probe
confirmed that Telegram accepted the configured application credentials for
`auth.exportLoginToken`; it did not log in to an account or access messages.
End-to-end real-account login, archiving, and downloading with the SQLite workflow,
network reconnection, and server update recovery remain unverified.
Review [security guidance](../SECURITY.md) and the [disclaimer](../DISCLAIMER.md).
