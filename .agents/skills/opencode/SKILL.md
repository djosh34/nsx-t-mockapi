---
name: opencode
description: Use opencode to query a model from the opencode-go provider. Trigger when the user asks to use opencode, ask an opencode model, query a model through opencode, or run a one-off model prompt with opencode.
---

# Opencode

Use `opencode run` for one-off model queries.

Always use a model whose name starts with `opencode-go/`. Do not use models from other providers for this skill.

## List Allowed Models

```bash
opencode models | grep '^opencode-go/'
```

## Prompt Argument

```bash
opencode run -m opencode-go/qwen3.5-plus "Reply with a one-sentence answer: what is 2 + 2?"
```

## Stdin Prompt

```bash
printf 'Reply with a one-sentence answer: what is 2 + 2?\n' | opencode run -m opencode-go/qwen3.5-plus
```

## Notes

- Prefer the simplest prompt that answers the user's question.
- If a specific `opencode-go/` model is not requested, pick one from the current `opencode models` output.
- Keep command output visible to the user when they asked to see it; otherwise summarize the model's answer.
