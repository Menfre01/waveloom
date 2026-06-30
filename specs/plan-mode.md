# Plan Mode 组件规格书

## 组件定位

Plan Mode 是先规划后执行的二阶段工作流系统。LLM 在编码前通过 `enter_plan_mode` 进入只读规划模式，产出 plan 文件，用户审核通过 `exit_plan_mode` 后方可写代码。

对应 Claude Code 的 `EnterPlanModeTool` + `ExitPlanModeV2Tool`（`extracted_sources/EnterPlanModeTool/` + `ExitPlanModeTool/`）。

核心职责：
1. 状态机管理 — normal / plan 两种模式切换
2. Plan 模式下的写操作拦截 — write_file, edit_file, shell（写命令）自动拒绝
3. Plan 文件生命周期 — enter 时指定路径，planning 阶段 LLM 写入，exit 时提交审批
4. 用户审批门控 — plan 必须经过用户确认后才能执行

## 参考来源

- Claude Code: `extracted_sources/EnterPlanModeTool/EnterPlanModeTool.ts` + `prompt.ts`
- Claude Code: `extracted_sources/ExitPlanModeTool/ExitPlanModeV2Tool.ts` + `prompt.ts`
- Claude Code 的 plan mode 权限过滤逻辑（`permissionSetup.ts`, `planModeV2.ts`）

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 状态存储 | Agent Loop 内存变量 | 单 session 内有效，不需要持久化 |
| Plan 文件位置 | `Config.PlanFile`（由 CLI 参数或默认路径指定） | 对齐 Claude Code 的 `PLAN_ROOT` 约定 |
| 写操作拦截层 | Permission Guard | 复用现有权限框架，plan 模式下写工具返回 deny |
| 审批流程 | ExitPlanMode 触发 TurnEvent → TUI 展示 plan → 用户 approve/reject | 对齐 AskUserQuestion 的阻塞等待模式 |
| Plan 内容传递 | LLM 写入文件，exit 时工具从文件读取 | 避免超长 tool result（plan 内容可能很大） |
| shell 拦截粒度 | 拦截并拒绝 | plan 模式下 shell 允许只读命令（如 `go doc`），但写命令（如 `make build`）由权限规则处理 |
| 取消 Plan | reject 后回到 plan 模式，不回到 normal | LLM 根据反馈修改 plan 后重新 exit，用户也可直接说 "cancel plan" |
| 并发安全 | 是 | 状态切换在 Loop 主 goroutine 内，无竞态 |

## 状态机

```
NORMAL ──EnterPlanMode──▶ PLAN ──ExitPlanMode(approved)──▶ NORMAL
   ▲                        │
   │                        ├── ExitPlanMode(rejected) ──▶ PLAN (继续修改)
   │                        │
   └── user cancel ─────────┘
```

| 状态 | 允许操作 | 禁止操作 |
|------|----------|----------|
| `NORMAL` | 所有工具 | — |
| `PLAN` | read_file, grep, search_file, ls, lsp_*, web_fetch, ask_user_question, write_file（仅 plan 文件）, exit_plan_mode | write_file（非 plan 文件）, edit_file, shell |

### 补充说明

- **shell 在 plan 模式下的处理**：权限系统根据命令内容判定。`go doc`, `git log` 等只读命令放行；`go build`, `make`, `npm install` 等写命令拒绝。或简化处理：plan 模式下 shell 一律拒绝，LLM 通过 `web_fetch` 和 `lsp_*` 获取信息。

- **write_file 例外**：plan 模式下允许写入 plan 文件（`Config.PlanFile` 指定的路径），LLM 通过 write_file 产出 plan 内容。

---

## enter_plan_mode 工具

### 输入

无参数。

### 输出

```json
{
  "message": "Entered plan mode. Explore the codebase and write your plan to <plan_file>. DO NOT write or edit any source files. Use exit_plan_mode when ready for approval."
}
```

### Description（Prompt 引导）

```
Enter plan mode for complex tasks requiring exploration and design before coding.
Use this proactively when:
- Implementing new features with architectural ambiguity
- Multiple valid approaches exist and the choice matters
- Changes affect 3+ files or restructure existing behavior
- User preferences matter for the implementation approach

Skip plan mode for:
- Single-line or few-line fixes (typos, obvious bugs)
- Tasks with very specific, detailed instructions from the user
- Adding a single function with clear requirements

In plan mode you CAN: read/search/explore code, ask questions, write to the plan file.
In plan mode you CANNOT: edit/write source files, run build/install commands.

Exit with exit_plan_mode when your plan is complete and ready for review.
```

### 权限

