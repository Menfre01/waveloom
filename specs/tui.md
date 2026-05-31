# TUI 设计规格书

## 定位

`cmd/waveloom/tui*.go` 是 Waveloom 的正式 **Code Agent TUI 模块**，提供完整的交互式代码代理体验。
它直接连接 `pkg/context/ContextManager` + `agentloop.Loop` + `permission.Guard`，
覆盖 prompt 输入、流式 thought/text 渲染、7 种工具调用展示、权限确认交互。

### 设计原则

- **Agent 自主操作，用户旁观 + 发指令**：agent 自行调用工具读/写文件，用户无需手动选文件、管理上下文。TUI 的职责是展示思考过程 + 工具行为，而不是提供文件管理 UI。
- **对话即交互**：展开工具输出、折叠 thought 在对话 viewport 内完成；仅权限确认和 Help 使用覆盖层。
- **最小覆盖层原则**：覆盖层仅用于需要用户决策的阻断式交互（权限确认）或临时参考（Help）。

---

## 布局：三明治结构 + 两个覆盖层

> **零 emoji 原则**：全 UI 不使用任何 emoji。Header 信息用带主题色的文字前缀（`工作区:` `帮助:`），覆盖层标题用 `▲` 等 ASCII 符号，Footer 指标用缩写（`lat:` 替代 ⚡）。body 内容区前缀为单宽 ASCII（`>` `*` `●` `~`），确保所有终端等宽对齐。

### 三明治结构

```
╦ ╦╔═╗╦ ╦╔═╗╦  ╔═╗╔═╗╔╦╗
║║║╠═╣ ║ ║╣ ║  ║ ║║ ║║║║
╚╩╝╩ ╩ ╩ ╚═╝╩═╝╚═╝╚═╝╩ ╩
工作区: /home/user/project          帮助: Ctrl+H
├─ Viewport ────────────────────────────────────────────────────────┤
│                                                                   │
│   > 重构 auth 模块，加上 rate limiter                              │
│                                                                   │
│   ~ 思考完成 (234 tokens) — Tab 展开                               │
│   * 好的，让我先读取 auth/login.go...                              │
│       这里是 assistant 的多行回复，支持 **GitHub 风格**            │
│       `markdown` 渲染。代码块、列表、标题等均可正确显示。           │
│                                                                   │
│   ● read_file   auth/login.go  (230B, 8ms)                        │
│   ● write_file  auth/login.go  (1.2KB, 45ms)                      │
│   │  package auth                                                  │
│   │  import "fmt"                                           │
│   │  ... (Ctrl+O 展开全部)                                         │
│   ● shell       go test ./pkg/auth/... (exit 0, 120ms)             │
│   │  ok   waveloom/pkg/auth  0.234s                                │
│   │  ... (Ctrl+O 展开全部)                                         │
│                                                                   │
│   * 已完成 rate limiter 的添加。                                   │
│                                                                   │
├─ Input ───────────────────────────────────────────────────────────┤
│ > 输入消息，Enter 发送...                                          │
├─ Footer HUD ──────────────────────────────────────────────────────┤
  deepseek-v4    ctx ██████░░░░ 67%    cache 42%    T 3    M 15    lat 234ms  
└──────────────────────────────────────────────────────────────────┘
```

### 各区域职责

| 区域 | 高度 | 内容 |
|------|------|------|
| **Header** | 固定 4 行 | 第 1-3 行：ASCII art "WAVELOOM"（渐变色 `#5f5faf`→`#5fafd7`）；第 4 行：`工作区:` 前缀（`colorHeaderAccent`）+ 路径（`colorHeaderFg`）左对齐，`帮助: Ctrl+H` 右对齐 |
| **Viewport** | 弹性填充 | 对话历史：user/assistant/thought/tool 段落，支持滚动 |
| **Input** | 固定 3 行 | textinput 单行输入，带圆角边框 |
| **Footer HUD** | 固定 1 行 | 实时动态：模型名、上下文窗口使用量、缓存命中率、Turns、Messages、延迟 |

