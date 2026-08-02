# Waveloom 工具描述

> 本文档记录 14 个内置工具通过 function calling 发送给 LLM 的 `Description()` 文本与参数定义。
> 内容与 `pkg/tool/*.go` 及 `pkg/subagent/agent.go` 中的实现一致,改动工具时请同步更新。
> 完整 JSON Schema 以各工具源码中的 `Schema()` 返回值为准,本文档为人工可读摘要。
>
> 详细使用规则(行为约束、策略)通过 `Prompt()` 注入 system prompt,不在 `Description()` 中。

## 概述

| 工具 | 并发安全 | 类别 | 说明 |
|------|:--:|------|------|
| `read` | ✅ | 文件 | 读取文件内容(带行号,支持 pattern 定位、outline 符号索引) |
| `write` | ❌ | 文件 | 创建新文件或覆盖已有文件(自动创建父目录) |
| `edit` | ❌ | 文件 | 使用 unified diff hunk 编辑文件(支持多文件多 hunk) |
| `bash` | ❌ | 命令 | 在子进程中执行 Shell 命令(支持后台任务) |
| `web_fetch` | ✅ | Web | 获取 URL 内容(HTML 转纯文本) |
| `web_search` | ✅ | Web | 搜索引擎查询(DDG 默认 + Brave 可选) |
| `ask_user_question` | ❌ | 交互 | 向用户发起选择题决策 |
| `skill` | ❌ | 系统 | 调用用户定义的 Skill |
| `enter_plan_mode` | ❌ | Plan | 进入先规划后执行的 Plan 模式 |
| `exit_plan_mode` | ❌ | Plan | 提交 Plan 审批,通过后恢复正常模式 |
| `agent` | ✅ | 子代理 | 委派复杂任务给子 agent(Fork / Cold / Explore / Evaluate / Verification) |
| `kill_background_task` | ✅ | 任务 | 按任务 ID 终止后台运行的任务 |
| `todo_create` | ❌ | 任务 | 创建待办任务 |
| `todo_update` | ❌ | 任务 | 更新任务状态(in_progress / completed / pending) |

> 并发安全(`ConcurrentSafe() true`):标记为并发的工具可由 Agent Loop 在同一轮中与其他读操作并行执行。
>
> `bash_subagent` 是 `bash` 的子代理只读变体,不直接暴露给用户,仅子代理内部使用。

---

## read

```
Read a file with line numbers for editing. Rules: see system prompt ## File Operations.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `file_path` | string | ✅ | 文件路径(绝对,或相对 workspace root / working_dir) |
| `pattern` | string | | 定位子串,输出以首个匹配为中心 |
| `context_lines` | integer | | 匹配上下文行数(默认 5,最大 50) |
| `offset` | integer | | 无 pattern:起始行(0 起);有 pattern:匹配索引 |
| `limit` | integer | | 最大返回行数(0 = 不限) |
| `working_dir` | string | | 相对路径解析基准目录 |
| `outline` | boolean | | 返回符号索引而非文件内容(LSP symbols + regex fallback) |

## write

```
Create a new file or overwrite an existing file. Creates parent directories automatically.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `file_path` | string | ✅ | 目标文件路径 |
| `content` | string | ✅ | 完整文件内容 |
| `working_dir` | string | | 相对路径解析基准目录 |

## edit

```
Edit files using unified diff hunks. Supports multi-file, multi-hunk patches.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `file_path` | string | ✅ | 默认目标文件(单文件编辑) |
| `hunk` | string | ✅ | unified diff hunk(信封格式,支持 `*** Update File:` 多文件头) |

> 匹配容错 4 层:exact → 忽略行尾空白 → 忽略首尾空白 → Unicode 归一化。失败后重读再试,禁止用 shell 命令改文件。

## bash

```
Execute a shell command in a subprocess. Rules: see system prompt ## Tool Selection.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `command` | string | ✅ | 要执行的 Shell 命令 |
| `working_dir` | string | | 执行目录(不设则默认 workspace root) |
| `timeout_ms` | integer | | 超时(默认 300000,最大 1800000) |
| `run_in_background` | boolean | | 后台运行,立即返回任务 ID + 日志路径 |

