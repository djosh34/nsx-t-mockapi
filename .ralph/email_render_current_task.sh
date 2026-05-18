#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$(dirname "$SCRIPT_DIR")"
CURRENT_TASK_POINTER="$SCRIPT_DIR/current_task.txt"

resolve_repo_path() {
    local repo_path="$1"

    if [[ "$repo_path" == /* ]]; then
        printf '%s\n' "$repo_path"
        return
    fi

    printf '%s/%s\n' "$WORK_DIR" "$repo_path"
}

if [[ ! -f "$CURRENT_TASK_POINTER" ]]; then
    printf '%s\n' '(current task not set)'
    exit 0
fi

CURRENT_TASK_PATH=$(cat "$CURRENT_TASK_POINTER")
if [[ -z "$CURRENT_TASK_PATH" ]]; then
    printf '%s\n' '(current task not set)'
    exit 0
fi

CURRENT_TASK_FILE="$(resolve_repo_path "$CURRENT_TASK_PATH")"
if [[ ! -f "$CURRENT_TASK_FILE" ]]; then
    printf '%s\n' "(current task file missing: $CURRENT_TASK_PATH)"
    exit 0
fi

cat "$CURRENT_TASK_FILE"
