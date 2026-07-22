#!/usr/bin/env bash
# Block `git commit` when the staged diff adds comment or doc lines and
# wrap-docs has not run against that exact staged content.
# Record a run with .claude/hooks/wrap-docs-ok.sh, after staging the reflow.
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

# Added markdown prose, or added Go comment lines.
# Every added markdown line counts: over-detecting only costs a wrap-docs run.
# Go matches leading and trailing comments, and `/* */` continuation lines.
# A `//` right after a colon is a URL in a string, not a comment.
docs=$(git diff --cached -U0 -- '*.md' | grep -E '^\+[^+]' || true)
comment_re='^\+[[:space:]]*(//|/\*|\*)|^\+.*[^:]//|^\+.*/\*'
comments=$(git diff --cached -U0 -- '*.go' | grep -E "$comment_re" || true)
[ -n "$docs$comments" ] || exit 0

hash=$(printf '%s' "$staged" | shasum -a 256 | cut -d' ' -f1)
recorded="$(git rev-parse --git-dir)/wrap-docs-hash"
[ -f "$recorded" ] && [ "$(cat "$recorded")" = "$hash" ] && exit 0

echo "Blocked: the staged diff adds comments or docs and wrap-docs has not run" \
	"on this exact staged content. Run the wrap-docs skill, stage the reflow," \
	"then record it with .claude/hooks/wrap-docs-ok.sh and commit again." >&2
exit 2
