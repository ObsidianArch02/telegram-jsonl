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
