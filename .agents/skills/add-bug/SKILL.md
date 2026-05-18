---
name: add-bug
description: Create a bug task file in .ralph/tasks/bugs/. Triggers on "add bug", "create bug", "new bug", "/add-bug".
---

## Where to create

- `mkdir -p .ralph/tasks/bugs/`
- Write the bug file: `.ralph/tasks/bugs/bug-slug.md`

## Bug file format

Every bug fix must be manually verified with concrete evidence that the broken behavior is fixed.
Useful targeted checks may be included when they directly prove the bug is fixed, and the bug task must require manual verification with real calls, commands, logs, screenshots, external service status, or equivalent evidence.
Do not accept shallow checkbox completion, code inspection only, or "it should work" reasoning as verification.

```markdown
## Bug: Bug Title <status>not_started</status> <passes>false</passes> <priority>optional: medium|high|ultra high</priority>

<description>
[What is broken and how it was detected.]
</description>

<mandatory_manual_verification>
Manually reproduce or inspect the broken behavior, fix it, then manually verify with concrete evidence that the bug no longer occurs.
Record the exact calls, commands, logs, screenshots, external service status, or other evidence used for verification.
</mandatory_manual_verification>

<acceptance_criteria>
- [ ] I reproduced or inspected the broken behavior enough to understand the failure.
- [ ] I fixed the bug.
- [ ] I manually verified with concrete calls, commands, logs, screenshots, external service status, or other evidence that the bug no longer occurs.
- [ ] The verification evidence is recorded in the task or linked artifact.
</acceptance_criteria>
```
