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
