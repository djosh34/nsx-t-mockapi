#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TASKS_DIR="$SCRIPT_DIR/tasks"
SEPARATOR_WIDTH=30
WRAP_WIDTH=36
SEPARATOR="$(printf '%*s' "$SEPARATOR_WIDTH" '' | tr ' ' '=')"

wrap_with_indent() {
    local indent="$1"
    local text="$2"
    local content_width=$((WRAP_WIDTH - ${#indent}))

    while IFS= read -r line; do
        printf '%s%s\n' "$indent" "$line"
    done < <(
        printf '%s\n' "$text" \
            | sed 's/-/- /g' \
            | fold -s -w "$content_width" \
            | sed 's/- /-/g; s/ $//'
    )
}

read_task_field() {
    local task_file="$1"
    local field_name="$2"
    local header

    header=$(grep -m1 '^## ' "$task_file" || true)
    printf '%s\n' "$header" | sed -n "s/.*<${field_name}>\\([^<]*\\)<\\/${field_name}>.*/\\1/p"
}

render_story() {
    local story_dir="$1"
    local story_name task_file task_files
    local done_count=0
    local total_count=0
    local passes priority priority_label status_icon task_name

    story_name=$(basename "$story_dir")
    mapfile -t task_files < <(find "$story_dir" -maxdepth 1 -type f -name '*.md' | sort)

    for task_file in "${task_files[@]}"; do
        passes=$(read_task_field "$task_file" "passes")
        total_count=$((total_count + 1))
        if [[ "$passes" == "true" ]]; then
            done_count=$((done_count + 1))
        fi
    done

    wrap_with_indent "  " "($done_count/$total_count) $story_name"
    printf '\n'

    for task_file in "${task_files[@]}"; do
        task_name=$(basename "$task_file" .md)
        passes=$(read_task_field "$task_file" "passes")
        priority=$(read_task_field "$task_file" "priority")

        if [[ -n "$priority" ]]; then
            priority_label="[$priority]"
        else
            priority_label="[-]"
        fi

        if [[ "$passes" == "true" ]]; then
            status_icon="✅"
        else
            status_icon="❌"
        fi

        printf '    %s %s\n' "$priority_label" "$status_icon"
        wrap_with_indent "    " "$task_name"
        printf '\n'
    done

}

main() {
    local story_dir story_dirs=()
    local index

    mapfile -t story_dirs < <(find "$TASKS_DIR" -mindepth 1 -maxdepth 1 -type d | sort)

    printf '%s\n%s\n%s\n\n' "$SEPARATOR" "$SEPARATOR" "$SEPARATOR"

    for index in "${!story_dirs[@]}"; do
        story_dir="${story_dirs[$index]}"
        render_story "$story_dir"
        if (( index < ${#story_dirs[@]} - 1 )); then
            printf '%s\n%s\n\n' "$SEPARATOR" "$SEPARATOR"
        fi
    done

    printf '%s\n%s\n%s\n' "$SEPARATOR" "$SEPARATOR" "$SEPARATOR"
}

main "$@"
