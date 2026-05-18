On startup, read your previous progress
```bash
/bin/bash .ralph/progress_read.sh "<codex>"
```

Append to the progress log — it is your working memory across context windows.
Please write very often.
```bash
/bin/bash .ralph/progress_append.sh "<codex>" << 'EOF_APPEND_PROGRESS_LOG'
- what you did
- what happened
- should do next, after quitting immediately due to context limit
EOF_APPEND_PROGRESS_LOG
```

Improve one small, concrete part of the current task or repo.

when done: 
- manually verify the real feature/functionality/task with concrete calls, commands, logs, screenshots, external service status, or other evidence that proves it works
- record the verification evidence; do not mark done from a shallow checkbox, assumption, or code inspection only
- run only targeted checks that are useful evidence for the specific change
- commit (including .ralph changes)
- push
- bash .ralph/task_switch.sh
