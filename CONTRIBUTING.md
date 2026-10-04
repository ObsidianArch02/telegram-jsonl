# Contributing

[English](CONTRIBUTING.md) | [简体中文](CONTRIBUTING.zh-CN.md)

## Development

Use the Go version in [go.mod](go.mod). The project builds as a single executable;
the integrated tdl-derived login code must not depend on a separate runtime binary.

```sh
go build -trimpath -o telegram-jsonl .
go test -race ./...
go vet ./...
```

Use `gofmt` on changed Go files. Automated tests must not require Telegram
credentials or contact real accounts. Use temporary directories and simulated
RPC responses for lifecycle, archive, migration, and download tests.

## Changes

Keep changes scoped and explain the user-visible behavior and relevant validation.
Add regression coverage when changing account binding, session isolation, deletion,
protection, history windows, or attachment validation. Preserve `archive` as the
only JSONL writer; `search` and `fetch` must remain read-only consumers.

Never commit account sessions, login tokens, application secrets, message archives,
downloaded attachments, or logs containing personal content. Review the actual
staged diff; `.gitignore` cannot protect secrets already tracked by Git.
Use synthetic examples in bug reports and documentation.

English is the default documentation language. Update the corresponding
`.zh-CN.md` page when changing user-facing documentation, and keep the language
switch at the top. Keep README focused on installation and first use; put detailed
configuration, edge cases, and examples in `docs/`.

## Commits and Pull Requests

Use a conventional commit subject such as `fix(fetch): reject replaced attachments`
or `docs: explain session migration`. Describe why in the body when it helps
review. Separate unrelated changes and include the checks actually run.

Commit messages must use ASCII English. Keep subjects within 72 characters and
omit a trailing period. Separate the subject, body, and trailers with blank lines.
Breaking changes require both `!` in the subject and a `BREAKING CHANGE:` trailer
that explains the change and migration. Each commit should compile on its own.

Do not claim real-account validation from mock tests or offline client checks.
If you run an authorized integration test, describe its scope without attaching
credentials, account identifiers, or conversation content.

## License

Contributions are provided under this project's [AGPL v3.0 license](LICENSE).
Only submit code and documentation you have the right to contribute. Preserve
upstream notices and identify modifications to derived third-party code.
See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Releases

The repository's tag-triggered workflow builds and publishes multi-platform
artifacts. Maintainer instructions are in [docs/releasing.md](docs/releasing.md).
Report security issues through [SECURITY.md](SECURITY.md).
