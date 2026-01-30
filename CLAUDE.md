# Claude Code Guidelines

## Git
- Prefer modern git commands, e.g. `switch` over `checkout` for branches
- Don't use slashes in branch names
- Use one-line commit messages only
- Use conventional commit style
- Use site scope for single-site changes, e.g. `feat(chaturbate): add room subject`
- When stashing, use a descriptive name

## Code Style
- Put function arguments on separate lines if they don't fit on one line
- Format multiline SQL with backticks on their own lines:
  ```
  `
      select foo
      from bar
  `
  ```
- ALWAYS use lowercase SQL keywords, including in conversation examples
- Use 4 spaces to show code in comments
- Use true em-dash (—) in comments when grammar requires
- Wrap documentation and comments at 80 characters max,
  prefer breaking at full stops over commas over natural pauses over spaces
- Keep lines no longer than 120 characters

## Checks and Tests
- Run `go fmt ./...` after changes
- Run `golangci-lint run ./...` before committing
- Run `go test ./...` to ensure changes work
- Ask before modifying tests — explain what needs changing and why

## Build
- When building binaries, place them in the command's directory,
  e.g. `go build -o cmd/bot/ ./cmd/bot`

## Platform Notes
- GNU sed is installed as `sed` (no empty string needed for `-i`)
- Don't use `cat -A` (macOS cat doesn't support it)
- Many commands are allowed in the project directory — prefer relative paths

## Database Migrations
- SQL files are in `internal/db/migrations/`, runner is in `internal/db/migrations.go`
- Filename format: `0001_name.sql` (number for ordering, name stored in DB)
- Use `0001_no_transaction_name.sql` for statements like VACUUM that cannot
  run inside a transaction
- When renaming a table, also rename its primary key constraint.
  PostgreSQL auto-creates it as `tablename_pkey`.

## Documentation
- Read `docs/status-changes.md` before modifying status handling code

## Code Locations
- Bot main entry point: `cmd/bot/main.go`
- Site-specific checkers: `internal/checkers/`
- SQL queries: `internal/db/sql_queries.go`
- Database migrations: `internal/db/migrations/`
- Translations: `res/translations/`
