# Permission & Safety 组件规格书

## 组件定位

Permission & Safety 是 Waveloom Code Agent 的**守门人**，在工具执行前拦截、评估、决策。它确保 Agent 不会在用户不知情的情况下执行破坏性操作（写文件、删文件、执行命令等），同时不过度阻碍正常工作流。

核心职责：
1. 权限决策 — 对每次工具调用，返回 allow / deny / ask 三种决策
2. 规则管理 — 从配置文件加载 allow/deny/ask 规则，运行期可追加
3. 路径安全 — 敏感路径（.git/、shell 配置文件等）的访问拦截
4. 命令安全 — 危险命令模式检测和分级
5. 会话记忆 — 用户批准的操作可在 session 内自动通过

## 参考来源

### Claude Code

- `permissions/permissions.ts` — 核心决策引擎 `hasPermissionsToUseTool`
- `types/permissions.ts` — `PermissionDecision`、`PermissionBehavior`、`RiskLevel`、`DecisionReason` 类型
- `permissions/filesystem.ts` — 文件读写的路径安全检查
- `permissions/pathValidation.ts` — 路径安全校验、规则匹配
- `permissions/dangerousPatterns.ts` — 危险命令模式
- `permissions/denialTracking.ts` — 拒绝跟踪，连续拒绝达上限回退弹窗
- `permissions/PermissionUpdate.ts` — 规则更新（addRules/replaceRules/removeRules/setMode）
- `permissions/permissionsLoader.ts` — 从磁盘加载规则
- `permissions/permissionRuleParser.ts` — `ToolName(content)` 格式解析
- `toolPermission/PermissionContext.ts` — 运行时权限上下文协调
- `sandbox/sandbox-adapter.ts` — 沙箱适配层

### Codex CLI

- `protocol/src/protocol.rs` — `AskForApproval`（5 级策略）、`SandboxPolicy`、`ReviewDecision`
- `protocol/src/approvals.rs` — 审批事件结构
- `protocol/src/permissions.rs` — `FileSystemSandboxPolicy`、`FileSystemAccessMode`（Read/Write/Deny）
- `core/src/exec_policy.rs` — 执行策略管理、命令前缀规则匹配
- `core/src/tools/sandboxing.rs` — 审批缓存 + 会话记忆
- `core/src/guardian/` — Guardian 自动审核子 Agent（90s 超时）
- `core/src/safety.rs` — 补丁安全性评估
- `sandboxing/src/manager.rs` — 跨平台沙箱管理
- `execpolicy/src/` — Starlark DSL 规则引擎

### 两者共同模式

| 模式 | Claude Code | Codex CLI |
|------|-------------|-----------|
| 决策三态 | allow / deny / ask | Allow / Denied / Prompt |
| 规则来源 | userSettings / projectSettings / localSettings / session / cliArg | TOML 配置 + Starlark .rules 文件 |
| 工具级 vs 内容级 | `ToolName` + `ToolName(content)` | prefix_rule + 精确匹配 |
| 危险路径 | .git/, shell rc, .claude/ | .git/, .agents/, .codex/ |
| 会话记忆 | "Yes, and don't ask again" → session 级规则 | `ApprovedForSession` → `ApprovalStore` |
| 风险等级 | LOW / MEDIUM / HIGH | Guardian risk_level |

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 决策模型 | allow/deny/ask 三态 | Claude Code + Codex 共识，ask 交给调用方（CLI UI / API handler）处理 |
| 规则格式 | `ToolName` + `ToolName(pattern)` | Claude Code 模式，简洁直观；`pattern` 支持 glob 匹配路径或命令前缀 |
| 规则来源 | config / session / cliArg | 三层，config 持久化、session 运行期、cliArg 单次覆盖 |
| 路径安全 | 安全路径列表 + 危险路径列表 | 白名单（工作目录）+ 黑名单（.git/、shell rc）双保险 |
| 命令安全 | 危险模式匹配 + 风险等级 | 复用 Wave 2 的 `dangerousPatterns`，升级为硬拦截 |
| 权限检查时机 | Loop 执行工具前 | Loop 调用 `Guard.Check()` → 决策为 allow 才执行 |
| 沙箱 | Wave 3 不实现 | macOS Seatbelt / Linux seccomp 复杂度高，留给后续 Wave |
| AI 分类器 | Wave 3 不实现 | Claude Code 的 YOLO classifier 依赖额外 LLM 调用，留作 P2 |