- **对话区**: viewport 全宽，自动滚到底部，支持 ↑↓ PgUp/Dn 浏览历史
- **输入区**: textinput 单行输入，Enter 发送
- **Footer HUD**: 每秒刷新，显示模型名 / 上下文窗口（已用/总量）/ 缓存命中率 / Turns / Messages / 本轮延迟

### 覆盖层一览

| 覆盖层 | 触发方式 | 类型 | 说明 |
|--------|----------|------|------|
| 权限确认 | agent 工具调用触发 ask 决策时**自动弹出** | 阻断式 | 用户必须选择后才能继续 |
| Help | `Ctrl+H` **手动触发** | 参考式 | Esc 关闭，不阻断 |

### 覆盖层 1：权限确认（阻断式，自动弹出）

当 agent 的工具调用命中 Guard 的 `ask` 决策时，弹出权限确认框，agent loop **阻塞等待用户选择**。

权限确认采用**列表式交互**：用户通过 `↑` `↓` 在选项间导航，`Enter` 确认选择。

```
┌─ Permission ──────────────────────────────────────────────────────┐
│  ▲ Permission Required                                            │
│                                                                   │
│  Tool:  shell                                                     │
│  Args:  go test ./pkg/auth/...                                    │
│  Reason: 执行外部命令需要确认                                      │
│                                                                   │
│  ▶ Allow (本次放行)                                                │
│    Allow All (记住并始终放行)                                      │
│    Deny (本次拒绝)                                                 │
│    Deny All (记住并始终拒绝)                                       │
│                                                                   │
│  ↑↓ 导航   Enter 确认   Esc = Deny                                │
└──────────────────────────────────────────────────────────────────┘
```

- 四个选项（列表式，`▶` 标识当前选中项，默认高亮 Allow）：
  - **Allow**（本次放行）— 主选项，绿色高亮
  - **Allow All**（记住并始终放行）— 次级，缩进 2 格，写入 session 规则
  - **Deny**（本次拒绝）— 主选项，红色高亮
  - **Deny All**（记住并始终拒绝）— 次级，缩进 2 格，写入 session 规则
- `Allow All`/`Deny All` 通过 `Guard.AddRule()` 持久化到 session 规则，同 pattern 不再询问
- `↑` `↓` 在 4 个选项间移动高亮（按上述顺序：Allow → Allow All → Deny → Deny All）
- `Enter` 确认当前高亮选项
- `Esc` 等同于 `Deny`
- 确认框出现在**底部**（Footer HUD 上方），不遮挡对话区。对话区保持正常渲染（不 dim）

**与 agent loop 的集成方式：**

agent loop 的 `Config.UserResponder` 传入自定义 responder，在 `AskUser()` 中：
1. 通过 channel 发送权限请求到 TUI goroutine
2. **阻塞**等待用户选择
3. 返回 `DecisionResult` 给 agent loop

```go
// 伪代码
type tuiUserResponder struct {
    ch chan permissionReq
}

func (r *tuiUserResponder) AskUser(ctx context.Context, toolName string, args json.RawMessage, result permission.DecisionResult) permission.UserChoice {
    req := permissionReq{toolName, args, result, make(chan permission.UserChoice)}
    r.ch <- req
    select {
    case choice := <-req.reply:
        return choice
    case <-ctx.Done():
        return permission.UserChoice{Decision: permission.DecisionDeny}
    }
}
```

TUI 端：
```go
// Update 中处理
case permissionReqMsg:
    m.permissionReq = &msg
    // 渲染阻断框
```

### 覆盖层 2：Help（参考式，Ctrl+H 手动触发）

```
┌─ 快捷键 ──────────────────────────────────────────────────────────┐
│                                                        Esc 关闭  │
│                                                                   │
│  常用                                         颜色                │
│  Enter          发送消息                       colorOK 绿          │
│  Ctrl+C         退出                           colorOK 绿          │
│  Ctrl+L         重置上下文（保留 system prompt）colorOK 绿          │
│                                                                   │
│  展开/折叠                                                         │
│  Ctrl+O         工具输出（仅作用于 tool）       colorGray 灰        │
│  Tab            思考过程（仅作用于 thought）    colorGray 灰        │
│                                                                   │
│  导航                                                              │
│  ↑ ↓            滚动对话历史                  colorMuted 暗灰      │
│  PgUp PgDn      翻页                          colorMuted 暗灰      │
│  Ctrl+H         本帮助                        colorMuted 暗灰      │
│                                                                   │
│  Esc 关闭                                          colorMuted      │
└──────────────────────────────────────────────────────────────────┘
```

