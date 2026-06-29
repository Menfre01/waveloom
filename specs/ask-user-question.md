# AskUserQuestion 工具规格书

## 组件定位

AskUserQuestion 是允许 LLM 在执行过程中向用户发起选择题式交互决策的工具。不等同于 inline 文本对话——这是一个**阻塞式工具调用**，Agent Loop 在收到用户回答后才继续执行。

对应 Claude Code 的 `AskUserQuestionTool`（`AskUserQuestionTool/AskUserQuestionTool.tsx` + `prompt.ts`）。

核心职责：
1. 提供结构化选择题界面（单选/多选 + "Other" 自定义输入）
2. 阻塞 Loop 等待用户决策后返回答案
3. 用户拒绝回答时返回可恢复错误，LLM 自行决定替代方案

## 参考来源

- Claude Code: `extracted_sources/AskUserQuestionTool/AskUserQuestionTool.tsx`
- Claude Code: `extracted_sources/AskUserQuestionTool/prompt.ts`
- 核心 schema 来自 claude 实现的 `questionSchema` + `questionOptionSchema`

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 阻塞方式 | TurnEvent + UserResponder | Loop 通过 channel 推送事件，TUI 订阅后渲染选择界面，用户回答后通过 UserResponder 接口返回 |
| 问题数量 | 1-4 个 | 对齐 Claude Code，单次调用可批量提问减少轮次 |
| 选项数量 | 2-4 个（+ 自动 Other） | 防止决策疲劳，Other 由系统自动追加覆盖边界情况 |
| 推荐选项 | label 后缀 "(Recommended)" | 纯文本约定，不破坏 schema，LLM 和用户均可识别 |
| Header 长度 | ≤12 chars | TUI chip 组件宽度限制，对齐 Claude Code 的 `ASK_USER_QUESTION_TOOL_CHIP_WIDTH` |
| 并发安全 | 是 | 阻塞在 UserResponder 等待上，不存在竞态 |
| 只读 | 是 | 不修改文件系统 |
| requiresUserInteraction | true | 权限检查恒返回 ask，无 allow/deny 快捷路径 |

## 组件边界

### 输入
- `context.Context` — 超时信号（默认无超时，CLI 模式下用户可能离开）
- `AskUserQuestionParams` — 问题列表

### 输出
- `*ToolResult` — `{questions, answers}` 或 `user_declined` 错误

### 依赖
- `permission.UserResponder` — 用户交互接口（ask 决策时弹出选择 UI）
- Agent Loop — 通过 `TurnEvent` 推送问题，通过 `UserResponder` 等待答案

### 不纳入本组件
- TUI 渲染逻辑（属于 `cmd/waveloom/tui.go` 职责）
- Plan mode 审批（那是 `exit_plan_mode` 工具的职责，见 `specs/plan-mode.md`）

---

## 接口定义

### TypedTool[AskUserQuestionParams]

```go
type QuestionOption struct {
    Label       string `json:"label"`       // 显示文本，1-5 words
    Description string `json:"description"` // 选项解释
}

type Question struct {
    Question    string           `json:"question"`    // 完整问题，以 ? 结尾
    Header      string           `json:"header"`      // 简短标签，≤12 chars
    Options     []QuestionOption `json:"options"`     // 2-4 项，label 唯一
    MultiSelect bool             `json:"multiSelect"` // 是否多选，默认 false
}

type AskUserQuestionParams struct {
    Questions []Question `json:"questions"` // 1-4 个问题
}
```

### JSON Schema

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

### 输出结构

```go
type AskUserQuestionResult struct {
    Questions []Question        `json:"questions"`
    Answers   map[string]string `json:"answers"`  // question text → answer (multi-select comma-separated)
}
```

实现采用匿名 `map[string]interface{}` 构造 JSON（等价于上述结构），避免在 `execute.go` 中引入对 `tool.Question` 的额外依赖。

---

## 工具 Description（Prompt 引导）

```
Ask the user one or more multiple-choice questions to gather preferences,
clarify ambiguity, or make decisions during execution. Use this tool when
you need to:

1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Get decisions on implementation choices as you work
4. Offer choices to the user about what direction to take

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multiSelect: true to allow multiple answers for a question
- Put "(Recommended)" at the end of the label for the suggested option
- Question texts must be unique; option labels must be unique within each question

Do NOT use this tool to ask "is my plan ready?" or "should I proceed?" —
use exit_plan_mode for plan approval.
```

---

## 执行流程

```
┌──────────┐    ToolCallStart     ┌──────────┐  AskUserQuestionEvent   ┌──────────┐
│   LLM    │ ──────────────────▶  │  Loop    │ ────────────────────▶  │   TUI    │
│          │                      │          │   (非阻塞通知)          │          │
│          │                      │  (阻塞)  │  UserResponder         │  渲染    │
│          │                      │ ◀────────│  .AnswerQuestion()    │  选择界面 │
│          │                      │          │ ◀─── program.Send ─── │          │
│          │    ToolCallResult    │          │ ─── replyCh ────────▶ │          │
│          │ ◀────────────────── │          │                        │          │
└──────────┘                      └──────────┘                        └──────────┘
```