## 组件边界

### 输入
- 工具调用请求（工具名 + JSON 参数）
- 权限规则配置（文件/CLI 参数）
- 用户决策回调（ask 模式下由调用方提供）

### 输出
- `Decision` — allow / deny / ask
- 规则持久化更新（session 级规则写入内存，config 级规则写入文件）

### 依赖（接口，非具体实现）
- `tool.Tool` — 读取工具名、判断 ConcurrentSafe
- `tool.ToolSpec` — 获取工具参数 Schema（未来用于内容级规则匹配）
- 无 LLM 依赖（Wave 3 不做 AI 分类器）

### 不纳入本组件
- 沙箱执行（macOS Seatbelt / Linux seccomp — 复杂度高，P2）
- AI 分类器（需要额外 LLM 调用，P2）
- Guardian 自动审核子 Agent（Codex 特有概念，P2）
- UI 展示（PermissionDialog 等属于 CLI 层，Wave 10）
- 网络策略（MCP / WebFetch 的网络权限，属于 Wave 8）

---

## 接口定义

### 核心接口

```
┌─────────────────────────────────────────────────────────────┐
│                         Guard                               │
│  ┌───────────────┐  ┌──────────────┐  ┌─────────────────┐ │
│  │  RuleEngine   │  │ PathSafety   │  │ CommandSafety   │ │
│  │  (规则匹配)   │  │ (路径安全)   │  │ (命令安全)      │ │
│  └───────┬───────┘  └──────┬───────┘  └────────┬────────┘ │
│          │                 │                    │           │
│          └────────────┬────┴────────────────────┘           │
│                       ▼                                     │
│              ┌────────────────┐                             │
│              │   Decide()     │                             │
│              │  权限决策入口   │                             │
│              └────────────────┘                             │
└─────────────────────────────────────────────────────────────┘
```

```go
// Decision 是权限检查的结果。
type Decision string

const (
    DecisionAllow Decision = "allow" // 允许执行
    DecisionDeny  Decision = "deny"  // 拒绝执行
    DecisionAsk   Decision = "ask"   // 需要用户确认
)

// DecisionReason 描述决策产生的原因。
type DecisionReason string

const (
    ReasonRule       DecisionReason = "rule"         // 匹配了显式规则
    ReasonDefault    DecisionReason = "default"      // 无匹配规则，走默认策略
    ReasonSafety     DecisionReason = "safety"       // 安全检查拦截
    ReasonSession    DecisionReason = "session"      // 会话级记忆
    ReasonBypass     DecisionReason = "bypass"       // bypass 模式（测试/CI）
)

// DecisionResult 包含权限决策的完整信息。
type DecisionResult struct {
    Decision         Decision       // 决策
    Reason           DecisionReason // 原因
    Message          string         // 人类可读的解释（给 LLM 或 UI 使用）
    Rule             string         // 匹配的规则原文（如 "Bash(git:*)"），空表示无规则匹配
    SuggestedPattern string         // 若用户选择"记住"，建议记住的 pattern；空表示无法提取
}

// CheckPermissionFunc 是权限检查函数签名。
// 由 Guard 实现，注入到 Loop 中。
type CheckPermissionFunc func(ctx context.Context, toolName string, input json.RawMessage) DecisionResult

// UserResponder 处理 ask 决策的用户交互。
// 具体实现由 CLI/UI 层提供（Wave 10），Guard 本身不感知 UI。
type UserResponder interface {
    // AskUser 向用户请求决策。
    // 返回：allow / deny，以及是否记住此决策到 session。
    AskUser(ctx context.Context, toolName string, input json.RawMessage, result DecisionResult) UserChoice
}

type UserChoice struct {
    Decision      Decision  // allow 或 deny
    RememberScope RuleScope // "" → 不记住；ScopeSession → session 内记住；ScopeConfig → 持久化到文件
    Feedback      string    // 可选的用户反馈文本
}

// Guard 是权限守门人的核心接口。
type Guard interface {
    // Check 对工具调用执行权限检查，返回决策结果。
    // 不负责用户交互——如果返回 ask，调用方需通过 UserResponder 获取用户决策。
    Check(ctx context.Context, toolName string, input json.RawMessage) DecisionResult

    // AddRule 追加一条规则。
    // scope 决定规则持久化到哪里：session 级 / config 级。
    AddRule(rule Rule, scope RuleScope) error

    // RemoveRule 移除一条规则。
    RemoveRule(rule Rule, scope RuleScope) error

    // ListRules 列出当前生效的规则。
    ListRules() []RuleEntry
}
```

