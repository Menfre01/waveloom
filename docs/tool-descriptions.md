# Waveloom 工具描述

> 本文档记录了 14 个内置工具通过 function calling 发送给 LLM 的完整内容:
> `Description()` 文本 + `Schema()` JSON 参数定义。
> 这些内容与 `pkg/tool/*.go` 中的实现严格一致,改动时请同步更新。
>
> 详细使用规则(行为约束、策略)通过 `Prompt()` 注入 system prompt,不在 `Description()` 中。

## 概述

| 工具 | 并发安全 | 类别 | 说明 |
|------|:--:|------|------|
| `read` | ✅ | 文件 | 读取文件内容(带 TAG 和行号,支持 pattern 定位) |
| `write` | ❌ | 文件 | 创建或覆盖文件 |
| `edit` | ❌ | 文件 | 基于 hashline 格式的精确编辑(SWAP/DEL/INS/MV/REM) |
| `bash` | ❌ | 命令 | 执行 Shell 命令(支持后台任务) |
| `web_fetch` | ✅ | Web | 获取 URL 内容 |
| `web_search` | ✅ | Web | 搜索引擎查询(DDG 默认 + Brave 可选) |
| `ask_user_question` | ❌ | 交互 | 向用户发起选择题决策 |
| `skill` | ❌ | 系统 | 调用用户定义的 Skill |
| `enter_plan_mode` | ❌ | Plan | 进入先规划后执行的 Plan 模式 |
| `exit_plan_mode` | ❌ | Plan | 提交 Plan 审批,通过后恢复正常模式 |
| `agent` | ✅ | 子代理 | 委派复杂任务给子 agent(Fork / Cold / Explore / Evaluate / Verification / Advisor) |
| `kill_background_task` | ✅ | 任务 | 终止后台运行的任务 |
| `todo_create` | ❌ | 任务 | 创建待办任务 |
| `todo_update` | ❌ | 任务 | 更新任务状态(in_progress / completed) |

> 并发安全(✅ = `ConcurrentSafe() true`):标记为并发的工具可由 Agent Loop 在同一轮中与其他读操作并行执行。
>
> `bash_subagent` 是 `bash` 的子代理只读变体(`AllowBg: false`),不直接暴露给用户,仅子代理内部使用。

---

## read

```
Read a file with TAG and line numbers for hash-anchored editing. Rules: see system prompt ## Read File (Hashline).
```

```json
{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') first to explore directories. Paths without a file extension are likely directories."
    },
    "offset": {
      "type": "integer",
      "description": "Without pattern: starting line number (0-based, 0 = first line, optional). With pattern: match index (0-based) to page through matches."
    },
    "limit": {
      "type": "integer",
      "description": "Number of lines to read (optional, default: all)"
    },
    "pattern": {
      "type": "string",
      "description": "Optional substring to locate in the file. When present, the output centers on the first match ±context_lines, eliminating a separate grep call. Use offset/limit to page through additional matches."
    },
    "context_lines": {
      "type": "integer",
      "description": "Lines of context above and below each match (default: 5, max: 50). 0 is treated as default. Only meaningful with pattern. For match-line-only, use limit=1."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path"]
}
```

## write

```
Create a new file or overwrite an existing file. Creates parent directories automatically.
```

```json
{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') to explore directories first."
    },
    "content": {
      "type": "string",
      "description": "File content to write"
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path", "content"]
}
```

## edit

```
Edit files using hash-anchored patches. Rules: see system prompt ## Edit File (Hashline).
```

```json
{
  "type": "object",
  "properties": {
    "patch": {
      "type": "string",
      "description": "Hashline format patch text. Must start with *** Begin Patch and end with *** End Patch."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["patch"]
}
```

## bash

```
Execute a shell command in a subprocess. Rules: see system prompt ## Shell Usage.
```

```json
{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Shell command to execute. Unix/macOS uses bash -c (sh fallback), Windows uses Git Bash (bash -c)."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (default: 300000, max: 1800000)"
    },
    "run_in_background": {
      "type": "boolean",
      "description": "Set to true to run this command in the background. The tool returns immediately with a task ID and log path. Use read to check progress. The next turn will receive a completion notification.",
      "default": false
    }
  },
  "required": ["command"]
}
```

> `bash_subagent` 变体不含 `run_in_background` 参数(仅 `command` + `working_dir` + `timeout_ms`)。

## web_fetch

```
Fetch content from a URL and return text (HTML stripped to plain text). Only text/*, JSON, XML, JavaScript. Rules: see system prompt ## Web Fetch.
```

