# Claude Code Guidelines

## Git

- Never use `git -C` — the working directory is already the repo root,
  and `-C` triggers an extra permission prompt.
  If the wrong cwd is in effect, run a bare `cd /path` first (no `&&`, no chain)
- Prefer modern git commands, e.g. `switch` over `checkout` for branches
- Commit directly on `master` unless asked to use a branch
- Don't use slashes in branch names
- Use one-line commit messages only
- Don't use heredocs for commit messages, use `git commit -m "message"`
- Use conventional commit style
- When fixing review findings on not-yet-pushed commits,
  land each fix as a `git commit --fixup=<sha>`
  into the commit that introduced the issue,
  then autosquash — don't amend the tip or add a follow-up commit,
  so every commit in the stack stays independently correct.
  Judge the target commit by what the fix relates to, not by where it compiles.
- Use site scope for single-site changes, e.g. `feat(chaturbate): add room subject`
  or `refactor(myfreecams): ...` for bot-side MyFreeCams code in
  `internal/checkers/` (the `adapter-mfc` scope is only for the
  `cmd/adapter-mfc` daemon).
- When stashing, use a descriptive name
- Never push to remote without explicit permission
- Never delete tags after pushing to the container registry
- After a mistake is corrected, ask if a new guideline
  should be added to CLAUDE.md to prevent it in the future
- Before modifying a file using an ad-hoc script (e.g. `sed`),
  `git add` it first so the pre-script state can be restored.
  This includes new untracked files

## Refactoring

- Use `gopls rename -w` for renaming Go identifiers, not `sed`.
  Always verify line/column points to the right identifier
  before running, and check at least one occurrence after.
  Run `go fmt` after `gopls rename`

## Code Style

- Put function arguments on separate lines if they don't fit on one line
- Format multiline SQL with the opening backtick
  on the preceding line and SQL indented one level:
  ```go
  d.MustExec(`
      select foo
      from bar`,
      args)
  ```
  When arguments are on separate lines, the backtick goes on its own line:
  ```go
  someFunc(
      longArg1,
      `
          select foo
          from bar
      `,
      longArg2)
  ```
- Use tabs for SQL indentation inside Go backtick strings —
  match the surrounding Go indentation
- ALWAYS use lowercase SQL keywords,
  including in documentation and conversations
- Don't extract shared SQL fragments for DRY; write each query out in full.
  Build dynamically only when the query itself must vary at runtime.
- For doc comments containing code blocks, use `/* */` instead of `//`
  so gofmt's tab indent renders cleanly without the `//` eating columns
- Use true em-dash (—) in comments when grammar requires.
  Do not use em-dashes in log messages — prefer `:` or `,` there.
- In log messages, write `key = value` with spaces around `=`
  (e.g., `@uid = %d`, `head = %s`), not `key=%s`.
- Wrap documentation (including CLAUDE.md) and comments at 80 characters max.
  Keep elementary discourse units on the same line —
  prefer breaking at full stops over semicolons over em-dashes
  over commas over natural pauses over spaces.
  Never break a line mid-phrase; break only at the boundaries above.
  The `wrap-docs` skill applies this rule — see Skills.
- Prefer short comments: one line is the default.
  Add more lines only when required to understand the code.
- Keep lines no longer than 120 characters
- Never hardcode user-facing strings — always use
  the translation system (`res/translations/`)

## Checks and Tests

- Run `npx prettier --write` on markdown files after changes
- Before committing, rewrap changed docs and comments
  to the 80-char discourse-boundary rules under Code Style.
  Run the `wrap-docs` skill via a subagent that follows
  `.claude/skills/wrap-docs/SKILL.md`,
  so the reflow stays out of the main context;
  or run `/wrap-docs` inline.
- Run `go fmt ./...` after changes and before committing
- Run `golangci-lint run ./...` before committing
- Run `go test ./...` to ensure changes work
- Ask before modifying tests — explain what needs changing and why
- Use table-driven tests with `t.Run` subtests

## Build

- When building binaries for whatever reason, e.g. to check if code compiles,
  always place them in their main.go's directory,
  e.g. `go build -o cmd/bot/ ./cmd/bot`.
  Never build with a bare `go build ./cmd/bot`.
  Without `-o`, the binary lands in the current directory, not cmd/bot/.
- Do not clean up binaries you built while they sit in their correct
  directories — the project's `.gitignore` already excludes them.
- To build a Docker image and publish to the registry,
  run the matching `scripts/publish-<name>` script
  (e.g. `scripts/publish-bot`, `scripts/publish-adapter-mfc`).
  All publish scripts use `git describe --tags` as the version.
- Only create tags for completed features ready to be published.
  Publish scripts work without tagging —
  they produce versions like `v2.9.0-2-gabcdef12`.
- Run `scripts/query-registry-versions <repo>`
  (e.g. `scripts/query-registry-versions bot`) to list images
  in the container registry.

## Releases

- On tagging, ensure `CHANGELOG.md` has a `## v<version> — <date>`
  section at the top describing what's in the release (em-dash, ISO
  date). If `## Unreleased` exists, promote it; otherwise write the
  section fresh from `git log v<prev>..HEAD`. Don't presume Unreleased
  is maintained between releases — sometimes it isn't there.
  Commit the CHANGELOG update as `chore: release v<version>`,
  then `git tag v<version>` on that commit and run the publish
  scripts.