### 规则模型

```go
// RuleBehavior 规则的行为。
type RuleBehavior string

const (
    RuleAllow RuleBehavior = "allow"
    RuleDeny  RuleBehavior = "deny"
    RuleAsk   RuleBehavior = "ask"
)

// RuleScope 规则的持久化范围。
type RuleScope string

const (
    ScopeSession RuleScope = "session" // 仅当前会话，进程结束消失
    ScopeConfig  RuleScope = "config"  // 写入配置文件，跨会话持久化
)

// RuleSource 规则的来源。
type RuleSource string

const (
    SourceConfig  RuleSource = "config"  // 配置文件
    SourceSession RuleSource = "session" // 会话内记忆
    SourceCLI     RuleSource = "cli"     // CLI 参数（--allow / --deny）
)

// Rule 表示一条权限规则。
// 格式：ToolName 或 ToolName(pattern)
// 示例：
//   "read_file"            → 工具级：允许/拒绝整个 read_file
//   "Bash(git *)"          → 内容级：匹配以 "git " 开头的命令
//   "write_file(src/**)"   → 内容级：匹配 src/ 下的路径
//   "shell"                → 工具级：shell 的旧别名也兼容
type Rule struct {
    Behavior RuleBehavior
    ToolName string // 工具名称
    Pattern  string // 可选的内容匹配模式（glob）
}

// RuleEntry 是带来源信息的规则。
type RuleEntry struct {
    Rule   Rule
    Source RuleSource
    Scope  RuleScope
}
```

### 路径安全

```go
// PathSafetyLevel 路径的风险等级。
type PathSafetyLevel string

const (
    PathSafe       PathSafetyLevel = "safe"       // 工作目录内，安全
    PathSensitive  PathSafetyLevel = "sensitive"  // .git/、shell rc 等，需确认
    PathDangerous  PathSafetyLevel = "dangerous"  // 工作目录外 + 敏感文件，高风险
)

// PathCheckResult 路径安全检查结果。
type PathCheckResult struct {
    Level             PathSafetyLevel
    Message           string // 解释为什么是这个等级
    ClassifierSafe    bool   // true → 未来 AI 分类器可自动批准
}
```

### 命令安全

```go
// CommandRiskLevel 命令的风险等级。
type CommandRiskLevel string

const (
    RiskLow      CommandRiskLevel = "low"      // 只读/查询命令
    RiskMedium   CommandRiskLevel = "medium"   // 写入/修改命令
    RiskHigh     CommandRiskLevel = "high"     // 危险命令模式匹配
)

// CommandCheckResult 命令安全检查结果。
type CommandCheckResult struct {
    Level    CommandRiskLevel
    Pattern  string // 匹配的危险模式描述（如有）
    Message  string // 解释
}
```

---

## Guard 实现设计

### 检查流程

