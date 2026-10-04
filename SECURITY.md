# Security

[English](SECURITY.md) | [简体中文](SECURITY.zh-CN.md)

## Reporting

Use GitHub's private vulnerability reporting for this repository when it is enabled.
If no private reporting channel is available, open an issue requesting a private
contact without including exploit details, personal information, or credentials.
There is no published support lifetime or guaranteed response time for this prototype.

Reports should include the affected commit or version, operating system, a
synthetic reproduction, expected behavior, actual behavior, and potential impact.
Never attach real sessions, login QR codes, archive metadata, conversations,
application secrets, or downloaded personal files.

## Local Data

Session files grant account access. Protect both the archive and fetch state,
and keep state out of version control,
public shares, and ordinary diagnostic uploads. Restrictive Unix permissions do
not encrypt data and are not a substitute for Windows ACLs or disk protection.
If a session leaks, revoke the corresponding device authorization in Telegram
and stop using that local session directory.

The downloader and archiver use different authorizations. Do not share a state
directory across processes or accounts. Downloaded files are untrusted content;
this program does not inspect them for malware.

## Scope and Limits

Deletion, protection handling, account binding, and download
path isolation are security-relevant behavior. Tests use simulations; real-account
behavior is not yet verified. Remote deletions cannot be observed offline and do
not erase external backups or completed downloads.

The integrated application identity is published upstream and is not an individual
account credential. Its availability can change independently of this project.
Review [operational limits](docs/limitations.md) and [Telegram terms](DISCLAIMER.md).