```json
{
  "type": "object",
  "properties": {
    "url": {
      "type": "string",
      "description": "URL to fetch (http/https only)"
    },
    "max_size": {
      "type": "integer",
      "description": "Maximum response size in bytes (optional, default: 1MB, max: 5MB)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (optional, default: 30000, max: 120000)"
    }
  },
  "required": ["url"]
}
```

## web_search

```
Search the web and return a list of results (title, URL, snippet). Backends: DuckDuckGo (default) or Brave Search. Rules: see system prompt ## Web Search.
```

```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Search query — keywords, natural language, or technical terms",
      "minLength": 1
    },
    "max_results": {
      "type": "integer",
      "description": "Maximum number of results to return (default: 10, max: 20)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (optional, default: 45000, max: 120000)"
    }
  },
  "required": ["query"]
}
```

## ask_user_question

```
Ask the user one or more multiple-choice questions to gather preferences, clarify ambiguity, or make decisions during execution.
```

```json
{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {
        "type": "object",
        "properties": {
          "question": {
            "type": "string",
            "description": "The complete question to ask the user. Should be clear, specific, and end with a question mark."
          },
          "header": {
            "type": "string",
            "maxLength": 12,
            "description": "Very short label displayed as a chip/tag. Examples: 'Auth method', 'Library', 'Approach'."
          },
          "options": {
            "type": "array",
            "minItems": 2,
            "maxItems": 4,
            "items": {
              "type": "object",
              "properties": {
                "label": {
                  "type": "string",
                  "description": "The display text for this option (1-5 words). Append '(Recommended)' if this is the suggested choice."
                },
                "description": {
                  "type": "string",
                  "description": "Explanation of what this option means or what will happen if chosen."
                }
              },
              "required": ["label", "description"]
            }
          },
          "multiSelect": {
            "type": "boolean",
            "default": false,
            "description": "Set to true to allow multiple selections (for non-mutually-exclusive choices)."
          }
        },
        "required": ["question", "header", "options"]
      }
    }
  },
  "required": ["questions"]
}
```

## skill

```
Invoke a user-defined skill. Use this when a task matches an available skill's description. Call with skill name and optional arguments.
```

```json
{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "The skill name (e.g., 'deploy', 'summarize-changes')"
    },
    "arguments": {
      "type": "string",
      "description": "Optional arguments to pass to the skill"
    }
  },
  "required": ["name"]
}
```

## enter_plan_mode

```
Enter plan mode for complex tasks. Rules: see system prompt ## Plan Mode. Exit with exit_plan_mode.
```

```json
{
  "type": "object",
  "properties": {},
  "required": []
}
```

## exit_plan_mode

```
Exit plan mode when your plan is complete and ready for user approval.
```

```json
{
  "type": "object",
  "properties": {},
  "required": []
}
```

## agent

```
Launch a subagent to handle complex, multi-step tasks. See ## Agent Tool in the system prompt for agent types, when to fork vs cold, and prompt-writing guidance.
```

```json
{
  "type": "object",
  "properties": {
    "subagent_type": {
      "type": "string",
      "description": "Omit to fork (DEFAULT). Set to 'Explore', 'evaluate', 'verification', or 'advisor' for specialized agents. See ## Agent Tool in system prompt for details."
    },
    "description": {
      "type": "string",
      "description": "A short (3-5 word) description of the task"
    },
    "prompt": {
      "type": "string",
      "description": "The task for the subagent to perform"
    },
    "model": {
      "type": "string",
      "enum": ["pro", "flash"],
      "description": "Optional model override. 'pro' = full reasoning (default), 'flash' = faster/cheaper. Omit to use default."
    }
  },
  "required": ["description", "prompt"]
}
```

## kill_background_task

```
Kill a running background task by its task ID. Rules: see system prompt ## Shell Usage.
```

```json
{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "The task ID of the background command to kill. Obtained from the bash tool response or background-task notifications."
    }
  },
  "required": ["task_id"]
}
```

## todo_create

```
Create new tasks in the todo list. Tasks are created with status 'pending'. Use todo_update to change status.
```

```json
{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "content": {
            "type": "string",
            "minLength": 1,
            "description": "Imperative form describing what needs to be done."
          },
          "description": {
            "type": "string",
            "description": "Optional details to help remember what this task involves."
          }
        },
        "required": ["content"]
      }
    }
  },
  "required": ["todos"]
}
```

## todo_update

```
Update task status in the todo list. Use to mark tasks as in_progress (start working) or completed (done).
```

```json
{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string",
            "minLength": 1,
            "description": "ID of the task to update."
          },
          "status": {
            "type": "string",
            "enum": ["in_progress", "completed"],
            "description": "New status. Only ONE task in_progress at a time."
          }
        },
        "required": ["id", "status"]
      }
    }
  },
  "required": ["todos"]
}
```