```
Loop 执行工具前调用 Guard.Check(toolName, input)
  │
  ├── 1. 工具级 deny 规则检查
  │     → 命中 → DENY (reason=rule)
  │
  ├── 2. 工具级 ask 规则检查
  │     → 命中 → ASK (reason=rule)
  │     → 如果该工具在 ConcurrentSafe 白名单中且处于 autoAllowSandboxed 模式 → ALLOW (reason=rule)
  │
  ├── 3. 工具特有安全检查 (tool-specific check)
  │     ├── 文件工具 → PathSafetyCheck
  │     ├── Shell   → CommandSafetyCheck
  │     └── 其他    → passthrough
  │     → 安全检查 deny → DENY (reason=safety)
  │     → 安全检查 ask  → ASK (reason=safety)
  │
  ├── 4. 工具级 allow 规则检查
  │     → 命中 → ALLOW (reason=rule)
  │
  ├── 5. 内容级规则检查 (ToolName(pattern))
  │     → 命中 → 对应决策
  │
  ├── 6. Session 记忆检查
  │     → 命中 → ALLOW (reason=session)
  │
  ├── 7. Bypass 模式检查
  │     → 开启 → ALLOW (reason=bypass)
  │
  └── 8. 默认策略
        ├── read-only 工具 → ALLOW (reason=default)
        ├── write/execute 工具 → ASK (reason=default)
        └── 未知工具 → DENY (reason=default)
```

### 关键数据结构

```go
// GuardImpl 是 Guard 接口的默认实现。
type GuardImpl struct {
    mu sync.RWMutex

    // 规则引擎
    allowRules []RuleEntry // 有序，先匹配先生效
    denyRules  []RuleEntry
    askRules   []RuleEntry

    // 会话记忆：用户批准过的操作
    sessionMemory map[sessionKey]Decision

    // 路径安全
    workingDirs  []string          // 允许的工作目录列表
    sensitivePaths []string        // 敏感路径模式（.git/, shell rc 等）

    // 命令安全
    dangerousPatterns []DangerousCommandPattern

    // 模式
    bypassMode bool // true → 全部 allow（CI/测试场景）

    // 工具风险分类（从 Tool.ConcurrentSafe 推导）
    toolRiskClass map[string]ToolRiskClass
}

// sessionKey 是会话记忆的 key。
type sessionKey struct {
    ToolName string
    Pattern  string // 内容级匹配的 key
}

// ToolRiskClass 工具的风险分类。
type ToolRiskClass string

const (
    RiskClassRead    ToolRiskClass = "read"    // 只读：read_file, grep, search_file, ls
    RiskClassWrite   ToolRiskClass = "write"   // 写入：write_file, edit_file
    RiskClassExecute ToolRiskClass = "execute" // 执行：shell
)
```

### 与 Agent Loop 的集成

```
┌─────────────────────────────────────────────────────────────┐
│                        Agent Loop                            │
│                                                              │
│  for shouldContinue {                                        │
│    response = llmClient.SendMessage(...)                     │
│    for each toolCall {                                       │
│      ┌──────────────────────────────┐                       │
│      │  Guard.Check(toolName, input) │ ← 新增：权限检查      │
│      └──────────┬───────────────────┘                       │
│                 │                                             │
│         ┌───────┼────────┐                                   │
│         ▼       ▼        ▼                                   │
│      allow    ask      deny                                  │
│         │       │        │                                   │
│         │   UserResponder│                                    │
│         │   ├→ allow     │                                    │
│         │   └→ deny      │                                    │
│         │       │        │                                    │
│         ▼       ▼        ▼                                    │
│      执行工具  执行工具  返回拒绝消息                           │
│                       给 LLM                                 │
│    }                                                         │
│  }                                                           │
└─────────────────────────────────────────────────────────────┘
```

Loop 的 `executeToolCalls` 方法需要修改为：

```go
// 在执行每个工具前增加权限检查
for _, tc := range calls {
    result := l.guard.Check(ctx, tc.Name, json.RawMessage(tc.Arguments))
    switch result.Decision {
    case DecisionAllow:
        // 正常执行
    case DecisionDeny:
        // 构造拒绝消息，返回给 LLM
    case DecisionAsk:
        // 通过 UserResponder 获取用户决策
        choice := l.userResponder.AskUser(ctx, tc.Name, json.RawMessage(tc.Arguments), result)
        if choice.Decision != DecisionAllow {
            // 构造拒绝消息
        }
        if choice.Remember {
            l.guard.AddRule(Rule{...}, ScopeSession)
        }
    }
}
```

