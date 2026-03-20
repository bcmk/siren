# Claude Code Guidelines

## Git

- Never use `git -C` — the working directory is already the repo root
- Prefer modern git commands, e.g. `switch` over `checkout` for branches
- Don't use slashes in branch names
- Use one-line commit messages only
- Don't use heredocs for commit messages, use `git commit -m "message"`
- Use conventional commit style
- Use site scope for single-site changes, e.g. `feat(chaturbate): add room subject`
- When stashing, use a descriptive name
- Never delete tags after pushing to the container registry
- After a mistake is corrected, ask if a new guideline
  should be added to CLAUDE.md to prevent it in the future
- Before modifying a file using an ad-hoc script (e.g. `sed`),
  `git add` it first so the pre-script state can be restored.
  This includes new untracked files

## Refactoring

- Use `gopls rename` for renaming Go identifiers, not `sed`.
  Always verify line/column points to the right identifier
  before running, and check at least one occurrence after.
  Run `go fmt` after `gopls rename`

## Code Style

- Put function arguments on separate lines if they don't fit on one line
- Format multiline SQL with backticks on their own lines:
  ```
  `
      select foo
      from bar
  `
  ```
- ALWAYS use lowercase SQL keywords,
  including in documentation and conversations
- Use 4 spaces to show code in comments
- Use true em-dash (—) in comments when grammar requires
- Wrap documentation and comments at 80 characters max,
  prefer breaking at full stops over commas over natural pauses over spaces
- Keep lines no longer than 120 characters
- Never hardcode user-facing strings — always use
  the translation system (`res/translations/`)

## Checks and Tests

- Run `prettier --write` on markdown files after changes (no `npx`)
- Run `go fmt ./...` after changes and before committing
- Run `golangci-lint run ./...` before committing
- Run `go test ./...` to ensure changes work
- Ask before modifying tests — explain what needs changing and why

## Build

- When building binaries for whatever reason, e.g. to check if code compiles,
  always place them in their main.go's directory,
  e.g. `go build -o cmd/bot/ ./cmd/bot`
- To build a Docker image and publish to the registry,
  run `scripts/publish` (uses `git describe --tags` as version)
- Only create tags for completed features ready to be published.
  `scripts/publish` works without tagging —
  it produces versions like `v2.9.0-2-gabcdef12`.
- Run `scripts/query-registry-versions`
  to list images in the container registry.

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

## Database

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

## Code Locations

- Bot main entry point: `cmd/bot/main.go`
- Site-specific checkers: `internal/checkers/`
- SQL queries: `internal/db/sql_queries.go`
- Database migrations: `internal/db/migrations/`
- Translations: `res/translations/`
