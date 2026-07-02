#!/usr/bin/env bash
# Block a bare `go build <pkg>`: it writes a binary to the cwd.
# Use `go vet ./cmd/...` to compile-check, or `go build -o cmd/<n>/ ./cmd/<n>`.
# `go build` must sit at a command position (start or after ;, &, |),
# so quoted mentions (e.g. commit messages) pass.
# `-o path` and `-o=path` in the same command segment both count.
cmd=$(jq -r '.tool_input.command // empty')
if grep -Eq '(^|[;&|])[[:space:]]*go build[^;&|]*cmd/[a-z-]+' <<<"$cmd" \
	&& ! grep -Eq '(^|[;&|])[[:space:]]*go build[^;&|]*[[:space:]]-o[= ]' <<<"$cmd"; then
	echo "Blocked: bare 'go build <pkg>' writes a binary to the cwd." \
		"Use 'go vet ./cmd/...' to compile-check, or 'go build -o cmd/<name>/ ./cmd/<name>'." >&2
	exit 2
fi