---

## 工具特有安全检查

### 文件工具安全检查

适用工具：`read_file`、`write_file`、`edit_file`、`search_file`、`grep`

```
PathSafetyCheck(path, operation)
  │
  ├── 1. 路径标准化
  │     → ResolvePath → 绝对路径 + Clean
  │     → EvalSymlinks → 解析符号链接
  │
  ├── 2. 工作目录检查
  │     ├── 在工作目录内 → PathSafe
  │     └── 在工作目录外 → PathDangerous
  │
  ├── 3. 敏感路径检查（无论是否在工作目录内）
  │     ├── .git/ → PathSensitive (classifierSafe=true)
  │     ├── .claude/ / .waveloom/ → PathSensitive (classifierSafe=true)
  │     ├── shell rc (.bashrc, .zshrc, .profile...) → PathSensitive (classifierSafe=true)
  │     ├── .gitconfig, .gitmodules → PathSensitive (classifierSafe=true)
  │     ├── .ssh/ → PathDangerous (classifierSafe=false)
  │     └── /etc/, /System/ → PathDangerous (classifierSafe=false)
  │
  ├── 4. 操作类型叠加
  │     ├── read + PathSafe → ALLOW
  │     ├── read + PathSensitive → ASK
  │     ├── read + PathDangerous → ASK
  │     ├── write + PathSafe → ASK (默认需确认)
  │     ├── write + PathSensitive → ASK (强调敏感)
  │     └── write + PathDangerous → DENY
  │
  └── 5. 返回 PathCheckResult
```

**危险文件列表**：

```go
var dangerousFiles = map[string]bool{
    ".gitconfig":   true,
    ".gitmodules":  true,
    ".bashrc":      true,
    ".bash_profile": true,
    ".zshrc":       true,
    ".zprofile":    true,
    ".profile":     true,
    ".ssh/config":  true,
}

var sensitiveDirs = map[string]bool{
    ".git":     true,
    ".claude":  true,
    ".waveloom": true,
    ".vscode":  true,
    ".idea":    true,
}
```

### Shell 命令安全检查

适用工具：`shell`

```
CommandSafetyCheck(command)
  │
  ├── 1. 危险模式匹配
  │     → 匹配 dangerousPatterns → RiskHigh
  │
  ├── 2. 已知安全命令快速通道
  │     → git status, git log, ls, cat, echo, pwd, which, env...
  │     → RiskLow
  │
  ├── 3. 默认分类
  │     → 包含写入类命令 (rm, mv, cp, chmod, chown...) → RiskMedium
  │     → 其他 → RiskMedium
  │
  └── 4. 返回 CommandCheckResult
```

**已知安全命令列表**：

```go
var knownSafeCommands = map[string]bool{
    "git":       true, // git status, git log, git diff, git branch...
    "ls":        true,
    "cat":       true,
    "head":      true,
    "tail":      true,
    "echo":      true,
    "pwd":       true,
    "which":     true,
    "where":     true,
    "env":       true,
    "printenv":  true,
    "whoami":    true,
    "hostname":  true,
    "date":      true,
    "uname":     true,
    "df":        true,
    "du":        true,
    "wc":        true,
    "sort":      true,
    "uniq":      true,
    "diff":      true,
    "test":      true,
    "go":        true, // go test, go build, go vet...
    "cargo":     true,
    "python":    true, // python -c "print(...)" 相对安全
    "node":      true, // node -e "console.log(...)"
}
```

**注意**：`knownSafeCommands` 是首命令级别的白名单，匹配逻辑为取命令行的第一个 token 进行检查。这是粗粒度的快速通道——即使首命令安全，后续参数仍可能构成危险（如 `git clean -fdx`），但这类情况交由 `dangerousPatterns` 的细粒度匹配兜底。

---

## 规则持久化

### 配置文件格式

