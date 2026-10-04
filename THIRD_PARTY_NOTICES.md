# Third-Party Notices

[English](THIRD_PARTY_NOTICES.md) | [简体中文](THIRD_PARTY_NOTICES.zh-CN.md)

## tdl

The integrated QR/code login flow and application configuration are derived
from [iyear/tdl](https://github.com/iyear/tdl), maintained by
iyear and its contributors, under the GNU Affero General Public License v3.0.
The source reference is version `v0.20.4`, commit
`9d7d49eef2bcb04da720c26e33598c49c68b9ddd`.

Relevant upstream files are `app/login/qr.go`, `app/login/code.go`,
and `pkg/tclient/app.go`.
See the [immutable source tree](https://github.com/iyear/tdl/tree/9d7d49eef2bcb04da720c26e33598c49c68b9ddd).

This project modifies the integration to compile into one Go executable, use
project-configured application credentials for QR/code login, store stable sessions
locally, isolate archive/fetch authorizations, and support the archiver.
These modifications are maintained here, not by the upstream tdl project. Derived source files carry
attribution comments; [LICENSE](LICENSE) provides the AGPL text.

## Go Dependencies

Dependency versions are recorded in [go.mod](go.mod) and [go.sum](go.sum).
Dependencies retain their own licenses and copyright notices, including
[gotd/td](https://github.com/gotd/td) for MTProto and Telegram API handling.
The release packaging includes dependency notices and license texts where
required; preserve them when redistributing binaries or source. The
[license inventory](licenses/inventory.json) records copied files and SHA-256
hashes for the modules linked across all six release targets.

## Redistribution

The combined project is distributed under [AGPL v3.0](LICENSE). Preserve
attribution, modification notices, and applicable dependency licenses.
Distributing binaries requires providing corresponding source under the license;
a release's tracked-source archive identifies this project's corresponding source.
Dependency modules remain identified by their pinned versions and upstream notices.
If you modify the program and offer it for remote network interaction, review
AGPL section 13's corresponding-source requirement.

Project licensing does not grant rights to Telegram service access, trademarks,
or user content; see [DISCLAIMER.md](DISCLAIMER.md).