> 命令经 mvdan.cc/sh AST 解析与危险分级(RiskNone/Low/Medium/High),高危命令硬拦截。

## web_fetch

```
Fetch content from a URL and return text (HTML stripped to plain text). Only text/*, JSON, XML, JavaScript. Rules: see system prompt ## Information Sources.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `url` | string | ✅ | http/https URL |
| `max_size` | integer | | 最大响应字节数(默认 1MB,最大 5MB) |
| `timeout_ms` | integer | | 超时(默认 30000,最大 120000) |

## web_search

```
Search the web and return a list of results (title, URL, snippet). Backends: DuckDuckGo (default) or Brave Search. Rules: see system prompt ## Information Sources.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `query` | string | ✅ | 搜索关键词 |
| `max_results` | integer | | 结果数(默认 10,最大 20) |
| `timeout_ms` | integer | | 超时(默认 45000,最大 120000) |

## ask_user_question

```
Ask the user one or more multiple-choice questions to gather preferences, clarify ambiguity, or make decisions during execution.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `questions` | array | ✅ | 问题列表(1-4 个) |
| `questions[].question` | string | ✅ | 完整问题文本 |
| `questions[].header` | string | | 短标签(最多 12 字符) |
| `questions[].options` | array | ✅ | 选项(2-4 个,label 唯一) |
| `questions[].options[].label` | string | ✅ | 选项显示文本 |
| `questions[].options[].description` | string | | 选项解释 |

> 用户始终可选 "Other" 提供自定义输入;`multiSelect: true` 允许多选。

## enter_plan_mode

```
Enter plan mode for complex tasks. Rules: see system prompt ## Plan Mode. Exit with exit_plan_mode.
```

无参数。

> 仅用于复杂特性/重构(3+ 文件、架构决策、多方案)。plan 模式禁止写源文件,审批后经 exit_plan_mode 恢复。

## exit_plan_mode

```
Exit plan mode when your plan is complete and ready for user approval.
```

无参数。

## skill

```
Invoke a user-defined skill. Use this when a task matches an available skill's description. Call with skill name and optional arguments.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `name` | string | ✅ | Skill 名称 |
| `arguments` | string | | 传给 Skill 的参数 |

## agent

```
Launch a subagent to handle complex, multi-step tasks. See ## Agent Tool in the system prompt for agent types, when to fork vs cold, and prompt-writing guidance.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `description` | string | ✅ | 3-5 词任务标签 |
| `prompt` | string | ✅ | 子代理任务指令 |
| `subagent_type` | string | | 省略 = fork;`Explore` / `evaluate` / `verification` = cold |
| `model` | string | | 模型覆盖(pro / flash) |

## kill_background_task

```
Kill a running background task by its task ID. Rules: see system prompt ## Tool Selection.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `task_id` | string | ✅ | 后台任务 ID |

## todo_create

```
Create new tasks in the todo list. Tasks are created with status 'pending'. Use todo_update to change status.
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `todos` | array | ✅ | 任务列表 |
| `todos[].content` | string | ✅ | 任务内容(祈使句) |
| `todos[].description` | string | | 补充细节 |

## todo_update

```
Update task status in the todo list. Use to mark tasks as in_progress (start working), completed (done), or pending (revert).
```

| 参数 | 类型 | 必需 | 说明 |
|------|------|:--:|------|
| `todos` | array | ✅ | 状态更新列表 |
| `todos[].id` | string | ✅ | 任务 ID(字符串,如 "1") |
| `todos[].status` | string | ✅ | `pending` / `in_progress` / `completed` |

> 仅允许一个任务处于 in_progress;全部完成时自动清理。