配置文件位置：`.waveloom/settings.json`（项目级）或 `~/.waveloom/settings.json`（用户级）

```json
{
  "permissions": {
    "allow": [
      "read_file",
      "search_file",
      "grep",
      "ls",
      "Bash(git *)",
      "Bash(go *)",
      "Bash(cargo *)"
    ],
    "deny": [
      "Bash(rm -rf /*)",
      "Bash(curl *| sh)"
    ],
    "ask": [
      "write_file",
      "edit_file"
    ]
  }
}
```

### 规则解析

```go
// ParseRule 解析规则字符串。
// 格式：ToolName 或 ToolName(pattern)
// 示例：
//   "read_file"         → Rule{ToolName: "read_file", Pattern: ""}
//   "Bash(git *)"       → Rule{ToolName: "shell", Pattern: "git *"}
//   "write_file(src/**)" → Rule{ToolName: "write_file", Pattern: "src/**"}
//
// 兼容性：
//   "Bash(...)" 自动映射为 "shell(...)"（Claude Code 命名兼容）
func ParseRule(s string, behavior RuleBehavior) (Rule, error)
```

### 规则匹配

- **工具级规则**：精确匹配工具名
- **内容级规则**：
  - 对 `shell` 工具：pattern 匹配命令字符串（glob 前缀匹配）
  - 对文件工具（`read_file`、`write_file`、`edit_file`）：pattern 匹配 `file_path` 参数（glob 路径匹配）
  - 对 `search_file`、`grep`：pattern 匹配搜索路径参数
  - 对其他工具：忽略 pattern，只做工具级匹配

匹配使用 Go 标准库 `path.Match`（glob 模式）：

```go
// matchContent 检查内容级规则是否匹配工具输入。
func matchContent(toolName, pattern string, input json.RawMessage) bool
```

---

## 默认策略

### 工具风险分类与默认决策

| 工具 | 风险分类 | ConcurrentSafe | 默认决策 | 说明 |
|------|---------|---------------|---------|------|
| `read_file` | read | ✅ | allow | 只读，默认允许 |
| `search_file` | read | ✅ | allow | 只读，默认允许 |
| `grep` | read | ✅ | allow | 只读，默认允许 |
| `ls` | read | ✅ | allow | 只读，默认允许 |
| `write_file` | write | ❌ | ask | 写入，需确认 |
| `edit_file` | write | ❌ | ask | 编辑，需确认 |
| `shell` | execute | ❌ | ask | 执行，需确认 |

### Bypass 模式

在 CI/测试/自动化场景下，可通过 `--bypass-permissions` CLI 参数或 `WAVELOOM_BYPASS_PERMISSIONS=true` 环境变量启用 bypass 模式。

bypass 模式下：
- 所有工具调用自动 allow
- 安全检查仍执行但仅记录，不拦截
- 启动时必须输出警告信息

---

## 会话记忆机制

当用户选择 "Yes, and don't ask again" 时，该决策被记入 session 规则：

```go
// Session 记忆的 key 生成策略：
// - 工具级：sessionKey{ToolName: "write_file", Pattern: ""}
//   → 整个 write_file 工具在 session 内自动 allow
//
// - 内容级：sessionKey{ToolName: "shell", Pattern: "git *"}
//   → shell 工具中以 "git " 开头的命令在 session 内自动 allow

func (g *GuardImpl) rememberSession(key sessionKey, decision Decision) {
    g.mu.Lock()
    defer g.mu.Unlock()
    g.sessionMemory[key] = decision
}
```

### 记忆查找优先级

1. 内容级 session 记忆（`shell` + `git *`）→ 精确匹配
2. 工具级 session 记忆（`shell`）→ 宽泛匹配
3. 继续走规则检查流程

---

## 拒绝跟踪

参考 Claude Code 的 `denialTracking`，防止 Agent 在被拒绝后反复尝试同一操作。

