#!/usr/bin/env bash
# Block `git commit` when the staged diff adds comment or doc lines and
# tidy-docs has not run against that exact staged content.
# Approve the staged content with .claude/hooks/tidy-docs-approve.sh, after staging the reflow.
# Replaces an agent hook that never fired: agent hooks are experimental.
input=$(cat)
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
# git, with or without -C <dir>
git_re='git[[:space:]]+(-C[[:space:]]+[^[:space:];&|]+[[:space:]]+)?'
grep -Eq "(^|[;&|])[[:space:]]*${git_re}commit\b" <<<"$cmd" || exit 0

# A cd in the same call moves the commit somewhere this gate can't tell.
if grep -Eq '(^|[;&|(])[[:space:]]*cd[[:space:]]' <<<"$cmd"; then
	echo "Blocked: change directory in its own call, or commit with 'git -C <dir> commit'." >&2
	exit 2
fi

# The commit runs in the directory -C names, else in the session's, which may be a worktree.
cd "$(jq -r '.cwd' <<<"$input")" || exit 0
dir_re='(^|.*[;&|])[[:space:]]*git[[:space:]]+-C[[:space:]]+([^[:space:];&|]+)[[:space:]]+commit([[:space:]].*)?$'
dir=$(sed -nE "s/$dir_re/\2/p" <<<"$cmd")
dir=${dir//[\"\']/}
if ! cd "${dir:-.}" 2>/dev/null; then
	echo "Blocked: can't read the directory after -C. Write it as a plain path, with no variable or ~." >&2
	exit 2
fi
cd "$(git rev-parse --show-toplevel)" || exit 0
[ -x .claude/hooks/tidy-docs-approve.sh ] || exit 0

# Staging inside the committing command hides content from this gate:
# PreToolUse runs before the command, so the diff read here would predate it.
if grep -Eq "(^|[;&|])[[:space:]]*${git_re}add\b" <<<"$cmd" ||
	grep -Eq "${git_re}commit[^;&|]*[[:space:]](--all\b|-[a-zA-Z]*a)" <<<"$cmd"; then
	echo "Blocked: stage in its own call. 'git add ... ; git commit' and" \
		"'git commit -a' stage content this gate cannot see yet." >&2
	exit 2
fi

staged=$(git diff --cached)
[ -n "$staged" ] || exit 0

# Added markdown prose, or added Go, shell or SQL comment lines.
# Every added markdown line counts: over-detecting only costs a tidy-docs run.
# Go matches leading and trailing comments, `/* */` continuation lines and SQL `--` lines.
# A `//` right after a colon is a URL in a string, not a comment.
# Shell covers the extensionless scripts/* files and every *.sh; a `#!` shebang is not a comment.
docs=$(git diff --cached -U0 -- '*.md' | grep -E '^\+[^+]' || true)
# A SQL name tag is not prose: tidy-docs does not apply to it.
comment_re='^\+[[:space:]]*(//|/\*|\*)|^\+.*[^:]//|^\+.*/\*|^\+[[:space:]]*--([[:space:]]|$)'
tag_re='^\+[[:space:]]*/\*name='\''[a-z0-9_]+'\''\*/(`[^[:alnum:]]*)?$'
comments=$(git diff --cached -U0 -- '*.go' | grep -E "$comment_re" | grep -Ev "$tag_re" || true)
shell=$(git diff --cached -U0 -- 'scripts/*' '*.sh' | grep -E '^\+[[:space:]]*#([^!]|$)' || true)
sql=$(git diff --cached -U0 -- '*.sql' | grep -E '^\+([^+]|$)' | grep -F -e '--' || true)
[ -n "$docs$comments$shell$sql" ] || exit 0

hash=$(printf '%s' "$staged" | shasum -a 256 | cut -d' ' -f1)
recorded="$(git rev-parse --git-dir)/tidy-docs-hash"
[ -f "$recorded" ] && [ "$(cat "$recorded")" = "$hash" ] && exit 0

echo "Blocked: the staged diff adds comments or docs and tidy-docs has not run" \
	"on this exact staged content. You MUST check every changed comment and doc line" \
	"against the tidy-docs skill, not just run the approval script:" \
	"an approval without the check is a lie the gate cannot detect." \
	"Run the skill, stage the reflow, then approve it with" \
	"$PWD/.claude/hooks/tidy-docs-approve.sh and commit again." >&2
exit 2
