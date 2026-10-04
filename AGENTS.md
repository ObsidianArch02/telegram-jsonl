# Agent Instructions

These instructions apply to the entire repository. Follow the maintainer's
latest explicit instructions when they change a preference in this file.

## Collaboration

- Communicate with the maintainer concisely in the language appropriate to the user's context; use English for code,
  commit messages, CLI messages, and the default documentation.
- Read the relevant code before changing it. Complete implementation and
  appropriate verification without repeatedly asking for routine decisions.
- Explain significant edits before making them and give brief progress updates.
- Report the result, checks actually run, and material limitations. Distinguish
  simulated tests, cross-compilation, credential probes, and real-account tests.
- For substantial work that consumes a large amount of context, use subagents
  with separate file ownership. Coordinate Git staging and commits through one
  agent, and review delegated results before accepting them.

## Atomic Commits

The maintainer authorizes a local commit after every completed atomic change.
Do not wait for a separate `git commit` request or combine unrelated completed
changes into a later catch-all commit.

An atomic change is the smallest complete, reviewable behavior or documentation
change, including the tests and documentation needed for that change. Keep each
commit buildable; do not split a dependent implementation into broken commits.

For each atomic change:

1. Implement it, synchronize its documentation, and run appropriate checks.
2. Inspect `git status --short` and the staged diff. Stage explicit paths or
   selected hunks; preserve unrelated user changes.
3. Check for private data and build artifacts, then create a local commit.
4. Verify the commit and remaining worktree state before starting the next change.

Follow `/Users/sukhoina/@pi-default-dir/commit-skill/AGENTS.md` when available.
That directory and its hooks are read-only. The standing authorization above
allows local commits without another request. If the external rules are absent,
use the rules below and report that their hook could not be run.

- Use Conventional Commits with ASCII English throughout the message.
- Keep the subject at most 72 characters, with no trailing period.
- Separate subject, body, and trailers with blank lines.
- Breaking changes require both `!` in the subject and a `BREAKING CHANGE:`
  trailer explaining the change and what users must do.
- Run the prescribed hook without changing persistent Git configuration or
  bypassing hooks. Use a per-command hook path if needed.
- Give Git commands an explicit timeout. For example, on this machine use
  `perl -e 'alarm 60; exec @ARGV' git ...`.
- Do not bump versions or create release tags unless requested.

## Branches and Upload Approval

- Make all subsequent local modifications and commits on a `dev/<topic>`
  branch. `dev/` is a branch-name prefix, not a literal branch name.
- Reuse the current suitable development branch. If starting on `main`, create
  or switch to a development branch before editing; preserve existing changes.
- Keep `main` unchanged during routine local development. Do not reset or move
  existing commits merely to adopt this convention.
- When preparing a push, identify the actual remote and update its `main`
  reference. Rebase the unpublished development commits onto the latest `main`
  before preparing the final review. Resolve conflicts and run affected checks.
- The maintainer must review the concrete commits, changes, check results, and
  destination, then explicitly approve the upload. Local commit authorization
  does not authorize a GitHub push, release publication, or other upload.
- Push only the reviewed state to the approved destination. If it changes after
  approval, provide the updated review before uploading it.
- Do not rewrite published history, force-push, or bypass hooks without separate
  explicit authorization. Do not rebase another contributor's branch.

## Data and Credentials

- Preserve exported JSONL, archive metadata, stable sessions, and completed
  downloads. Cleanup of retired code must not remove user data.
- Do not stop a running archiver, log an account out, replace an authorization,
  or change a bound application identity as a side effect of code maintenance.
- Keep private state and generated binaries out of ordinary commits and release
  source packages. Review tracked files too; `.gitignore` is not sufficient.
- Do not print credentials, login tokens, authorization keys, phone numbers,
  message bodies, captions, or file references in logs or diagnostic diffs.
- Use synthetic data in tests and examples. Real-account access requires user
  authorization; offline checks must remain offline.
- The maintainer explicitly authorized the existing application-credential
  commit. Preserve that decision without exposing values in tool output. Do not
  introduce additional private credentials into Git without an explicit request.
  Include the credential exposure in any review for a future public upload;
  do not silently rewrite the existing credential commit.

## Architecture and Telegram Behavior

- Build one standalone Go binary. Integrate required upstream code at source
  level; do not restore an embedded executable or external tdl subprocess.
