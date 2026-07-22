#!/usr/bin/env bash
# Record that wrap-docs ran against the current staged content,
# by storing that content's hash in .git/wrap-docs-hash.
# Run it after staging the reflow; restaging anything invalidates the record.
# The hash must be computed exactly as wrap-docs-gate.sh computes it:
# command substitution strips the diff's trailing newline, a pipe does not.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 1
staged=$(git diff --cached)
printf '%s' "$staged" | shasum -a 256 | cut -d' ' -f1 \
	> "$(git rev-parse --git-dir)/wrap-docs-hash"
echo "wrap-docs recorded for the current staged content"
