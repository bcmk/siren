#!/usr/bin/env bash
# Approve the current staged content, attesting that tidy-docs ran against it,
# by storing that content's hash in .git/tidy-docs-hash.
# Run it after staging the reflow; restaging anything invalidates the approval.
# The hash must be computed exactly as tidy-docs-gate.sh computes it:
# command substitution strips the diff's trailing newline, a pipe does not.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 1
staged=$(git diff --cached)
printf '%s' "$staged" | shasum -a 256 | cut -d' ' -f1 > "$(git rev-parse --git-dir)/tidy-docs-hash"
echo "tidy-docs approved for the current staged content"