- Keep `archive`, `search`, and `fetch` as subcommands of that binary.
  `archive` is the resident JSONL writer; `search` is offline and read-only;
  `fetch` is a one-shot downloader that never modifies JSONL or archiver state;
  it may write completed-file mappings to the shared SQLite index.
- Store non-message, non-login data in SQLite. Keep chat records in JSONL and
  login authorization in `session.json`. Do not import or convert retired JSON
  state files; preserve existing exports and require a fresh directory when
  they lack SQLite account and deletion metadata.
- Preserve a single account and stable sessions, with separate archive/fetch
  authorizations bound to the same account.
- Keep history backfill configurable and bounded by default. Honor RPC pacing,
  persisted FLOOD_WAIT deadlines, live updates, edits, and deletions.
- Respect self-destructing, protected, restricted, inaccessible, and paid
  content. Revalidate attachments through authorized APIs before downloading
  and before committing a completed download.
- Store compact structured media metadata; avoid automatic bulk downloads.
  Do not promise durable HTTP attachment links or recovery of deleted content.
- Use the project's own application identity. An upstream source license does
  not authorize reuse of another application's Telegram credentials.
- Never claim that official interfaces, TDLib, tdl, or rate limits guarantee
  Telegram compliance or immunity from account restrictions.

## CLI and Documentation

- Send operational progress and errors to stderr. Keep stdout machine-readable
  for commands that return JSON; never mix progress text into those results.
- Show terminal timestamps and human-readable deadlines in the computer's
  local timezone, respecting `TZ`. Keep stored JSONL timestamps in UTC.
- Report useful startup, authorization, synchronization, persistence, download,
  waiting, and shutdown status. Claim successful persistence only after it succeeds.
- Mask typed and pasted phone numbers, login codes, and two-factor passwords
  with stars. Preserve editing, cancellation, and terminal-state restoration.
- Explain both QR and phone/code login in README and retain a practical example
  for searching and downloading an attachment.
- English is the default for project documents. Maintain corresponding Chinese
  pages and a language switch at the top of user-facing documents. `AGENTS.md`
  is English-only and has no Chinese companion.
- Keep README an entry point: a short description, early installation and a
  runnable example, then links to detailed documentation. Aim for 60-120 lines
  for this CLI; move advanced configuration and edge cases into `docs/`.
- Retain the brief AI-assisted-code disclosure and review-before-use reminder
  near the README header. Keep badges and decoration limited.
- Keep AGPL licensing, upstream attribution, Telegram disclaimers, and
  `.gitignore` current. Dependency changes must include regenerated license notices.
- Release automation must build the supported Linux/macOS/Windows targets for
  amd64 and arm64, with source, license notices, and checksums. Keep actions
  pinned and permissions limited; publishing still requires upload approval.

## Task Completion

Before ending every task:

1. Compare the final implementation with the relevant documentation. Update
   affected commands, flags, defaults, data layouts, examples, limitations, and
   development or release instructions as part of the corresponding atomic change.
2. Keep English and Chinese user-facing documents synchronized; `AGENTS.md`
   remains English-only. Check affected links, heading anchors, command examples,
   and documentation included in release packages.
3. Commit each completed atomic change on the development branch, including its
   documentation. Review the resulting commits and run `git status --short` to
   confirm there are no remaining task-owned staged, unstaged, or untracked changes.
4. Workspace cleanup means keeping the Git-managed worktree orderly. Preserve
   ignored build artifacts, temporary builds, dependency and build caches, exported
   data, and login files unless the maintainer explicitly requests their removal.
5. Preserve unrelated user changes rather than reverting, hiding, or sweeping them
   into a commit. If they remain, report them; do not claim the entire worktree is
   clean. Finish with a concise report of documentation updates, checks, commits,
   and the actual Git status. Uploads still require explicit maintainer approval.

## Verification

Use `gofmt` on changed Go files and scale tests to the actual risk. Relevant Go
changes normally require `go test -race ./...`, `go vet ./...`, and a build.
Use temporary directories and simulated RPCs for automated tests. Add regression
coverage for session/account binding, archive lifecycle, history boundaries,
attachment checks, and output privacy when changing those behaviors.

Do not rerun broad checks after a documentation-only change when the tested code
is unchanged. Verify examples, links, language parity, and the staged diff instead.
State any check that could not be completed; never imply it passed.