```go
// DenialTracker 跟踪连续拒绝。
type DenialTracker struct {
    mu              sync.Mutex
    consecutive     int // 连续拒绝次数
    total           int // 总拒绝次数
    maxConsecutive  int // 连续拒绝上限，默认 3
    maxTotal        int // 总拒绝上限，默认 10
}

// RecordDenial 记录一次拒绝。
// 返回 true 表示已达上限，应回退到更强制的策略（如终止循环）。
func (d *DenialTracker) RecordDenial() bool

// RecordAllow 记录一次允许，重置连续计数。
func (d *DenialTracker) RecordAllow()
```

当连续拒绝达到 `maxConsecutive` 时：
- 在 `DecisionResult.Message` 中附加警告
- Loop 可以选择终止当前 turn 或强制弹窗

---

## 与 Wave 2 的衔接

### shell.go 的 dangerousPatterns 升级

Wave 2 中 `shell.go` 的 `dangerousPatterns` 是软限制（仅警告），Wave 3 将升级为硬拦截：

| 变化 | Wave 2 | Wave 3 |
|------|--------|--------|
| 危险模式匹配 | 警告（warnings） | 拦截（deny/ask） |
| 匹配位置 | Shell.Execute 内部 | Guard.Check 中 |
| 输出 | "⚠️ Security warnings" | DecisionResult.Deny |

升级方式：
1. 将 `dangerousPatterns` 和 `DangerousCommandPattern` 类型从 `pkg/tool/shell.go` 移到 `pkg/permission/command_safety.go`
2. `shell.go` 中保留 `dangerousPatterns` 但删除警告逻辑（Wave 3 由 Guard 接管）
3. Guard 的 CommandSafetyCheck 复用这些模式定义

### path_safety.go 的升级

Wave 2 中 `path_safety.go` 提供了基础路径安全检查（`IsWithinDir`、`IsBlockedDevicePath`、`IsBinaryFile` 等），Wave 3 在此基础上扩展：

| 保留（工具仍需要） | 新增（权限层需要） |
|---|---|
| `ResolvePath` | `PathSafetyCheck` |
| `IsWithinDir` | 敏感路径列表 |
| `IsBlockedDevicePath` | 操作类型叠加决策 |
| `IsBinaryFile` | `ClassifierSafe` 标记 |
| `ShouldSkipDir` | — |

---

## 文件结构

```
pkg/permission/
├── guard.go              # Guard 接口 + GuardImpl 主结构
├── guard_test.go         # Guard 核心逻辑测试
├── types.go              # Decision, DecisionReason, Rule, RuleEntry 等类型
├── types_test.go         # 类型测试
├── rule_engine.go        # 规则匹配引擎
├── rule_engine_test.go   # 规则匹配测试
├── rule_parser.go        # 规则字符串解析
├── rule_parser_test.go   # 解析测试
├── rule_loader.go        # 从配置文件加载规则
├── rule_loader_test.go   # 加载测试
├── path_safety.go        # 路径安全检查
├── path_safety_test.go   # 路径安全测试
├── command_safety.go     # 命令安全检查（dangerousPatterns 迁移至此）
├── command_safety_test.go# 命令安全测试
├── session_memory.go     # 会话记忆
├── session_memory_test.go# 会话记忆测试
├── denial_tracker.go     # 拒绝跟踪
└── denial_tracker_test.go# 拒绝跟踪测试
```

### 修改已有文件

| 文件 | 修改内容 |
|------|---------|
| `pkg/agentloop/loop.go` | Config 增加 `Guard` 和 `UserResponder` 字段；`executeToolCalls` 增加权限检查逻辑 |
| `pkg/agentloop/loop_test.go` | 测试更新：mock Guard + UserResponder |
| `pkg/tool/shell.go` | 移除 `dangerousPatterns` 警告逻辑（保留模式定义供迁移） |
| `pkg/tool/shell.go` | 移除 "Wave 3 将实施硬拦截" 的注释 |

---

## 不变量

