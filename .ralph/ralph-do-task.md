## Progress Log

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

# Your task
- [ ] read .ralph/current_task.txt
    - [ ] this file will contain a path to a task
    - [ ] from this on we will call that [task name].md
- [ ] read the [task name].md file from the path specified
- [ ] complete the work like this:
    - [ ] If you read nothing/ TO BE VERIFIED, follow these steps:
        - [ ] Create a plan first in separate file in plans subdir .ralph/tasks/[story]/[task]_plans/[plan].md
        - [ ] Make sure this plan uses $improve-code-boundaries skill to have better code and you MUST use $tdd skill (except when excepted) to plan and execute
        - [ ] Replace end of plan with 'TO BE VERIFIED' and add path to the plan
        - [ ] Once your happy with plan/interface design/types/enums, replace end of plan with 'NOW EXECUTE'
        - [ ] QUIT IMMEDIATELY!
    - [ ] When you read 'NOW EXECUTE':
        - [ ] you must read $tdd skill (if applicable), it is not only about tdd, but also about the whole coding mindset of Red-Green tdd and code quality
        - [ ] execute the plan as written, fix the compile errors, and tick off the boxes when you do them.
        - [ ] If at any point you find that the design was not correct, and types are in need of change, switch 'NOW EXECUTE' back to 'TO BE VERIFIED'
        - [ ] If switched, QUIT IMMEDIATELY, else continue until checks pass (only if design is still right)

- [ ] you are really done if and only if ALL of these pass 100%:
    - [ ] `make check`
    - [ ] `make test`
    - [ ] test-coverage of new code must be 80%+, and `make test-coverage` also 80%+
    - [ ] Final check if code isn't more muddy using improve-code-boundaries skill, otherwise resolve it

- [ ] only when you're done, and all checks pass:
    - [ ] set in [task name].md
        - [ ] set `<passes>true</passes>`
    - [ ] CRUCIAL: run `/bin/bash .ralph/task_switch.sh` to indicate that you want to switch task.
      This can be when you're done or just when you want to switch (e.g. for going to subtask)
    - [ ] commit with: `task finished [task name]: [insert text]`
        - [ ] include summary of what was done in commit message (evidence for tests completing, challenges faced during
          implementation)
        - [ ] You must add all files. Make sure to properly gitignore where it makes sense. You MUST also add/commit all files (also in .ralph) even if you didn't create/update them
    - [ ] QUIT IMMEDIATELY


TDD exceptions: (VERY IMPORTANT!!)

TDD is not allowed for non code related tasks. E.g. never assert that some text string is contained in dockerfile/workflow/or any other file in a test.
Instead, for instance when creating dockerfile or workflow, you MUST test by executing the docker build or workflow, and never using brittle and useless string comparisons.
Also do not 'test', namechanges or any other string manipulation within rust tests. These give false confidence and make the code very bad.