1. LLM 调用 `ask_user_question`，传入 `questions`
2. Loop 检测工具实现 `UserInteractionTool` 接口且 `RequiresUserInteraction() == true`，发送 `AskUserQuestionEvent`（非阻塞 TurnEvent 通知）到事件 channel
3. Loop 调用 `UserResponder.AnswerQuestion(ctx, prompts)` 阻塞等待
4. `AnswerQuestion` 实现（`tuiUserResponder`）通过 `program.Send(questionReqMsg)` 推送问题到 TUI，在 reply channel 上阻塞
5. TUI 订阅 `questionReqMsg`，渲染选择题界面（Bubble Tea + Lipgloss）
6. 用户回答后，TUI 通过 reply channel 返回 `[]QuestionResponse`；拒绝时返回 nil
7. Loop 收到答案，构造 `ToolResult{content: {questions, answers}}`，发送 `ToolCallResult` 事件，继续执行

### 拒绝处理

用户拒绝 → `ToolError{Class: Recoverable, Kind: "user_declined"}` → LLM 自行决定替代方案（不等同于 fatal error，Loop 不终止）

---

## 错误处理

| 场景 | 错误 Kind | Class | LLM 行为 |
|------|-----------|-------|----------|
| 用户拒绝回答 | `user_declined` | Recoverable | 自行决定替代方案 |
| 超时（如配置了 timeout_ms） | `timeout` | Recoverable | 重试或跳过 |
| 无效参数（如选项重复） | `invalid_args` | Recoverable | 修正参数重试 |
| Context 取消 | — | — | 标准取消传播 |

---

## 权限集成

```go
func (t *AskUserQuestion) requiresUserInteraction() bool { return true }
```

- `requiresUserInteraction() → true` 意味着权限检查恒返回 `ask`，无 `allow`/`deny` 路径
- 不对 `user_declined` 做太多解释——LLM 根据上下文自行决定替代路径

---

## TUI 渲染方案（huh 表单）

### 传输层复用

AskUserQuestion 与权限面板共享同一个阻塞式 overlay 通信模式，`tuiUserResponder` 中已实现的核心三要素可直接复用：

```
Loop 阻塞 → program.Send(msg) → TUI 渲染 overlay → 用户交互 → replyCh ← 答案 → Loop 继续
```

| 组件 | 复用？ | 说明 |
|------|--------|------|
| `channel` 阻塞等待 | ✅ 直接复用 | `replyCh := make(chan ..., 1)` + `select` |
| `program.Send` 推送 | ✅ 直接复用 | 新增 `questionReqMsg` 消息类型 |
| TUI `case` 分发 | ✅ 直接复用 | 新增 `overlayQuestion` 状态 |
| `permissionReqMsg` | ❌ | 结构不同，需独立的 `questionReqMsg` |
| `renderPermOverlay` | ❌ | 内容完全不同，需独立渲染函数 |

### huh Form 生命周期

```
questionReqMsg
  → buildQuestionForm()        // 构建 huh.Form（Select / MultiSelect）
  → form.Init()                // 返回初始 Focus/Blink 命令
  → form.Update(msg) 循环       // 用户键盘导航 + 选择
  → form.State == StateCompleted → handleQuestionFormComplete()
     ├─ 选择 Other → buildOtherForm() → Input Form → 提交
     └─ 正常选项 → recordQuestionAnswer() → advanceQuestion()
  → form.State == StateAborted → handleQuestionFormAborted()
     ├─ 在 Other 输入中取消 → 回退选项列表
     └─ 在选项中取消 → user_declined → closeQuestionOverlay()
```

### 表单组件映射

| 功能 | huh 组件 | 配置 |
|------|----------|------|
| 单选 | `huh.Select[string]` | `Options(opts...)` + `Value(&selected)` |
| 多选 | `huh.MultiSelect[string]` | `Options(opts...)` + `Value(&selected)` |
| Other 输入 | `huh.Input` | 独立 Form，`Prompt("> ")` + `CharLimit(200)` |
| 推荐选项 | `huh.NewOption("★ "+label, value)` | ★ 前缀用于视觉区分 |
| 主题 | `huh.ThemeFunc` | 映射 `colorHeaderAccent` / `colorOK` / `colorMuted` 到 `FieldStyles` |

### "Other..." 处理流程

1. "Other..." 作为额外选项追加到 Select/MultiSelect 的 Options 列表，key 为内部常量 `otherOptionKey`
2. 表单提交后，检测结果中是否包含 `otherOptionKey`
3. 若包含：构建独立的 `huh.Input` Form，收集自定义文本
4. 多选时：已选中的常规选项暂存，Other 输入完成后合并为 `["Option A", "Other: custom text"]`
5. 单选时：直接返回 `["Other: custom text"]`

#### 方案对比