**主次规则**：常用操作绿色（`colorOK`）醒目；展开/折叠灰色（`colorGray`）中等；导航暗灰（`colorMuted`）弱化。Esc 全局统一为暗灰。

Agent 自主操作文件，无需 File Picker——对话流中 tool 输出已足够。

---

## 设计语言：前缀 + Spinner 动画

统一用**单字符 ASCII 前缀**标识角色，用**颜色**和 **bubbles spinner 动画**表达状态。
所有 7 种工具共用 `●` 前缀。全 UI 零 emoji——Header 用 `工作区:` `帮助:` 等带色文字标签，不依赖 emoji 装饰。

### 核心规则

| 维度 | 含义 |
|------|------|
| **前缀字符** | 标识 WHO：`>` 用户 / `*` assistant / `●` 工具 / `~` thought |
| **颜色** | 标识 STATE：蓝=用户 / 绿=完成 / 红=失败 / 灰=进行中 |
| **动画** | 标识 ACTIVE：静态=空闲/完成，spinner 旋转=流式进行中 |

### 前缀体系

| 角色 | 前缀 | 宽度 | 静态 | 流式中 |
|------|------|------|------|--------|
| User | `>` | 1 | 蓝色 `#5fafff` | —（无流式） |
| Assistant | `*` | 1 | 绿色 `#5faf5f` | spinner.MiniDot（绿色 `#5faf5f`） |
| Tool（所有 7 种） | `●` | 1 | 绿 `#5faf5f` / 红 `#d75f5f` | spinner.Line（灰色 `#777777`） |
| Thought | `~` | 1 | 灰色 `#777777` | spinner.Points（灰色 `#777777`） |

### Spinner 动画机制

使用 `charm.land/bubbles/v2/spinner` 组件驱动前缀动画，替代手写 tickMsg + 帧色表：

- model 中维护 3 个 `spinner.Model`（`spAsst`、`spThought`、`spTool`），分别对应 3 种流式角色
- `Init()` 中调用 `sp.Tick` 启动动画；`Update()` 中将 `spinner.TickMsg` 路由到所有 4 个 spinner（含通用 HUD spinner）
- 流式段落渲染时调用 `sp.View()` 获取当前动画帧字符作为前缀
- 完成后切换为静态 ASCII 字符（`*` / `~` / `●`）

**Spinner 类型对照：**

| 角色 | Spinner 类型 | 视觉效果 | 说明 |
|------|-------------|----------|------|
| Assistant | `spinner.MiniDot` | 微小 braille 点旋转 | 微妙不抢眼，适合阅读流 |
| Thought | `spinner.Points` | 点填充渐变 `∙→●` | 暗示思考进行中 |
| Tool | `spinner.Line` | 经典旋转 `\|/-\` | 清晰表示任务执行中 |
| HUD | `spinner.Dot` | braille 点旋转 | 通用加载指示 |

### 消息渲染

#### User

```
> 重构 auth 模块，加上 rate limiter
```
- `>` 蓝色 `#5fafff` Bold，无动画
- `PrepareRun` 后立即追加

#### Assistant（流式 content + Markdown 渲染）

```
* 好的，让我先读取 auth/login.go...
    这里是多行回复内容，支持 **GitHub 风格** `markdown` 渲染。

    ## 标题
    - 列表项 1
    - 列表项 2

    ```go
    func main() { ... }
    ```
```
- `*` 前缀：流式中绿色亮度呼吸（4 帧循环），流式结束绿色静态 `#5faf5f`
- **内容区**：**标准 GitHub 风格 Markdown 渲染**（详见下方 "Markdown 渲染" 章节）
- 打字机效果，viewport 实时滚到底部
- 内容始终完整显示（不折叠、不截断），区别于 thought 和 tool 输出