- `requiresUserInteraction() → true`：用户必须同意进入 plan 模式
- 拒绝 → 返回 `user_declined` 错误，LLM 回到 normal 模式继续

---

## exit_plan_mode 工具

### 输入

无参数。Plan 内容由 LLM 提前写入 plan 文件，工具从文件读取。

### 输出

```json
{
  "plan": "<plan file content>",
  "message": "Plan submitted for review. The user will review and approve or request changes."
}
```

### Description（Prompt 引导）

```
Exit plan mode when your plan is complete and ready for user approval.

## Before Using This Tool
- Write your plan to the plan file first (use write_file with the path in the system message)
- Ensure your plan is complete and unambiguous
- Resolve any open questions with ask_user_question BEFORE calling exit_plan_mode

## How This Tool Works
- This tool reads the plan from the file you wrote
- The user will see the plan content and approve or request changes
- If approved, you return to normal mode and can begin implementation
- If rejected, you stay in plan mode to revise the plan

Do NOT use ask_user_question to ask "is my plan ready?" or "should I proceed?" — 
that's exactly what this tool does.
```

### 权限

- `requiresUserInteraction() → true`：用户审批 plan
- 通过 → plan 内容提交，回到 normal 模式
- 拒绝 → 返回 `user_declined` 错误（含用户反馈），LLM 留在 plan 模式修改

---

## 权限集成

### Plan 模式下的工具过滤

Permission Guard 在 `checkPermission` 中新增 plan 模式感知：

```go
func (g *Guard) checkPermission(toolName string, mode agentloop.Mode) PermissionDecision {
    if mode == agentloop.ModePlan {
        switch toolName {
        case "write_file":
            // 允许写入 plan 文件，拒绝其他路径
        case "edit_file":
            return deny("edit_file is blocked in plan mode")
        case "shell":
            // 拦截 shell（或允许只读命令白名单）
        }
    }
    // 正常权限检查
}
```

简化方案：plan 模式下，Loop 层直接过滤工具列表——在发送给 LLM 的 `Tools` 列表中排除 `write_file`、`edit_file`、`shell`。LLM 看不到这些工具就不会调用，无需 Guard 拦截。`write_file` 到 plan 文件通过特殊路径处理（如 plan 文件路径白名单）。

### 推荐方案：Loop 层过滤

- Plan 模式下，`Loop.buildTools()` 只返回只读工具 + `exit_plan_mode` + `ask_user_question`
- `write_file` 不在列表中，LLM 无法调用 → 无权限检查开销
- 简单可靠，无需修改 Guard

---

## Agent Loop 变更

### types.go

```go
// Mode 表示当前 Agent 运行模式。
type Mode int

const (
    ModeNormal Mode = iota
    ModePlan
)

// Config 增加字段
type Config struct {
    // ... 现有字段 ...
    Mode     Mode   // 当前模式
    PlanFile string // plan 文件路径（plan 模式下 LLM 可写入的唯一文件）
}

// TurnEvent 新增类型
type PlanModeEnter struct {
    Turn int
}

type PlanModeExit struct {
    Turn    int
    Plan    string // plan 文件内容
    Message string // 审批结果消息
}

// UserResponder 新增方法
type UserResponder interface {
    // ... 现有方法 ...
    ApprovePlan(ctx context.Context, plan string) (approved bool, feedback string, err error)
}
```

### loop.go 变更

```go
func (l *Loop) Run() <-chan TurnEvent {
    // ...
    for {
        tools := l.buildTools() // plan 模式下过滤掉写工具
        resp := l.callLLM(tools)
        // 处理工具调用 ...
        if toolName == "enter_plan_mode" {
            l.mode = ModePlan
            emit(PlanModeEnter{})
        }
        if toolName == "exit_plan_mode" {
            approved, feedback := l.userResponder.ApprovePlan(ctx, plan)
            if approved {
                l.mode = ModeNormal
                emit(PlanModeExit{Plan: plan, Message: "approved"})
            } else {
                emit(PlanModeExit{Message: "rejected: " + feedback})
            }
        }
    }
}

func (l *Loop) buildTools() []ToolSpec {
    if l.mode == ModePlan {
        return planModeTools // read_file, grep, search_file, ls, lsp_*, web_fetch, ask_user_question, exit_plan_mode
    }
    return allTools
}
```

---

## 执行流程

### Enter Plan Mode