| 维度 | 方案 A: 纯 hand-rolled | 方案 B: 引入 `charm.land/huh/v2` |
|------|------------------------|----------------------------------|
| 新依赖 | 无 | `charm.land/huh/v2` |
| 单选 | `list` 直接复用（与权限面板一致） | `huh.Select` |
| 多选 | `list` + checkbox 前缀（`✓` / `○`）+ Space 切换，~150 行 | `huh.MultiSelect`，开箱即用 |
| "Other" 输入 | `list` → `textinput` 动态切换，有状态机复杂度 | `huh.Input`，作为独立 Form |
| Header chip | 纯 lipgloss 手写渲染 | `huh` 的 `Title()` + `Description()` |
| 键盘导航 | 手写 ↑↓ / Enter / Esc / Space | 内置，支持 vim 键位 |
| Accessibility | 需手写 | 内置 `WithAccessible(true)` |
| 样式定制 | 完全控制，与 Waveloom 色板无缝 | 通过 `huh.ThemeFunc` 映射 Waveloom 色板到 `FieldStyles` |
| 预估代码量 | ~300 行（含多选状态机） | ~180 行 |
| 风险 | 多选交互 bug 概率中等 | 样式冲突可能性低（ThemeFunc 全覆盖） |

#### 选型结论

**采用方案 B（charm.land/huh/v2）**。huh 提供标准表单组件（`Select` / `MultiSelect` / `Input`），开箱即用的键盘导航和 accessibility 支持。通过 `huh.ThemeFunc` 将 Waveloom 色板映射到 huh 的 `FieldStyles`，实现视觉统一。"Other" 选项通过独立 `huh.Input` Form 处理，流程清晰。

---

## 实现清单

| 文件 | 操作 | 内容 |
|------|------|------|
| `pkg/tool/ask_user_question.go` | **新增** | TypedTool 实现 + Schema + Description + `RequiresUserInteraction()` |
| `pkg/tool/ask_user_question_test.go` | **新增** | Schema 校验 + 参数合法性 + Wrap/Register 测试 |
| `pkg/tool/tool.go` | **修改** | 新增 `UserInteractionTool` 可选接口（`RequiresUserInteraction() bool`） |
| `pkg/agentloop/types.go` | **修改** | 新增 `AskUserQuestionEvent` TurnEvent；`QuestionPrompt` 等类型别名指向 `permission` |
| `pkg/agentloop/execute.go` | **修改** | 检测 `UserInteractionTool` → 发送事件 → 调用 `UserResponder.AnswerQuestion()` → 填充 answers |
| `pkg/permission/types.go` | **修改** | 新增 `QuestionPrompt`/`QuestionResponse` 类型；`UserResponder` 接口增加 `AnswerQuestion` 方法 |
| `pkg/permission/guard.go` | **修改** | `ExtractPattern` 增加 `ask_user_question` case（返回空字符串，无内容级 pattern） |
| `cmd/waveloom/tui.go` | **修改** | `tuiUserResponder.AnswerQuestion()` 实现；`questionReqMsg` 处理；`themeWaveloom()` huh Theme；`buildQuestionForm()` / `buildOtherForm()` / `handleQuestionFormComplete()` / `handleQuestionFormAborted()` |
| `cmd/waveloom/tui_overlay.go` | **修改** | 新增 `overlayQuestion` 状态 + `renderQuestionOverlay` 渲染函数（包装 `huh.Form.View()`） |

---

## 测试策略

| 层级 | 测试内容 | 方法 |
|------|----------|------|
| 单元 | Schema 校验（合法/非法参数） | `go test ./pkg/tool/ -run TestAskUserQuestion` |
| 单元 | Mock UserResponder 返回/拒绝/超时 | `go test ./pkg/agentloop/ -run TestExecuteAskUserQuestion` |
| 单元 | 类型接口合规（Tool / TurnEvent / UserResponder） | 编译期断言 + 专用测试 |
| 单元 | TUI 渲染（Theme、Form 构建、Overlay、生命周期） | `go test ./cmd/waveloom/ -run "TestTheme\|TestBuildQuestion\|TestRenderQuestion\|TestQuestionClose\|TestQuestionRecord\|TestQuestionAdvance\|TestQuestionHandle"` |
| 集成 | Loop → executeToolCalls → UserResponder 链路 | `go test ./pkg/agentloop/ -run TestExecuteAskUserQuestion` |
| TUI | 选择题交互验收（手动） | cold agent review |

---

## 验收标准

1. LLM 可以调用 `ask_user_question` 工具，TUI 展示选择题界面
2. 单选：选择一个选项，答案正确返回
3. 多选：选择多个选项，答案以逗号分隔返回
4. "Other" 选项：用户输入自定义文本
5. 用户拒绝回答时，LLM 收到 `user_declined` 错误，可继续执行不终止
6. 工具结果正确返回 `{questions, answers}` 给 LLM
7. `(Recommended)` 后缀的选项在 TUI 中有视觉区分
