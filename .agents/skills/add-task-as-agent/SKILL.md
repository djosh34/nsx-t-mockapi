---
name: add-task-as-agent
description: Create a task when the AGENT (Claude) needs to create one. Agents should always use THIS skill, not add-task-as-user.
---

## Purpose

This skill creates **focused, tasks** from completed research. Use your extensive research/subagent explore findings to define clear, concrete tasks.

## Prerequisites

- Research/exploration phase has identified what needs to be done
- You understand the scope and can break it into independent pieces

## Where to create

- Tasks go in the same numbered story dir as the research task: `.ralph/tasks/01-story-storyname/`
- Story directories and task files must both use two-digit ordering prefixes.
- Tasks start at `01` inside each story.
- Use descriptive slugs that reflect the goal: `01-task-convert-config-parsing.md`

## Task file format

Every task must be manually verified with concrete evidence that the feature/functionality/task works.
Useful targeted checks may be included when they directly prove the requested behavior, and the task must require manual verification with real calls, commands, logs, screenshots, external service status, or equivalent evidence.
Do not accept shallow checkbox completion, code inspection only, or "it should work" reasoning as verification.

```markdown
## Task: [Clear Goal Description] <status>not_started</status> <passes>false</passes>

<description>
Must be manually verified with concrete evidence that it works.


**Goal:** [multiple sentences stating the objective]
[also include the higher order goal of this task]
[Complete Discussed goal, things in-scope, out of scope, all decisions made]
[Must include ALL things discussed within the chat/context. You must verify that the task alone, stands on its own, without having any additional context beside this repo only (e.g. you can refer to files)]


</description>


<acceptance_criteria>
- [ ] Manual verification was performed with concrete calls, commands, logs, screenshots, external service status, or other evidence proving the feature/functionality/task works.
- [ ] The verification evidence is recorded in the task or linked artifact.
- [ ] Completion is not based only on a shallow checkbox, assumption, or code inspection.
</acceptance_criteria>
```