```
┌──────────┐   enter_plan_mode   ┌──────────┐   PlanModeEnter    ┌──────────┐
│   LLM    │ ──────────────────▶ │   Loop   │ ────────────────▶  │   TUI    │
│          │                     │          │                    │          │
│          │                     │  (阻塞)  │  UserResponder     │  "进入   │
│          │                     │ ◀────────│  .EnterPlan()     │  规划模式?"│
│          │   tool_result       │          │                    │          │
│          │ ◀────────────────── │          │                    │          │
└──────────┘                     └──────────┘                    └──────────┘
```

1. LLM 调用 `enter_plan_mode`
2. Loop 发送 `TurnEvent{Type: PlanModeEnter}`，阻塞等待 `UserResponder.EnterPlan()`
3. TUI 渲染确认提示（"是否进入规划模式？"）
4. 用户确认 → Loop 切换 `mode = ModePlan`，返回 tool_result
5. 之后 LLM 只能看到只读工具列表

### Exit Plan Mode

```
┌──────────┐   exit_plan_mode    ┌──────────┐   PlanModeExit     ┌──────────┐
│   LLM    │ ──────────────────▶ │   Loop   │ ────────────────▶  │   TUI    │
│          │                     │          │                    │          │
│          │                     │  (阻塞)  │  UserResponder     │  展示     │
│          │                     │ ◀────────│  .ApprovePlan()   │  plan     │
│          │   approve/reject    │          │                    │  审批     │
│          │ ◀────────────────── │          │                    │          │
└──────────┘                     └──────────┘                    └──────────┘
```

1. LLM 先通过 `write_file` 写入 plan 文件（plan 模式下允许）
2. LLM 调用 `exit_plan_mode`
3. Loop 读取 plan 文件内容，发送 `TurnEvent{Type: PlanModeExit, Plan: content}`
4. TUI 渲染 plan 内容（Markdown），用户 approve / reject（可附反馈）
5. Approve → Loop 切换 `mode = ModeNormal`，工具列表恢复完整
6. Reject → Loop 留在 plan 模式，返回 `user_declined` + 反馈给 LLM

---

## 实现清单

| 文件 | 操作 | 内容 |
|------|------|------|
| `pkg/tool/enter_plan_mode.go` | **新增** | TypedTool 实现 + Schema + Description |
| `pkg/tool/exit_plan_mode.go` | **新增** | TypedTool 实现 — 读取 plan 文件，提交审批 |
| `pkg/agentloop/types.go` | **修改** | 增加 `Mode` 类型、`PlanModeEnter`/`PlanModeExit` 事件、`Config.PlanFile` |
| `pkg/agentloop/types.go` | **修改** | `UserResponder` 增加 `EnterPlan()` / `ApprovePlan()` 方法 |
| `pkg/agentloop/loop.go` | **修改** | 状态机 `mode`、`buildTools()` 按模式过滤、plan 事件推送 |
| `pkg/agentloop/execute.go` | **修改** | `enter_plan_mode` / `exit_plan_mode` 特殊处理逻辑 |
| `cmd/waveloom/tui.go` | **修改** | 渲染 enter plan 确认 + exit plan 审批界面 + mode 状态指示器 |
| `cmd/waveloom/main.go` | **修改** | 增加 `--plan-file` flag，初始化 `Config.PlanFile` |

---

## 测试策略

| 层级 | 测试内容 | 方法 |
|------|----------|------|
| 单元 | Schema 校验 | `go test ./pkg/tool/ -run TestEnterPlanMode` |
| 单元 | exit_plan_mode 文件读取 | mock 文件系统 |
| 单元 | Loop.buildTools() plan 模式过滤 | `go test ./pkg/agentloop/ -run TestPlanModeToolFilter` |
| 单元 | 状态机切换 enter → exit → approve/reject | mock UserResponder |
| 集成 | 完整 plan 模式流程 | cold agent 启动 → enter → explore → write plan → exit → approve → implement |
| TUI | 审批界面渲染（手动验收） | cold agent review |

---

## 验收标准

1. LLM 调用 `enter_plan_mode` → TUI 确认 → 进入 plan 模式
2. Plan 模式下 LLM 的可用工具列表只含只读工具 + exit_plan_mode + ask_user_question
3. Plan 模式下 LLM 尝试 write_file 到非 plan 文件 → 工具不可见，不会发生
4. LLM 通过 write_file 写入 plan 文件（plan 模式下允许的例外）
5. LLM 调用 `exit_plan_mode` → plan 内容呈现给用户 → 用户通过/拒绝
6. 用户通过 → 回到 normal 模式，LLM 可正常写代码
7. 用户拒绝 → 留在 plan 模式，LLM 收到反馈，修改 plan 后重新 exit
8. TUI 有明确的 plan mode 状态指示器（如 header 颜色变化或 `[PLAN]` 标签）
