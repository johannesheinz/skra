#!/bin/bash
# PostToolUse hook: gofmt any Go file Claude edits, so CI's `gofmt -l` check never fails.
file=$(jq -r '.tool_input.file_path // empty')
[[ "$file" == *.go && -f "$file" ]] || exit 0
gofmt -w "$file"
