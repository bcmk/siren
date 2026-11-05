#!/usr/bin/env bash
# Approve the staged content of the checkout this script lives in,
# attesting that tidy-docs ran against it, by storing its hash in the checkout's git directory.
# Run it after staging the reflow; restaging anything invalidates the approval.
# The hash must be computed exactly as tidy-docs-gate.sh computes it:
# command substitution strips the diff's trailing newline, a pipe does not.
cd "$(dirname "$0")" || exit 1
staged=$(git diff --cached)
printf '%s' "$staged" | shasum -a 256 | cut -d' ' -f1 > "$(git rev-parse --git-dir)/tidy-docs-hash"
echo "tidy-docs approved for the current staged content"
