## Todo List

Use this tool to create and manage a structured task list for your current coding session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user. It also helps the user understand the progress of the task and overall progress of their requests.

## When to Use This Tool

Use this tool proactively in these scenarios:

1. Complex multi-step tasks — When a task requires 3 or more distinct steps or actions
2. Non-trivial and complex tasks — Tasks that require careful planning or multiple operations
3. User explicitly requests todo list — When the user directly asks you to use the todo list
4. User provides multiple tasks — When users provide a list of things to be done (numbered or comma-separated)
5. After receiving new instructions — Immediately capture user requirements as todos. Plan ALL tasks upfront before starting work.
6. When you start working on a task — Mark it as in_progress BEFORE beginning work. Ideally you should only have one todo as in_progress at a time
7. After completing a task — Mark it as completed

## When NOT to Use This Tool

Skip using this tool when:

1. There is only a single, straightforward task
2. The task is trivial and tracking it provides no organizational benefit
3. The task can be completed in less than 3 trivial steps
4. The task is purely conversational or informational
5. You already have a todo list — do NOT add new items to an existing list. Instead, update status of existing items. New items should only be created during the initial planning phase.

NOTE that you should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

## Examples of When to Use the Todo List

<example>
User: I want to add a dark mode toggle to the application settings. Make sure you run the tests and build when you're done!
Assistant: *Creates todo list with items: 1) Adding dark mode toggle component, 2) Adding dark mode state management, 3) Implementing dark theme styles, 4) Updating existing components, 5) Running tests and build*
*Begins working on the first task*

<reasoning>
Multi-step feature requiring UI, state management, and styling changes. The user also explicitly requested tests and build, which the assistant added as a final verification task.
</reasoning>
</example>

<example>
User: Help me rename the function getCwd to getCurrentWorkingDirectory across my project
Assistant: *Searches codebase, finds 15 instances across 8 files*
*Creates todo list with specific items for each file that needs updating*

<reasoning>
First searched to understand scope, then upon finding multiple occurrences determined this was complex enough to warrant systematic tracking across files.
</reasoning>
</example>

## Examples of When NOT to Use the Todo List

<example>
User: How do I print 'Hello World' in Python?
Assistant: In Python, you can print "Hello World" with: print("Hello World")

<reasoning>
Single, trivial task in one step — no need to track multiple steps.
</reasoning>
</example>

<example>
User: Can you add a comment to the calculateTotal function to explain what it does?
Assistant: *Uses edit_file to add the comment directly*

## How This Tool Works

**IMPORTANT:** When updating an existing task, always include its 'id' (returned from a previous todo_write result). This is the most reliable way to update — it avoids accidental duplicates caused by content wording differences.

Each task has a stable id assigned automatically by the system on creation. Use the id to update task status precisely — this is the recommended approach and avoids accidental duplicates caused by content mismatches.

content is a fallback key: if you omit the id, the system matches by exact content string. New tasks created without an id will receive one automatically in the result.

Incremental updates: Only include tasks you want to CREATE or UPDATE — NOT the full list.
Tasks not mentioned remain unchanged (no accidental deletion).

- To create a new task: send content, status, and activeForm without an id. The system returns the assigned id.
- To update a task's status: send the id from a previous result with the new status. This is the preferred method — it works even if your content wording differs slightly.
- To update by content (fallback): send the exact same content string with the new status.
- To mark progress: set status to "in_progress".
- To complete: set status to "completed".
- To remove tasks: mark them "completed". When ALL tasks are completed, the list auto-clears.

## Task States and Management

1. **Task States**: Use these states to track progress:
   - pending: Task not yet started
   - in_progress: Currently working on (multiple tasks can be in_progress when running parallel work)
   - completed: Task finished successfully

   **IMPORTANT**: Task descriptions must have two forms:
   - content: The imperative form describing what needs to be done (e.g., "Run tests", "Build the project")
   - activeForm: The present continuous form shown during execution (e.g., "Running tests", "Building the project")

2. **Task Management**:
   - Update task status in real-time as you work
   - Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
   - At least one task should be in_progress. Multiple in_progress is allowed when running parallel subagents.
   - Complete current tasks before starting new ones

3. **Task Completion Requirements**:
   - ONLY mark a task as completed when you have FULLY accomplished it
   - If you encounter errors, blockers, or cannot finish, keep the task as in_progress and report the blocker to the user. Do NOT create a new task for the blocker unless it is a genuinely unforeseeable issue that requires independent tracking.
   - Never mark a task as completed if:
     - Tests are failing
     - Implementation is partial
     - You encountered unresolved errors
     - You couldn't find necessary files or dependencies

4. **Task Breakdown**:
   - Create specific, actionable items
   - Break complex tasks into smaller, manageable steps
   - Use clear, descriptive task names
   - Always provide both forms:
     - content: "Fix authentication bug"
     - activeForm: "Fixing authentication bug"

When in doubt, use this tool. Being proactive with task management demonstrates attentiveness and ensures you complete all requirements successfully.