#### Thought（reasoning_content 流式）

Thought 消息在流式过程中**只刷新末尾几行**（随 reasoning delta 追加而变化），完成后**收敛为一行**。

**流式中**（实时刷新，显示最近 N 行）：
```
~ 用户想给 auth 模块加 rate limiter...
  需要考虑用 token bucket 还是 sliding window...
  如果用 token bucket，需要存上次刷新时间...
```
- `~` 灰色亮度呼吸（与 `*` 同色表 `breatheGrayBrightness`）
- 内容区域随 reasoning delta 追加而增长，viewport 仅刷新末尾几行
- 始终显示**全部内容**（不截断），区别于 tool 输出的折叠预览

**完成后（收敛为一行）**：
```
~ 思考完成 (234 tokens) — Tab 展开
```
- `~` 灰色静态 `#777777`
- 默认收敛为单行摘要，减少视觉干扰

**展开态**（Tab 切换，再次显示完整 content）：
```
~ ┌ 思考过程 ─────────────────────────────┐
  │ 用户想给 auth 模块加 rate limiter...     │
  │ 需要考虑用 token bucket 还是 sliding...  │
  └───────────────────────────────────────┘
```
- 外框 `#555`，内容 `#888` 暗灰色
- 再次 Tab 折叠回收敛行

#### Tool（7 种工具统一 `●` 前缀）

工具行格式：`● {工具名} {参数摘要}  — {状态后缀}`

**执行中（ToolCallStart）：**
```
● read_file  auth/login.go
```
- `●` 灰色大小+亮度双维呼吸（`·`→`○`→`●`→`○`）

**完成（ToolCallResult ok）：**
```
● read_file  auth/login.go  (230B, 8ms)
```
- `●` 绿色静态 `#5faf5f`

**失败（ToolCallResult err）：**
```
● read_file  auth/login.go  (permission denied)
```
- `●` 红色静态 `#d75f5f`

**各工具摘要后缀格式：**

| 工具名 | 成功后缀 |
|--------|----------|
| read_file | `path (size, duration)` |
| write_file | `path (size, duration)` |
| edit_file | `path (+N -M lines, duration)` |
| shell | `command (exit 0, duration)` |
| grep | `"pattern" in path (N matches)` |
| search_file | `glob in path (N files)` |
| ls | `path (N entries)` |

**write_file / edit_file 默认预览（折叠态）：**

摘要行下方**默认显示前 3 行**文件内容，末尾标注 `... (Ctrl+O 展开全部)`：
```
● write_file  auth/login.go  (1.2KB, 45ms)
  │  package auth
  │  import "fmt"
  │  func NewLoginManager() *LoginManager {{
  │  ... (Ctrl+O 展开全部)
```

**shell 默认预览（折叠态）：**

摘要行下方**默认显示前 5 行** stdout，末尾标注 `... (Ctrl+O 展开全部)`：
```
● shell  go test ./pkg/auth/... (exit 0, 120ms)
  │  ok    waveloom/pkg/auth    0.234s
  │  ok    waveloom/pkg/auth/middleware  0.156s
  │  ... (Ctrl+O 展开全部)
```

**read_file / grep / search_file / ls：** 仅一行摘要，不显示预览（内容通过 Ctrl+O 展开查看）。

展开/折叠通过 `Ctrl+O` 控制**最后一个** tool 段落，展开后显示完整输出（带行号代码 / stdout / diff 等）。

---

## 流式渲染架构

### 事件 → 消息映射

agent loop 的 `TurnEvent` 通过 `p.Send()` 实时推送到 TUI：

| TurnEvent | TUI 处理 |
|-----------|----------|
| `StreamDelta{ContentDelta}` | 追加到当前 assistant 段落末尾（markdown 按行增量解析） |
| `StreamDelta{ReasoningDelta}` | 追加到当前 thought 段落，实时刷新末尾几行 |
| `ToolCallStart` | 开始新的 tool 段落，`●` 进入大小+亮度呼吸 |
| `ToolCallResult` | 填充 tool 段落的摘要/结果/预览行 |
| `TurnStats` | 累加到 HUD 数据，等待下一 tick 刷新 Footer |
| `LoopDone` | thought → 收敛为一行；assistant/tool → stateDone；Footer HUD 刷新 |