1. **决策完整性**：每次工具调用前必须经过 `Guard.Check()`，不允许跳过
2. **deny 不可覆盖**：deny 规则优先级最高，allow 规则不能覆盖 deny
3. **安全检查不可 bypass**：`ReasonSafety` 的 deny 决策不受 bypass 模式影响
4. **规则顺序**：同一来源内规则按声明顺序匹配，先匹配先生效
5. **来源优先级**：deny > ask > allow（deny 规则总是优先检查）
6. **会话记忆不持久化**：session 级规则仅存在于进程生命周期内
7. **拒绝上限**：连续拒绝 ≥ 3 次时，Loop 应终止当前 turn 或返回错误给 LLM
8. **路径标准化**：所有路径比较前必须 ResolvePath + EvalSymlinks
9. **并发安全**：Guard 的所有方法必须是 goroutine-safe

---

## 错误分类

| 错误 | 分类 | 说明 |
|------|------|------|
| 规则解析失败 | 配置加载错误 | 启动时报告，不阻塞启动（忽略无效规则） |
| 配置文件不存在 | 正常 | 使用空规则集 + 默认策略 |
| 配置文件无读权限 | 警告 | 使用空规则集 + 默认策略 |
| 规则持久化失败 | 警告 | session 级不持久化无所谓；config 级失败需通知用户 |

---

## 测试计划

### 单元测试

| 测试文件 | 覆盖 | 关键场景 |
|---------|------|---------|
| `guard_test.go` | Guard.Check 全流程 | deny 规则优先 → ask 规则 → 安全检查 → allow 规则 → session 记忆 → bypass → 默认策略 |
| `rule_engine_test.go` | 规则匹配 | 工具级匹配、内容级 glob 匹配、多规则冲突、优先级 |
| `rule_parser_test.go` | 规则解析 | `ToolName`、`ToolName(pattern)`、括号转义、Bash→shell 兼容 |
| `rule_loader_test.go` | 配置加载 | 有效配置、无效规则跳过、缺失配置、多来源合并 |
| `path_safety_test.go` | 路径安全 | 工作目录内/外、敏感路径、危险路径、符号链接、操作类型叠加 |
| `command_safety_test.go` | 命令安全 | 危险模式匹配、已知安全命令、默认分类 |
| `session_memory_test.go` | 会话记忆 | 记住/查找/过期（进程结束） |
| `denial_tracker_test.go` | 拒绝跟踪 | 连续拒绝上限、重置、总上限 |

### 集成测试

| 场景 | 描述 |
|------|------|
| Loop + Guard 集成 | 验证 Loop 在执行工具前调用 Guard.Check；deny 时 LLM 收到拒绝消息 |
| Guard + UserResponder | ask 决策时回调 UserResponder；用户选择 allow 并记住 |
| 配置文件端到端 | 写入 settings.json → 启动 Guard → 规则生效 |
| bypass 模式 | 启用 bypass → 所有操作 allow → 安全检查仍记录 |

### Cold Agent 验收测试

验收标准：启动 cold agent（无本项目上下文），要求其：

1. 阅读 `pkg/permission/` 下所有代码
2. 验证 Guard.Check 的决策流程符合本规格书
3. 验证 deny 规则优先级高于 allow 规则
4. 验证路径安全检查覆盖所有敏感路径
5. 验证命令安全检查覆盖所有危险模式
6. 验证与 Agent Loop 的集成点正确
7. 验证并发安全性（Guard 方法可被多个 goroutine 同时调用）

---

## 后续扩展

| 扩展 | 优先级 | 说明 |
|------|------|------|
| 沙箱执行 | P2 | macOS Seatbelt / Linux seccomp，在 OS 层面隔离工具执行 |
| AI 分类器 | P2 | 调用轻量 LLM 自动判断操作安全性，减少用户弹窗 |
| Guardian 自动审核 | P2 | Codex 模式，子 Agent 代为审批 |
| 网络策略 | P2 | MCP / WebFetch 的域名级权限控制 |
| 远程权限桥接 | P2 | 分布式场景下权限决策的路由（Server 模式已通过通知+请求实现基本桥接） |
| Hook 系统 | P2 | PreToolUse / PostToolUse 钩子拦截权限决策 |
| Hook 系统 | P2 | PreToolUse / PostToolUse 钩子拦截权限决策 |
