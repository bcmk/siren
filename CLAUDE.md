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
- For doc comments containing code blocks, use `/* */` instead of `//`
  so gofmt's tab indent renders cleanly without the `//` eating columns
- Use true em-dash (—) in comments when grammar requires.
  Do not use em-dashes in log messages — prefer `:` or `,` there.
- In log messages, write `key = value` with spaces around `=`
  (e.g., `@uid = %d`, `head = %s`), not `key=%s`.
- Wrap documentation and comments at 80 characters max,
  prefer breaking at full stops over commas over natural pauses over spaces
- Prefer short comments: one line is the default.
  Add more lines only when required to understand the code.
- Keep lines no longer than 120 characters
- Never hardcode user-facing strings — always use
  the translation system (`res/translations/`)

## Checks and Tests

- Run `prettier --write` on markdown files after changes (no `npx`)
- Run `go fmt ./...` after changes and before committing
- Run `golangci-lint run ./...` before committing
- Run `go test ./...` to ensure changes work
- Ask before modifying tests — explain what needs changing and why
- Use table-driven tests with `t.Run` subtests

## Build

- When building binaries for whatever reason, e.g. to check if code compiles,
  always place them in their main.go's directory,
  e.g. `go build -o cmd/bot/ ./cmd/bot`
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
  then `git tag -a v<version>` on that commit and run the publish
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
- A `vacuum` statement must be alone in its own `no_transaction` file.
- When a migration needs multiple files, use `_1`, `_2` suffixes.
  They must share the same base name
  (differing only in number prefix, suffix, and `no_transaction`).
- When renaming a table, also rename its primary key constraint.
  PostgreSQL auto-creates it as `tablename_pkey`.
- Don't indent continuation lines in multi-line SQL statements

## Documentation

- Read `docs/status-changes.md` before modifying status handling code
- Read `docs/streamer-search.md` before modifying streamer fuzzy search
- Read `docs/architecture-diagrams.md` before adding or regenerating
  an architecture diagram (`docs/*.dot` / `docs/*.pdf`)

## Code Locations

- Bot main entry point: `cmd/bot/main.go`
- Site-specific checkers: `internal/checkers/`
- SQL queries: `internal/db/sql_queries.go`
- Database migrations: `internal/db/migrations/`
- Translations: `res/translations/`
- Architecture diagrams: `docs/*.dot`, built with `scripts/build-diagram`