### 渲染状态机

（TUI 内部维护，跟踪当前正在构建的段落）

```
Idle
  │
  ├─ StreamDelta(content) ──→ AssistantParagraph
  │     prefix: * (绿色亮度呼吸)
  │     │
  │     ├─ StreamDelta(content) ──→ 继续追加（* 保持呼吸）
  │     ├─ ReasoningDelta ──→ 追加到 thought 块
  │     ├─ ToolCallStart ──→ ToolParagraph（* 结束呼吸→绿色静态）
  │     └─ LoopDone ──→ * 绿色静态，Idle
  │
  ├─ ReasoningDelta ──→ ThoughtBlock
  │     prefix: ~ (灰色亮度呼吸)
  │     │
  │     ├─ ReasoningDelta ──→ 继续追加（~ 保持呼吸）
  │     └─ StreamDelta(content) ──→ AssistantParagraph（~ 结束呼吸→灰色静态）
  │
  └─ ToolCallStart ──→ ToolParagraph
        prefix: ● (灰色大小+亮度双维呼吸：·→○→●→○)
        │
        ├─ ToolCallResult(ok)──→ ● 绿色静态，填充后缀
        ├─ ToolCallResult(err)──→ ● 红色静态，填充错误后缀
        ├─ StreamDelta(content)─→ AssistantParagraph
        └─ LoopDone ──→ Idle
```

### 实现要点

- model 持有 `*tea.Program` 引用，goroutine 内通过 `p.Send(customMsg)` 推送事件
- 自定义消息类型：`streamDeltaMsg`, `toolStartMsg`, `toolResultMsg`, `loopDoneMsg`, `permissionReqMsg`
- viewport 内容从 `[]Paragraph` 列表全量渲染（段落数 <100，O(n) 可忽略），事件到达时触发重建
- tick 帧（1s）仅递增 `frame` 计数器驱动呼吸动画，不触发 viewport 重建
- 覆盖层弹出/关闭不改变 viewport 内容——覆盖层在 View() 中叠加渲染

---

## 交互规范

### 键盘快捷键

| 按键 | 作用域 | 行为 |
|------|--------|------|
| `Enter` | 全局 | 发送输入框内容 → 启动 agent loop |
| `Ctrl+C` | 全局 | 退出程序 |
| `Ctrl+L` | 全局 | 调用 `cm.Reset()`，清空对话 |
| `Ctrl+O` | 全局 | 展开/折叠最后一个 tool 段落的完整输出（仅作用于 tool，对 thought 无影响） |
| `Ctrl+H` | 全局 | 打开/关闭 Help 覆盖层 |
| `Tab` | 全局 | 展开/折叠最后一个 thought 块（仅作用于 thought，对 tool 无影响） |
| `↑`/`↓` | 全局 | 滚动对话历史 |
| `↑`/`↓` | 权限确认框 | 在 Allow / Deny / Allow All / Deny All 间移动高亮 |
| `PgUp`/`PgDn` | 全局 | 翻页对话历史 |
| `Enter` | 权限确认框 | 确认当前高亮选项 |
| `Esc` | 权限确认框 | 等同于 Deny |
| `Esc` | Help 覆盖层 | 关闭 Help |

### 覆盖层状态管理

用 union 表示当前活跃覆盖层：

```go
type Overlay int
const (
    overlayNone      Overlay = iota // 无覆盖层，正常交互
    overlayPermission               // 权限确认框（阻断式）
    overlayHelp                     // 帮助（参考式）
)

type model struct {
    // ...
    overlay        Overlay
    permissionReq  *permissionReqMsg // 当前待确认的权限请求（仅 overlayPermission 时非 nil）
    permChoice     chan<- permission.UserChoice // 用户选择后写入此 channel
}
```

**键盘路由规则：**

| 当前 overlay | 可用按键 |
|-------------|---------|
| `overlayNone` | 全部快捷键 |
| `overlayPermission` | `↑` `↓` / `Enter` / `Esc`（其余键忽略） |
| `overlayHelp` | `Esc`（其余键忽略） |

