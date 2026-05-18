#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
WORK_DIR="$(dirname "$SCRIPT_DIR")"

sanitize_repo_name() {
  local raw_name
  raw_name="$(basename "$WORK_DIR")"
  raw_name="$(printf '%s' "$raw_name" | tr '[:upper:]' '[:lower:]')"
  raw_name="$(printf '%s' "$raw_name" | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-{2,}/-/g')"

  if [[ -z "$raw_name" ]]; then
    raw_name="repo"
  fi

  printf '%.32s' "$raw_name"
}

REPO_SLUG="$(sanitize_repo_name)"
REPO_HASH="$(printf '%s' "$WORK_DIR" | sha1sum | cut -c1-12)"

printf 'ralph-worker-%s-%s.service\n' "$REPO_SLUG" "$REPO_HASH"
