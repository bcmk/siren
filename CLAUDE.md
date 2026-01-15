# Claude Code Guidelines

## Git Commits
- Use one-line commit messages only
- Use conventional commit style
- Do not include Co-Authored-By or other attribution
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

## Platform Notes
- GNU sed is installed as `sed` (no empty string needed for `-i`)
- Don't use `cat -A` (macOS cat doesn't support it)

## Database Migrations
- Migrations are in `internal/db/migrations.go`
- When renaming a table, also rename its primary key constraint.
  PostgreSQL auto-creates it as `tablename_pkey`.