- 覆盖层关闭后，焦点回到 textinput
- 权限确认框关闭后，结果写入 `permChoice` channel，唤醒阻塞的 agent loop

### 工具输出展开/折叠

不引入独立的 Tool Output 覆盖层——展开/折叠在对话 viewport 内完成：

- `Ctrl+O` 触发最后一个 tool 段落的状态切换（折叠 ↔ 展开）
- **折叠态（默认）**：
  - 通用：一行摘要 `● toolname args (230B, 12ms)`
  - `write_file` / `edit_file`：摘要行下方**默认显示前 3 行**（预览），末尾显示 `... (Ctrl+O 展开全部)`
  - `shell`：摘要行下方**默认显示前 5 行 stdout**（预览），末尾显示 `... (Ctrl+O 展开全部)`
- **展开态**：摘要行下方追加完整输出
  - `read_file`：带行号的代码块
  - `write_file` / `edit_file`：完整文件内容或 diff
  - `shell`：完整 stdout + stderr
  - `grep`：完整匹配列表
  - `search_file` / `ls`：完整文件列表
- 实现：在 viewport 内容中替换对应段落的渲染文本，重新 SetContent

### 权限交互（首期即实现）

- 注册自定义 `tuiUserResponder` 到 agent loop 的 `Config.UserResponder`
- agent loop 调用 `AskUser()` → channel 发权限请求到 TUI → 弹出确认框 → 用户选择 → channel 返回结果
- 确认框提供四种选择：Allow / Deny / Allow All（写入 session 规则）/ Deny All（写入 session 规则）
- `Allow All`/`Deny All` 通过 `Guard.AddRule(rule, ScopeSession)` 持久化，同 pattern 后续不再询问
- agent loop 侧的 `AskUser()` 也监听 `ctx.Done()`，ctx 取消时自动 deny

---

## 样式常量

```go
// 静态色（统一色板）
var (
    colorUser     = lipgloss.Color("#5fafff") // 蓝 → `>`
    colorOK       = lipgloss.Color("#5faf5f") // 绿 → `*` `●` 完成态
    colorErr      = lipgloss.Color("#d75f5f") // 红 → `●` 失败态
    colorGray     = lipgloss.Color("#777777") // 灰 → `~` 完成态
    colorMuted    = lipgloss.Color("#555555") // 暗灰 → 展开块外框

    // 工具输出内
    colorDiffAdd   = lipgloss.Color("#5f8700")
    colorDiffDel   = lipgloss.Color("#d70000")
    colorMatch     = lipgloss.Color("#d7af00")
    colorToolCode   = lipgloss.Color("#d7875f") // 工具输出内代码
    colorToolCodeBg = lipgloss.Color("#2a2a2a")

    // 三明治布局专用
    colorHeaderBg     = lipgloss.Color("#1a1a2e")
    colorHeaderFg     = lipgloss.Color("#e0e0e0")
    colorHeaderAccent = lipgloss.Color("#5fafd7") // 工作区:/帮助: 前缀色
    colorFooterBg  = lipgloss.Color("#1a1a2e")
    colorFooterFg  = lipgloss.Color("#a0a0a0")
    colorOverlayBg = lipgloss.Color("#2a2a3e")
    colorOverlayBorder = lipgloss.Color("#5f5faf")
    colorDimmed    = lipgloss.Color("#3a3a3a") // 覆盖层背后对话区变暗

    // 强调金色（Footer HUD / 覆盖层复用）
    colorAccentGold = lipgloss.Color("#d7af5f")
)
```

**统一色板总结：**

| 类别 | 颜色数 | 用途 |
|------|--------|------|
| 语义色 | 5 | 用户蓝 / 完成绿 / 失败红 / 中性灰 / 暗灰 muted |
| 布局色 | 7 | Header/Footer 背景+前景 / Overlay 背景+边框 / 覆盖层背后 dim / 强调金 |
| 工具输出色 | 4 | diff 增/删 / 代码前景+背景 |

