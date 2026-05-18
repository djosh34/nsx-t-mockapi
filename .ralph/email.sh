#!/bin/bash
# Email script for task updates
# Usage: .ralph/email.sh [finish]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RALPH_DIR="$SCRIPT_DIR"
WORK_DIR="$(dirname "$SCRIPT_DIR")"

resolve_repo_path() {
    local repo_path="$1"
    if [[ "$repo_path" == /* ]]; then
        printf '%s\n' "$repo_path"
        return
    fi
    printf '%s/%s\n' "$WORK_DIR" "$repo_path"
}

# Check for finish argument
FINISH_MODE=false
if [[ "$1" == "finish" ]]; then
    FINISH_MODE=true
fi

# Always run update_task_summary.sh first
if [[ -x "$RALPH_DIR/update_task_summary.sh" ]]; then
    "$RALPH_DIR/update_task_summary.sh"
fi

# Get iteration number
ITERATION_NUMBER=$("$RALPH_DIR/email_get_iteration.sh" 2>/dev/null) || true

# Read email addresses
SEND_FROM=$(cat ~/send_from 2>/dev/null) || { echo "Error: ~/send_from not found"; exit 1; }
SEND_TO=$(cat ~/send_to 2>/dev/null) || { echo "Error: ~/send_to not found"; exit 1; }

# Get current task path and name
CURRENT_TASK_PATH=""
CURRENT_TASK_NAME=""
CURRENT_TASK_CONTENT=""

if [[ -f "$RALPH_DIR/current_task.txt" ]]; then
    CURRENT_TASK_PATH=$(cat "$RALPH_DIR/current_task.txt")
    CURRENT_TASK_FILE=""
    if [[ -n "$CURRENT_TASK_PATH" ]]; then
        CURRENT_TASK_FILE="$(resolve_repo_path "$CURRENT_TASK_PATH")"
    fi
    if [[ -n "$CURRENT_TASK_FILE" && -f "$CURRENT_TASK_FILE" ]]; then
        CURRENT_TASK_NAME=$(basename "$CURRENT_TASK_PATH" .md)
    fi
fi

# Render task sections directly from task files
TASK_DIGEST=$("$RALPH_DIR/email_render_story_digest.sh")
CURRENT_TASK_CONTENT=$("$RALPH_DIR/email_render_current_task.sh")

# Count pass/fail tasks directly from task files
COUNT_PASS=0
COUNT_FAIL=0
while IFS= read -r task_file; do
    [[ -f "$task_file" ]] || continue
    task_header=$(grep -m1 '^## ' "$task_file" || true)
    if [[ "$task_header" =~ \<passes\>([^<]+)\</passes\> ]]; then
        case "${BASH_REMATCH[1]}" in
            false) ((COUNT_FAIL++)) || true ;;
            true)  ((COUNT_PASS++)) || true ;;
        esac
    fi
done < <(find "$RALPH_DIR/tasks" -mindepth 2 -maxdepth 2 -type f -name '*.md' | sort)
COUNT_TOTAL=$((COUNT_PASS + COUNT_FAIL))
TASK_COUNTS="${COUNT_PASS}/${COUNT_TOTAL}"

# Get progress content
PROGRESS=$("$SCRIPT_DIR/progress_read.sh" EMAIL 2>/dev/null) || true

# Build gauges
TASK_PATH_VAL="${CURRENT_TASK_PATH:-(not set)}"
TASK_NAME_VAL="${CURRENT_TASK_NAME:-(not set)}"
LINE_DIFF_VAL=$("$SCRIPT_DIR/git_diff_lines_since.sh" 2>/dev/null) || true
PROGRESS_VAL="not found"
[[ -n "$PROGRESS" ]] && PROGRESS_VAL="exists (see below)"

GAUGES="task_passes:                 $TASK_COUNTS
task_name:                      $TASK_NAME_VAL
progress:                         $PROGRESS_VAL
task_file:                         $TASK_PATH_VAL
line-diffs:                         $LINE_DIFF_VAL"

# Build email
# Choose subject prefix
if [[ "$FINISH_MODE" == true ]]; then
    SUBJECT_ACTION="Finished"
else
    SUBJECT_ACTION=""
fi
SUBJECT="[$ITERATION_NUMBER] $TASK_COUNTS $SUBJECT_ACTION: $CURRENT_TASK_NAME $LINE_DIFF_VAL"

TASK_ITERATION=$("$RALPH_DIR/task_get_iteration.sh" 2>/dev/null) || true

# Build finish section from current task
FINISH_SECTION=""
if [[ "$FINISH_MODE" == true && -n "$CURRENT_TASK_NAME" ]]; then
    FINISH_SECTION="--- Finished Task ---
[$TASK_ITERATION] $CURRENT_TASK_NAME

"
fi

BODY=$(cat <<EOF
$GAUGES

${FINISH_SECTION}--- Progress $TASK_ITERATION.jsonl ---
$PROGRESS

$TASK_DIGEST

--- Task ---
$CURRENT_TASK_CONTENT
EOF
)

# Construct and send email
EMAIL_CONTENT=$(cat <<EOF
From: Ralph Bot <$SEND_FROM>
To: $SEND_TO
Subject: $SUBJECT
Content-Type: text/plain; charset=utf-8

$BODY
EOF
)

echo "Sending email: $SUBJECT"
echo "$EMAIL_CONTENT" | msmtp "$SEND_TO"

if [[ $? -eq 0 ]]; then
    echo "Email sent successfully"
else
    echo "Failed to send email"
    exit 1
fi
