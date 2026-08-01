#!/usr/bin/env bash
# Block `git commit` when the staged diff adds comment or doc lines and
# tidy-docs has not run against that exact staged content.
# Approve the staged content with .claude/hooks/tidy-docs-approve.sh, after staging the reflow.
# Replaces an agent hook that never fired: agent hooks are experimental.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

cmd=$(jq -r '.tool_input.command // empty')
grep -Eq '(^|[;&|])[[:space:]]*git[[:space:]]+commit\b' <<<"$cmd" || exit 0

# Staging inside the committing command hides content from this gate:
# PreToolUse runs before the command, so the diff read here would predate it.
if grep -Eq '(^|[;&|])[[:space:]]*git[[:space:]]+add\b' <<<"$cmd" ||
	grep -Eq 'git[[:space:]]+commit[^;&|]*[[:space:]](--all\b|-[a-zA-Z]*a)' <<<"$cmd"; then
	echo "Blocked: stage in its own call. 'git add ... ; git commit' and" \
		"'git commit -a' stage content this gate cannot see yet." >&2
	exit 2
fi

staged=$(git diff --cached)
[ -n "$staged" ] || exit 0

# Added markdown prose, added Go comment lines, or added shell comment lines.
# Every added markdown line counts: over-detecting only costs a tidy-docs run.
# Go matches leading and trailing comments, and `/* */` continuation lines.
# A `//` right after a colon is a URL in a string, not a comment.
# Shell covers the extensionless scripts/* files and every *.sh; a `#!` shebang is not a comment.
docs=$(git diff --cached -U0 -- '*.md' | grep -E '^\+[^+]' || true)
comment_re='^\+[[:space:]]*(//|/\*|\*)|^\+.*[^:]//|^\+.*/\*'
comments=$(git diff --cached -U0 -- '*.go' | grep -E "$comment_re" || true)
shell=$(git diff --cached -U0 -- 'scripts/*' '*.sh' | grep -E '^\+[[:space:]]*#([^!]|$)' || true)
[ -n "$docs$comments$shell" ] || exit 0

hash=$(printf '%s' "$staged" | shasum -a 256 | cut -d' ' -f1)
recorded="$(git rev-parse --git-dir)/tidy-docs-hash"
[ -f "$recorded" ] && [ "$(cat "$recorded")" = "$hash" ] && exit 0

echo "Blocked: the staged diff adds comments or docs and tidy-docs has not run" \
	"on this exact staged content. Run the tidy-docs skill, stage the reflow," \
	"then approve it with .claude/hooks/tidy-docs-approve.sh and commit again." >&2
exit 2