动画颜色由 `spinner.Model.Style` 控制，不额外维护色表。Markdown 渲染颜色由 Glamour 内置 dark 主题管理。不引入 per-tool 颜色，工具统一用 `●` + 状态色（绿/红），7 种工具视觉不可区分——信息在**工具名文本**中，不在颜色中。

---

## Assistant 消息 Markdown 渲染

Assistant 回复内容经 `charm.land/glamour/v2`（dark 主题）渲染为终端格式文本，完整支持 GitHub Flavored Markdown：

### 支持的语法（GFM 全覆盖）

- 标题（H1-H6）、粗体、斜体、行内代码、代码块（chroma 语法高亮）
- 无序/有序列表、嵌套列表、任务列表 `- [x]`
- 链接、引用、分隔线
- **表格**（列对齐，自动换行/截断）
- 图片（降级显示 alt text）

### 实现

- 使用 `charm.land/glamour/v2` 的 `TermRenderer` 渲染 markdown（dark 主题）
- `TermRenderer` 在 `WindowSizeMsg` 时重建以适配当前 viewport 宽度（`contentWidth - 4`）
- 支持完整 GFM：表格、任务列表 `- [x]`、嵌套引用、代码块语法高亮（chroma）
- `glamourRenderer` 为 nil 时降级为纯文本输出

---

## 段落模型

Viewport 内容不在 View() 时从 `[]llm.Message` 重建，而是维护独立的**段落列表** `[]Paragraph`：

```go
type ParagraphType int
const (
    paraUser      ParagraphType = iota // > 用户消息
    paraAssistant                      // * assistant 回复（含 markdown）
    paraThought                        // ~ 思考过程
    paraTool                           // ● 工具调用
)

type ParaState int
const (
    stateStreaming ParaState = iota // 流式进行中（spinner 动画）
    stateDone                        // 完成（静态）
    stateCollapsed                   // 折叠（thought 收敛 / tool 默认）
    stateExpanded                    // 展开（thought / tool 输出）
)

type Paragraph struct {
    Type     ParagraphType
    State    ParaState
    Content  strings.Builder // 文本内容

    // Tool 专用
    ToolName    string
    ToolArgs    string
    ToolResult  string // 完整输出（展开时显示）
    ToolError   string
    ToolDurMs   int64
    ToolDenied  bool

    // Thought 专用
    ThoughtTokens int // 完成后的 token 数
}
```

**渲染规则：**
- `paraUser`：`>` 前缀，蓝色，始终 `stateDone`
- `paraAssistant`：`*` 前缀，`stateStreaming` 时 spinner.MiniDot 动画，`stateDone` 时绿色静态。内容经 Glamour 渲染
- `paraThought`：`~` 前缀，`stateStreaming` 时 spinner.Points 动画 + 显示全部 content；`stateDone`→自动收敛为 `stateCollapsed`（一行摘要）；`stateExpanded` 时显示完整内容外框
- `paraTool`：`●` 前缀，`stateStreaming` 时 spinner.Line 动画；`stateDone` 时绿色/红色静态；`stateCollapsed` 显示摘要+预览行；`stateExpanded` 显示完整输出

---

## Viewport 渲染策略

**段落追加是增量的，viewport 内容在状态变化时从段落列表全量重建。**

- 新事件到达（StreamDelta / ToolCallStart / ToolCallResult / LoopDone）→ 追加/更新对应 Paragraph → 从 `[]Paragraph` 重新渲染 viewport 内容
- tick 帧（每 1s）→ 仅递增 `frame` 计数器，不重建 viewport。呼吸动画通过 View() 中根据 `frame % 4` 选择样式实现
- 段落数通常 <100，全量重建 O(n) 可忽略
- 覆盖层弹出时 viewport 不变（仅上层覆盖渲染）；关闭后恢复
- `SetContent` 调用时机：初始化、Reset、收到任何自定义消息、段落折叠/展开

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 修改 | `cmd/waveloom/tui.go` | TUI 主程序：model 结构体、tea.Init/Update/View、agent loop 集成、流式状态机、`tuiUserResponder` |
| 新增 | `cmd/waveloom/tui_styles.go` | 样式常量：统一色板、呼吸动画色表、Header/Footer/Input/Viewport 基础样式、代码块/diff 着色 |
| 新增 | `cmd/waveloom/tui_renderer.go` | 段落模型 + Markdown 渲染器：Paragraph 结构体、段落增删改查、GitHub 风格 Markdown→终端渲染、工具摘要格式化、输出折叠预览 |
| 新增 | `cmd/waveloom/tui_overlay.go` | 覆盖层渲染：权限确认列表式 UI（↑↓ 导航）、Help 快捷键列表、覆盖层键盘路由 |
| 新增 | `specs/tui.md` | 本规格书 |