- Keep CHANGELOG entries terse — ideally one line per change,
  no rationale paragraphs. State what changed, not why.
- Bump major on user-visible breaking changes (config layout splits,
  CLI flag removals/renames, env-var renames, ops-action-required
  schema migrations). Bump minor for new features, patch for fixes.
  `git log v<prev>..HEAD` is the source of truth for what's in scope.
- After publishing, bump the matching `siren-config` image refs:
  `bump-all-bots v<version>` (covers prod/test bot charts) plus a
  `sed` on `prod/prod-adapter-mfc/values.yaml` when the adapter ships.
- Pushing tags to origin requires explicit permission, like any push.

## Go Module Version

- The module path's major-version suffix in `go.mod`
  must match the release tags' major version.
- To bump it, rewrite `github.com/bcmk/siren/vN` to the new `vN` in `go.mod`,
  every Go import, the `build-*` script `-ldflags`, and the README badge URLs.

## Communication

- Always suggest English grammar/style fixes
  in the user's messages
- When asked to "remember" or "write somewhere",
  save to CLAUDE.md or docs/\* files, not memory files,
  unless specifically asked for memory files

## Platform Notes

- GNU sed is installed as `sed` (no empty string needed for `-i`)
- Don't use `cat -A` (macOS cat doesn't support it)
- Many commands are allowed in the project directory — prefer relative paths
- Never read credential files (~/.pgpass, ~/.netrc, .env, etc.)
- Never store personal info (usernames, credentials, connection strings)
  in memory files
- Use WebFetch/WebSearch freely without asking permission
- Never chain shell commands with `&&` —
  chained commands trigger extra permission prompts.
  Run commands one after another instead.

## Database

- Run `cmd/schema-dump/schema-dump` to get the full database schema
- BRIN indexes require explicit `brin_summarize_new_values` calls
- Read `docs/testing-database.md` before working with the
  testing database environment
- pgx caches prepared statements, which can cause
  PostgreSQL to use slow generic plans.
  We use `pgx.QueryExecModeExec` to avoid this when needed.

## Database Migrations

- SQL files are in `internal/db/migrations/`, runner is in `internal/db/migrations.go`
- Filename format: `0001_name.sql` (number for ordering, name stored in DB)
- Use `0001_no_transaction_name.sql` for statements like `vacuum` that cannot
  run inside a transaction
- Each `vacuum` statement must be alone in its own `no_transaction` file.
  Two vacuums cannot share a migration file.
  PostgreSQL's simple-query protocol
  wraps a multi-statement Exec call in an implicit transaction,
  while a single-statement Exec runs without one.
- After `cluster` on a large table,
  follow up with `vacuum analyze` in `no_transaction` migrations.
  `cluster` resets the visibility map and leaves correlation stats out of date.
  Plain `analyze` updates `pg_stats` but does not touch the visibility map.
  Refs: <https://www.postgresql.org/docs/current/sql-cluster.html>,
  <https://www.postgresql.org/docs/current/storage-vm.html>.
- When a migration needs multiple files, use `_1`, `_2` suffixes.
  They must share the same base name
  (differing only in number prefix, suffix, and `no_transaction`).
- When renaming a table, also rename its primary key constraint.
  PostgreSQL auto-creates it as `tablename_pkey`.
- Name constraints and indexes:
  foreign keys `fk_<table>_<column>`, check constraints `chk_<table>_<column>`,
  indexes `ix_<table>_<columns>`, primary keys `<table>_pkey`.
  Use a unique `ix_` index for uniqueness, not a unique constraint.
- Don't indent continuation lines in multi-line SQL statements

## Commands and Logging

- When adding a command to the bot, add it to the `loggedCommands` map
  so its usage gets logged.
- To log a command-like event (invoice, payment, pre-checkout, callback),
  call `LogReceivedMessage` directly with a name for it (as `search` does).

## Documentation

- Don't mention other projects in CLAUDE.md or docs; keep them about this repo.
- Read `docs/status-changes.md` before modifying status handling code
- Read `docs/streamer-search.md` before modifying streamer fuzzy search
- Read `docs/architecture-diagrams.md` before adding or regenerating
  an architecture diagram (`docs/*.dot` / `docs/*.pdf`)
- Read `docs/brin-maintenance.md` before changing any BRIN index,
  recreating one, or running operations
  that could affect physical row order on a BRIN-indexed table.
- Read `docs/telegram-stars.md` before changing Telegram Stars payment handling
  (buying subscriptions, invoices, pre-checkout, refunds).

## Skills

- Skills live in `.claude/skills/<name>/SKILL.md`;
  each skill's description names the rule it applies.
  Here "docs" covers markdown documentation and code comments both.
- `wrap-docs` — applies the 80-char docs splitting rule
  (Code Style: "Wrap documentation ... and comments at 80 characters").
  Run it before committing doc or comment changes;
  prefer a subagent so the reflow stays out of the main context.

## Code Locations

- Bot main entry point: `cmd/bot/main.go`
- Site-specific checkers: `internal/checkers/`
- SQL queries: `internal/db/sql_queries.go`
- Database migrations: `internal/db/migrations/`
- Translations: `res/translations/`
- Architecture diagrams: `docs/*.dot`, built with `scripts/build-diagram`