4 文件，同一 `package main`，预计总规模 ~1200 行。

---

## Footer HUD 规范

Footer HUD 单行固定高度，每秒 tick 刷新一次。各项用 **2-3 空格**分隔，无分隔线。通过**颜色主次**区分信息层级：模型名和延迟是主信息（亮色），其余是辅信息（暗色标签 + 浅灰数值）。

### 字段与颜色层级

| 优先级 | 字段 | 格式示例 | 标签色 | 数值色 | 数据来源 |
|--------|------|----------|--------|--------|----------|
| **主** | 模型名 | `deepseek-v4` | —（无标签） | `colorOK` 绿色加粗 | `llm.ClientConfig.Model` |
| **主** | 延迟 | `lat 234ms` | `colorFooterFg` 暗灰 | 绿/金/红（阈值着色） | TurnStats→LoopDone wall-clock |
| 辅 | 上下文 | `ctx ██████░░░░ 67%` | `colorFooterFg` 暗灰 | 进度条着色（见下） | `cm.Stats().TotalPromptTokens` / 模型窗口总量 |
| 辅 | 缓存 | `cache 42%` | `colorFooterFg` 暗灰 | 绿/金/暗灰（阈值着色） | `cacheHit/(cacheHit+cacheMiss)` |
| 辅 | Turns | `T 3` | `colorFooterFg` 暗灰 | `colorHeaderFg` 浅灰 | `cm.Stats().TotalTurns` |
| 辅 | Messages | `M 15` | `colorFooterFg` 暗灰 | `colorHeaderFg` 浅灰 | `cm.Stats().MessageCount` |

**上下文进度条规范：**

- 固定宽度 10 字符，填充字符 `█`，空白字符 `░`
- 格式：`ctx ██████░░░░ 67%`（bar + 空格 + 百分比）
- 着色：<50% `colorOK` 绿 / 50-80% `colorMDAccent` 金 / >80% `colorErr` 红
- 计算：`usedTokens / modelContextWindow`，token 数按 K 格式化（如 `85K/128K` 不显示，仅 bar 呈现）
- 无上下文窗口数据时显示 `ctx --`

**延迟着色**：<500ms `colorOK` 绿 / 500ms-2s `colorMDAccent` 金 / >2s `colorErr` 红。

**缓存着色**：>50% `colorOK` 绿 / 25-50% `colorMDAccent` 金 / <25% `colorFooterFg` 暗灰 / 无数据 `colorFooterFg` 暗灰 `--`。

**布局**：左对齐，各项间距 2-3 空格，无分隔线。模型名在首、延迟在尾形成视觉锚点。

### 样式

- 背景色 `colorFooterBg`（`#1a1a2e`），无边框，与 Viewport 之间无分隔线
- 标签文字（`ctx` `cache` `T` `M` `lat`）使用 `colorFooterFg`（`#a0a0a0`），不加粗
- 模型名 `colorOK`（`#5faf5f`）加粗 — 最醒目
- 数值默认 `colorHeaderFg`（`#e0e0e0`）
- 延迟和缓存的阈值着色见上表

---

## 后续迭代

- `Ctrl+D` Diff 覆盖层（对所有 `edit_file`/`write_file` 的变更做汇总）
- Ctrl+Y 手动 approve 被 deny 的工具调用
- Tool 输出内语法高亮（chroma）
- Textarea 多行输入（Ctrl+Enter 发送）
- 工具输出展开时独立滚动（子 viewport）
- Session 持久化（保存/恢复消息历史）
