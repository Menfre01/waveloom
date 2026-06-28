# Tool System 组件规格书

## 组件定位

Tool System 是 Waveloom Code Agent 的**执行层**，负责工具的注册、发现、调度和结果格式化。它是 Agent Loop 操作文件系统和执行命令的唯一通道。

核心职责：
1. 工具注册与发现 — Registry 管理所有可用工具，对外暴露工具列表（供 Loop 发送给 LLM）
2. 参数校验 — 执行前验证参数格式（JSON Schema）
3. 执行调度 — 根据工具的 `ConcurrentSafe()` 标记分流并发/串行执行
4. 结果格式化 — 统一 ToolResult 格式，分类错误（Recoverable vs Fatal）

## 参考来源

- Claude Code: `tools/toolOrchestration.ts`, `tools/StreamingToolExecutor.ts`, `tools/toolExecution.ts`
- Codex CLI: `tools/src/tool_executor.rs`, `core/src/tools/parallel.rs`, `tools/src/router.rs`

两者的共同模式：
- 工具通过统一接口注册到 Registry
- JSON Schema 描述工具参数，发送给 LLM 做 function calling
- 执行结果统一格式化（文本输出 + 错误分类）
- 读操作可并发，写操作必须串行

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 工具接口 | 泛型 `TypedTool[P]` + 类型擦除 `Wrap` | 实现者获得编译期类型安全，Registry 仍操作单一接口 |
| 注册方式 | Registry.Register() 构造期注册 | 运行期不增删工具，避免竞争 |
| 参数校验 | JSON Schema 验证 | 标准做法，LLM 原生支持 |
| 结果格式 | text + meta 分离 | LLM 读 text，Loop 读 meta 做决策 |
| 错误模型 | ToolError 含 ErrorClass | Loop 据此决定继续还是终止 |
| 并发控制 | 工具声明，Loop 调度 | 工具只声明能力，调度策略由 Loop 决定 |
| 文件操作 | 直接使用 os/filepath 标准库 | Wave 2 不需要虚拟文件系统 |

## 组件边界

### 输入
- `context.Context` — 取消/超时信号
- 工具调用（名称 + JSON 参数）

### 输出
- `*ToolResult` — 执行结果（含错误分类和元数据）
- `error` — 仅在系统级错误时返回（如未知工具名）

### 依赖（接口，非具体实现）
- 无内部依赖（纯能力的封装）
- 工具实现依赖标准库（`os`, `io`, `os/exec`, `filepath`, `regexp`）

### 不纳入本组件
- 权限检查（属于 Permission & Safety 职责）
- 并发调度决策（Loop 负责分流，Tool 只声明 ConcurrentSafe）
- LSP 工具（详见 `specs/lsp-tools.md`）
- Web 工具（`web_fetch` 已实现，无独立 spec）
- MCP 工具代理（P2，待实施）

---

## 接口定义

### 两层接口设计

Tool System 采用**泛型 + 类型擦除**的两层设计：

```
┌─────────────────────────────────────────────┐
│                 Tool 接口                    │
│  Registry 存储和 Loop 调用的单一接口          │
│  Execute(ctx, json.RawMessage)              │
├─────────────────────────────────────────────┤
│              ErasedTool (struct)            │
│  包装 TypedTool[P]，实现 Tool 接口           │
│  负责 json.Unmarshal → 类型安全参数          │
├─────────────────────────────────────────────┤
│            TypedTool[P any] (泛型接口)       │
│  工具实现者关心，Execute(ctx, P)             │
│  P 是具体参数 struct，编译期类型安全          │
└─────────────────────────────────────────────┘
```

### TypedTool[P] — 工具实现者接口

```go
// TypedTool 是工具实现者关心的类型安全接口。
// P 是工具的参数结构体，例如 ReadFileParams。
type TypedTool[P any] interface {
    Name() string
    Description() string
    Schema() json.RawMessage           // JSON Schema for input parameters
    ConcurrentSafe() bool              // true → 可并行；false → 必须串行
    Execute(ctx context.Context, params P) (*ToolResult, error)
}
```

### Tool — 类型擦除后的统一接口

```go
// Tool 是 Registry 存储和 Loop 调用的统一接口。
// 每个 TypedTool[P] 通过 Wrap() 包装为 Tool，json.Unmarshal 由 ErasedTool 统一处理。
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    ConcurrentSafe() bool
    Execute(ctx context.Context, raw json.RawMessage) (*ToolResult, error)
}
```

### ErasedTool + Wrap — 类型擦除

```go
// ErasedTool 包装 TypedTool[P]，实现 Tool 接口。
// 在 Execute 中统一完成 json.Unmarshal，工具实现者永远不需要手写。
type ErasedTool struct {
    name           string
    desc           string
    schema         json.RawMessage
    concurrentSafe bool
    execute        func(ctx context.Context, raw json.RawMessage) (*ToolResult, error)
}

func (e *ErasedTool) Name() string             { return e.name }
func (e *ErasedTool) Description() string      { return e.desc }
func (e *ErasedTool) Schema() json.RawMessage  { return e.schema }
func (e *ErasedTool) ConcurrentSafe() bool     { return e.concurrentSafe }
func (e *ErasedTool) Execute(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
    return e.execute(ctx, raw)
}

// Wrap 将 TypedTool[P] 包装为 Tool。
// 这是唯一的 json.Unmarshal 调用的位置 — 所有工具实现者不再需要手写反序列化。
func Wrap[P any](t TypedTool[P]) *ErasedTool {
    return &ErasedTool{
        name:           t.Name(),
        desc:           t.Description(),
        schema:         t.Schema(),
        concurrentSafe: t.ConcurrentSafe(),
        execute: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
            var p P
            if err := json.Unmarshal(raw, &p); err != nil {
                return &ToolResult{
                    Error: &ToolError{
                        Class:   ErrorClassRecoverable,
                        Kind:    ErrKindInvalidArgs,
                        Message: fmt.Sprintf("invalid params for %s: %v", t.Name(), err),
                        Cause:   err,
                    },
                }, nil
            }
            return t.Execute(ctx, p)
        },
    }
}
```

**效果**：工具实现者的 `Execute` 签名就是它想要的 struct，零样板代码：

```go
type ReadFileParams struct {
    FilePath string `json:"file_path"`
    Offset   int    `json:"offset"`
    Limit    int    `json:"limit"`
}

type ReadFile struct{}

func (t *ReadFile) Name() string             { return "read_file" }
func (t *ReadFile) Description() string      { return "读取文件内容..." }
func (t *ReadFile) Schema() json.RawMessage  { return readFileSchema }
func (t *ReadFile) ConcurrentSafe() bool     { return true }

func (t *ReadFile) Execute(ctx context.Context, p ReadFileParams) (*ToolResult, error) {
    // p 已经是类型安全的 ReadFileParams，零 json.Unmarshal
    content, err := os.ReadFile(p.FilePath)
    if os.IsNotExist(err) {
        return &ToolResult{Error: &ToolError{
            Class: ErrorClassRecoverable, Kind: ErrKindFileNotFound,
            Message: fmt.Sprintf("file not found: %s", p.FilePath), Cause: err,
        }}, nil
    }
    // ...
}
```

### Registry 接口

```go
// Registry 管理所有已注册的工具。
type Registry interface {
    Register(t Tool)                 // 注册工具；重复名称会 panic（编程错误）
    List() []ToolSpec                // 返回所有工具规格（发给 LLM）
    Get(name string) (Tool, bool)    // 按名查找工具
    Execute(ctx context.Context, name string, input json.RawMessage) (*ToolResult, error)
}
```

### 核心类型

```go
// --- ToolResult ---

// ToolResult 封装工具执行结果
type ToolResult struct {
    Content    string     // 文本输出（发送给 LLM）
    Meta       ToolMeta   // 元数据（供 Loop 和其他组件使用）
    Error      *ToolError // nil → 成功；非 nil → 失败
    ToolCallID string     // LLM 工具调用 ID，由 Loop 填充（工具实现者不感知）
}

func (r *ToolResult) IsError() bool { return r.Error != nil }

// ToolMeta 携带结构化元数据
type ToolMeta struct {
    Duration time.Duration // 执行耗时
    FilePath string        // 操作涉及的文件路径（如有）
    ExitCode int           // shell 命令退出码（-1 表示不适用）
    LineCount int          // 输出行数
    ByteCount int          // 输出字节数
}
```

```go
// --- ToolError ---

// ErrorClass 区分错误的可恢复性
type ErrorClass int

const (
    ErrorClassRecoverable ErrorClass = iota // LLM 可以自行修正
    ErrorClassFatal                          // 必须终止
)

// ToolError 封装工具执行错误
type ToolError struct {
    Class   ErrorClass // 分类
    Kind    string     // "file_not_found", "permission_denied", "invalid_args" ...
    Message string     // 人类可读描述，会返回给 LLM
    Cause   error      // 原始 error，不对外暴露
}

func (e *ToolError) Error() string { return e.Message }
func (e *ToolError) Unwrap() error { return e.Cause }

// 预定义错误 Kind
const (
    // Recoverable — LLM 可以修正
    ErrKindFileNotFound    = "file_not_found"
    ErrKindNoResults       = "no_results"
    ErrKindInvalidArgs     = "invalid_args"
    ErrKindCommandFailed   = "command_failed"

    // Fatal — 不可恢复
    ErrKindPermissionDenied = "permission_denied"
    ErrKindDiskFull         = "disk_full"
    ErrKindUnknownTool      = "unknown_tool"
    ErrKindTimeout           = "timeout"
    ErrKindSecurityViolation = "security_violation"
)
```

```go
// --- ToolSpec ---
//
// ToolSpec 是 Tool 的轻量描述，发送给 LLM 做 function calling。
// JSON tag 对齐 DeepSeek/OpenAI API 的 tools[].function 格式。
type ToolSpec struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"` // JSON Schema 参数定义
}
```

---

## 内置工具

### 概览

```
┌───────────────┬─────────────────────┬──────────┬──────────────────┐
│ 工具           │ 职责                 │ 并发安全  │ 来源              │
├───────────────┼─────────────────────┼──────────┼──────────────────┤
│ read_file     │ 读取文件内容          │ 🟢 true  │ Claude Code: FileReadTool │
│ write_file    │ 创建/覆写文件，自动创建父目录 │ 🔴 false │ Claude Code: FileWriteTool│
│ edit_file     │ 基于 old_string 精确匹配的查找替换 │ 🔴 false │ Claude Code: FileEditTool │
│ shell         │ 执行 Shell 命令      │ 🔴 false │ Claude Code: BashTool     │
│ search_file   │ Glob 文件搜索         │ 🟢 true  │ Claude Code: GlobTool     │
│ grep          │ 正则内容搜索          │ 🟢 true  │ Claude Code: GrepTool     │
│ ls            │ 列出目录内容          │ 🟢 true  │ Claude Code: Bash ls      │
│ web_fetch     │ 获取在线文档/API      │ 🟢 true  │ 无参考（自研）             │
└───────────────┴─────────────────────┴──────────┴──────────────────┘

LSP 工具（lsp_diagnostic / lsp_definition / lsp_references / lsp_hover）
详见 `specs/lsp-tools.md`。
```

---

### 1. read_file — 读取文件

```
参考: Claude Code FileReadTool / Codex CLI read

功能:
  - 读取文件的全部或部分内容（offset + limit 可选）
  - 返回带行号的文本
  - 大文件截断提示（超过 N 行时截断并注明）
  - 二进制文件检测（拒绝读取或返回 hexdump）

参数 (JSON Schema):
  {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "文件绝对路径"
      },
      "offset": {
        "type": "integer",
        "description": "起始行号（1-based，可选）"
      },
      "limit": {
        "type": "integer",
        "description": "读取行数（可选，默认全部）"
      }
    },
    "required": ["file_path"]
  }

错误:
  ErrKindFileNotFound → file_not_found (Recoverable)
  ErrKindPermissionDenied → permission_denied (Fatal)

并发安全: 🟢 true（纯读操作）

并发安全说明: 只读不写，多个 read_file 可以同时执行。
```

### 1.1 read_file 执行流程

```
Input: {"file_path": "/path/to/file.go", "offset": 10, "limit": 50}

Step 1: 路径解析
  - filepath.Abs(path) → 解析为绝对路径
  - filepath.Clean → 清理 .. 和 .

Step 2: 文件检查
  - os.Stat → 获取文件信息
  - 检查是否为目录（是 → 返回错误）
  - 检测二进制（前 512 字节中 null 占比 > 30% → 返回错误）

Step 3: 读取内容
  - os.Open → 打开文件
  - 如果有 offset，逐行跳过前 offset-1 行
  - bufio.Scanner 逐行读取，附加前缀行号

Step 4: 格式化输出
  ```
  [1] package main
  [2]
  [3] import "fmt"
  ...
  ```
  如果内容被截断，追加: "... [truncated: X lines omitted]"

Step 5: 设置 ToolMeta
  - FilePath = 文件路径
  - LineCount = 实际返回的行数
  - ByteCount = 返回内容的字节数
```

---

### 2. write_file — 写入文件

```
参考: Claude Code FileWriteTool / Codex CLI write

功能:
  - 创建新文件或覆盖现有文件
  - 自动创建父目录（mkdir -p）
  - 返回写入内容的摘要（行数、字节数）

参数:
  {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "文件绝对路径"
      },
      "content": {
        "type": "string",
        "description": "要写入的文件内容"
      }
    },
    "required": ["file_path", "content"]
  }

错误:
  ErrKindPermissionDenied → permission_denied (Fatal)
  ErrKindDiskFull → disk_full (Fatal)
  ErrKindInvalidArgs → invalid_args (Recoverable, e.g. file_path is a directory)

并发安全: 🔴 false（写操作，修改文件系统状态）

并发安全说明:
  - 写操作会修改文件系统状态
  - 多个 write 并发可能导致竞态
  - 如果 write 和 read 并发，read 可能读到不完整的文件
```

### 2.1 write_file 执行流程

```
Input: {"file_path": "/path/to/file.go", "content": "package main\n..."}

Step 1: 路径解析
  - filepath.Abs(path)
  - filepath.Clean

Step 2: 父目录创建
  - filepath.Dir → 获取父目录
  - os.MkdirAll → 创建（如不存在）

Step 3: 写入
  - os.Create → 创建/截断文件
  - io.Copy → 写入内容
  - fsync → 确保落盘

Step 4: 返回摘要
  Content: "Wrote 15 lines (320 bytes) to /path/to/file.go"
```

---

### 3. edit_file — 精确编辑文件

```
参考: Claude Code FileEditTool

功能:
  - 基于字符串精确匹配的查找替换
  - old_string 必须唯一匹配（否则报错）
  - replace_all 模式（可选）
  - 保持原文件的缩进和换行符

参数:
  {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "文件绝对路径"
      },
      "old_string": {
        "type": "string",
        "description": "要替换的文本（必须精确匹配原始内容，含缩进）"
      },
      "new_string": {
        "type": "string",
        "description": "替换后的文本"
      },
      "replace_all": {
        "type": "boolean",
        "description": "是否替换所有匹配项（默认 false，只替换第一个）",
        "default": false
      }
    },
    "required": ["file_path", "old_string", "new_string"]
  }

错误:
  ErrKindFileNotFound → file_not_found (Recoverable)
  ErrKindInvalidArgs → "no match found" (Recoverable — LLM 可以调整 old_string 重试)
  ErrKindInvalidArgs → "multiple matches found" (Recoverable — LLM 需要提供更多上下文)
  ErrKindPermissionDenied → permission_denied (Fatal)

并发安全: 🔴 false（修改文件内容）
```

### 3.1 edit_file 执行流程

```
Input: {"file_path": "/path/to/file.go", "old_string": "func old() {}", "new_string": "func new() {}"}

Step 1: 路径解析 + 读取原文
  - os.ReadFile → 读取完整内容

Step 2: 匹配 old_string
  - strings.Count(content, old) → 0 次匹配 → "no match found"
  - strings.Count > 1 且 replace_all=false → "multiple matches found"
  - 1 次匹配 → 执行替换

Step 3: 执行替换
  - replace_all=true → strings.ReplaceAll
  - 否则 → strings.Replace(old, new, 1)

Step 4: 写入
  - os.WriteFile → 写入修改后的内容

Step 5: 返回 diff 摘要
  Content: "Replaced 1 occurrence in /path/to/file.go"
```

---

### 4. shell — 执行 Shell 命令

```
参考: Claude Code BashTool / Codex CLI shell

功能:
  - 在子进程中执行 Shell 命令
  - 设置执行超时（默认 120s）
  - 捕获 stdout 和 stderr
  - 返回退出码、输出内容和截断提示
  - 限制输出长度（默认 3000 行）

参数:
  {
    "type": "object",
    "properties": {
      "command": {
        "type": "string",
        "description": "要执行的 Shell 命令"
      },
      "working_dir": {
        "type": "string",
        "description": "工作目录（可选，默认项目根目录）"
      },
      "timeout_ms": {
        "type": "integer",
        "description": "超时时间（毫秒，默认 120000，最大 600000）"
      }
    },
    "required": ["command"]
  }

错误:
  ErrKindCommandFailed → command_failed (Recoverable, e.g. exit code != 0)
  ErrKindTimeout → timeout (Recoverable, LLM 可以调整参数重试)
  ErrKindPermissionDenied → permission_denied (Fatal, e.g. exec format error)
  ErrKindSecurityViolation → security_violation (Fatal, e.g. rm -rf /)

并发安全: 🔴 false
  - 命令可能有副作用（修改文件、启动进程、网络请求）
  - 多个 shell 并发可能互相干扰
```

### 4.1 shell 执行流程

```
Input: {"command": "go build ./...", "working_dir": "/path/to/project", "timeout_ms": 30000}

Step 1: 安全检查
  - 对命令进行模式匹配（危险命令拦截列表）
  - 检查 working_dir 在白名单内（后续 Permission Wave 增强）

Step 2: 构造命令
  - ctx, cancel := context.WithTimeout(ctx, timeout)
  - cmd := exec.CommandContext(ctx, "sh", "-c", command)
  - cmd.Dir = working_dir

Step 3: 执行
  - cmd.CombinedOutput() → 同时捕获 stdout + stderr

Step 4: 格式化输出
  - 退出码 0: "Command completed successfully.\n<piped output>"
  - 退出码 != 0: "Command exited with code 1.\n<stderr output>"
  - 超时: "Command timed out after 30s.\n<partial output>"
  - 超过 3000 行: 截断 + "... [truncated]"

Step 5: 设置 ToolMeta
  - ExitCode = cmd.ProcessState.ExitCode()
```

### 4.2 危险命令模式（初步）

```go
// dangerousPatterns 在 Wave 2 作为软限制（记录警告）。
// Wave 3（Permission）升级为硬拦截。
var dangerousPatterns = []string{
    `rm\s+-rf\s+/`,
    `mkfs`,
    `dd\s+if=`,
    `>\s*/dev/sd`,
    `:(){ :|:& };:`,  // fork bomb
    `chmod\s+777`,
    `curl.*\|.*sh`,
}
```

---

### 5. search_file — Glob 文件搜索

```
参考: Claude Code GlobTool / Codex CLI glob

功能:
  - 基于模式匹配（glob）搜索文件
  - 支持 ** 递归匹配
  - 不搜索隐藏目录（.git, node_modules 等）
  - 结果按路径排序

参数:
  {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "Glob 模式（如 **/*.go, *.md, src/**/*_test.go）"
      },
      "working_dir": {
        "type": "string",
        "description": "搜索起始目录（可选，默认项目根目录）"
      }
    },
    "required": ["pattern"]
  }

错误:
  ErrKindInvalidArgs → invalid_args (Recoverable, e.g. 非法 glob 模式)
  ErrKindNoResults → no_results (Recoverable, 未找到匹配文件)
  ErrKindPermissionDenied → permission_denied (Fatal, e.g. 无读权限)

并发安全: 🟢 true（纯读操作，不修改文件系统）
```

### 5.1 search_file 执行流程

```
Input: {"pattern": "**/*_test.go", "working_dir": "/path/to/project"}

Step 1: 解析 pattern
  - 验证 glob 模式合法性
  - 标准化路径分隔符

Step 2: 文件系统遍历
  - filepath.WalkDir → 遍历目录树
  - 跳过隐藏目录（.git, .claude, node_modules ...）
  - filepath.Match → 对每个文件名匹配 pattern
  - 若 pattern 含 **，用 doublestar.Match 递归匹配

Step 3: 格式化输出
  ```
  Found 15 files matching "**/*_test.go":
  pkg/agentloop/loop_test.go
  pkg/llm/client_test.go
  pkg/llm/retry_test.go
  ...
  ```

Step 4: 设置 ToolMeta
  - FilePath = pattern（搜索模式）
  - LineCount = 匹配文件数
```

---

### 6. grep — 正则内容搜索

```
参考: Claude Code GrepTool / Codex CLI grep

功能:
  - 在文件中搜索匹配正则表达式的行
  - 支持按 glob 过滤文件
  - 返回文件名、行号、匹配内容
  - 不搜索二进制文件

参数:
  {
    "type": "object",
    "properties": {
      "pattern": {
        "type": "string",
        "description": "正则表达式（RE2 语法）"
      },
      "include": {
        "type": "string",
        "description": "Glob 模式过滤文件（可选，如 *.go）"
      },
      "working_dir": {
        "type": "string",
        "description": "搜索起始目录（可选）"
      },
      "case_insensitive": {
        "type": "boolean",
        "description": "忽略大小写（默认 false）"
      },
      "context_lines": {
        "type": "integer",
        "description": "匹配行上下文的行数（可选，default 0）"
      }
    },
    "required": ["pattern"]
  }

错误:
  ErrKindInvalidArgs → invalid_args (Recoverable, e.g. 非法正则)
  ErrKindNoResults → no_results (Recoverable)
  ErrKindPermissionDenied → permission_denied (Fatal)

并发安全: 🟢 true（纯读操作）

并发安全说明: 只读文件，不修改状态。多个 grep 可以同时执行。
```

### 6.1 grep 执行流程

```
Input: {"pattern": "func.*Test", "include": "*_test.go", "working_dir": "/path/to/project"}

Step 1: 编译正则
  - regexp.Compile → RE2 正则校验
  - 非法正则 → ErrKindInvalidArgs

Step 2: 收集文件
  - 如果有 include → 先 search_file(include) 过滤
  - 无 include → 遍历项目 .go 文件

Step 3: 逐文件搜索
  - 对每个文件，bufio.Scanner 逐行读取
  - 正则匹配 → 记录文件名 + 行号 + 内容

Step 4: 格式化输出
  ```
  Found 12 matches for "func.*Test":
  pkg/llm/client_test.go:15    func TestSendMessageSuccess(t *testing.T) {
  pkg/llm/client_test.go:42    func TestRetryableErrorRetried(t *testing.T) {
  pkg/agentloop/loop_test.go:23 func TestRunCompletesImmediately(t *testing.T) {
  ...
  ```
```

---

### 7. ls — 列出目录内容

```
参考: Claude Code Bash ls / Codex CLI ls

功能:
  - 列出目录中的文件和子目录
  - 区分文件和目录（用 / 后缀标记目录）
  - 支持递归深度控制
  - 结果按名称排序

参数:
  {
    "type": "object",
    "properties": {
      "path": {
        "type": "string",
        "description": "目录路径（可选，默认项目根目录）"
      },
      "depth": {
        "type": "integer",
        "description": "递归深度（可选，默认 1）",
        "default": 1
      }
    },
    "required": []
  }

错误:
  ErrKindFileNotFound → file_not_found (Recoverable, e.g. 路径不存在)
  ErrKindPermissionDenied → permission_denied (Fatal)

并发安全: 🟢 true（纯读操作）
```

### 7.1 ls 执行流程

```
Input: {"path": "/path/to/project/pkg", "depth": 2}

Step 1: 路径检查
  - 验证路径存在且是目录

Step 2: 递归遍历
  - os.ReadDir → 读取目录条目
  - 排序（目录在前，文件在后，各自按字母排序）
  - 深度内递归子目录

Step 3: 格式化输出
  ```
  pkg/
    agentloop/
      loop.go
      loop_test.go
    llm/
      client.go
      types.go
      ...
    tool/
  ```

Step 4: 设置 ToolMeta
  - LineCount = 条目数
```

---

## 错误处理

### 错误分类规则

每个工具实现内部遵循统一的错误分类规则：

```go
// 工具错误分类指南:
//
// Recoverable — LLM 可以通过调整参数重试:
//   - 文件/路径不存在
//   - 模式匹配无结果
//   - 命令执行失败（非零退出码）
//   - 参数格式不正确
//   - 超时
//
// Fatal — 不可恢复，不应重试:
//   - 权限不足
//   - 磁盘满
//   - 未知的工具名称
//   - 安全策略违规
```

### 错误传递方式

```
Tool.Execute 返回 (*ToolResult, error):
  - 成功: ToolResult.Error == nil, error == nil
  - 工具级错误: ToolResult.Error != nil, error == nil
    → Loop 检查 Res.Error.Class 决定继续还是终止
  - 系统级错误: error != nil (e.g. 未知工具名)
    → Loop 直接终止

参数反序列化错误由 ErasedTool 统一处理:
  - json.Unmarshal 失败 → ErrorClassRecoverable, ErrKindInvalidArgs
  - 工具实现者不需要关心参数解析，Execute 收到的 P 总是合法的
```

### Registry.Execute 的错误转换

```go
func (r *registry) Execute(ctx context.Context, name string, input json.RawMessage) (*ToolResult, error) {
    tool, ok := r.Get(name)
    if !ok {
        return nil, fmt.Errorf("tool %q not registered", name)
    }

    // 参数校验 + json.Unmarshal 由 ErasedTool 内部完成
    return tool.Execute(ctx, input)
}
```

---

## Registry 实现

```go
// registry 是 Registry 接口的默认实现
type registry struct {
    tools map[string]Tool // 工具名 → ErasedTool（实现 Tool 接口）
    specs []ToolSpec      // 预构建的 ToolSpec 列表（避免每次 List 重建）
}

func NewRegistry() *registry {
    return &registry{
        tools: make(map[string]Tool),
    }
}

// Register 接受 Tool（即 ErasedTool），由外部通过 Wrap() 构造。
func (r *registry) Register(t Tool) {
    if _, exists := r.tools[t.Name()]; exists {
        panic(fmt.Sprintf("tool %q already registered", t.Name()))
    }
    r.tools[t.Name()] = t
    r.specs = append(r.specs, ToolSpec{
        Name:        t.Name(),
        Description: t.Description(),
        Parameters:  t.Schema(),
    })
}

func (r *registry) List() []ToolSpec {
    return r.specs
}

func (r *registry) Get(name string) (Tool, bool) {
    t, ok := r.tools[name]
    return t, ok
}
```

```go
// 构造 Registry 时，每个 TypedTool 先 Wrap 再注册
func NewDefaultRegistry() Registry {
    r := NewRegistry()
    r.Register(tool.Wrap(&ReadFile{}))
    r.Register(tool.Wrap(&WriteFile{}))
    r.Register(tool.Wrap(&EditFile{}))
    r.Register(tool.Wrap(&Shell{}))
    r.Register(tool.Wrap(&SearchFile{}))
    r.Register(tool.Wrap(&Grep{}))
    r.Register(tool.Wrap(&Ls{}))
    return r
}
```

---

## 文件操作安全性

### 路径沙盒（Wave 2 软限制 → Wave 3 硬限制）

```
所有文件操作工具（read_file, write_file, edit_file）:

Step 1: 路径标准化
  - filepath.Abs → 绝对路径
  - filepath.Clean → 清理 ./../

Step 2: 项目边界检查（软）
  - 检测路径是否在项目目录外
  - 如果在外部，记录警告但不阻止（Wave 2）
  - Wave 3 升级为硬阻止

Step 3: 符号链接处理
  - filepath.EvalSymlinks → 解析后再次检查边界
```

### 大文件保护

```
read_file:
  - 文件 > 1MB → 不全部读取，默认返回前 200 行
  - 消息中注明: "File is 2.3MB, showing first 200 lines"

write_file:
  - 内容 > 500KB → 拒绝写入，返回错误
  - 原因: 避免 LLM 意外生成超大文件

shell:
  - 输出 > 100KB → 截断，保留首尾各 50KB
```

---

## 不变量

1. **工具不可变性**: 工具注册后不可修改名称、描述、Schema（构造期注册）
2. **结果完整**: `ToolResult.Content` 始终可安全发送给 LLM（不含内部错误堆栈）
3. **错误不丢上下文**: `ToolError.Cause` 保留原始错误，供日志/调试使用
4. **Schema 合法**: 所有工具 Schema 必须可解析为合法 JSON Schema
5. **路径绝对化**: 所有文件操作前解析为绝对路径
6. **并发安全声明正确**: `ConcurrentSafe()=false` 的工具完全不能并发执行
7. **执行顺序无关**: `ConcurrentSafe()=true` 的工具之间不存在隐式依赖

---

## 测试计划

### Tool 接口测试

1. **TestToolInterface** — 每个工具满足 Tool 接口约定
2. **TestToolNameUnique** — 无重复工具名

### 内置工具测试

#### read_file
3. **TestReadFileSuccess** — 正常读取文件，返回带行号内容
4. **TestReadFileWithOffsetAndLimit** — offset + limit 正确截取
5. **TestReadFileNotFound** — 文件不存在 → Recoverable 错误
6. **TestReadFileBinary** — 二进制文件 → 拒绝读取
7. **TestReadFileTruncated** — 大文件自动截断

#### write_file
8. **TestWriteFileSuccess** — 正常写入文件
9. **TestWriteFileCreatesParentDir** — 自动创建父目录
10. **TestWriteFilePermissionDenied** — 无写权限 → Fatal

#### edit_file
11. **TestEditFileSuccess** — 正常替换
12. **TestEditFileNoMatch** — 无匹配 → Recoverable with "no match found"
13. **TestEditFileMultipleMatches** — 多处匹配 → Recoverable with "multiple matches"
14. **TestEditFileReplaceAll** — replace_all=true 全部替换

#### shell
15. **TestShellSuccess** — 命令执行成功（exit 0）
16. **TestShellNonZeroExit** — 非零退出码 → Recoverable
17. **TestShellTimeout** — 超时 → Recoverable
18. **TestShellOutputTruncation** — 输出过长被截断

#### search_file
19. **TestSearchFileSuccess** — 正常匹配文件
20. **TestSearchFileNoResults** — 无匹配 → Recoverable
21. **TestSearchFileRecursive** — ** 递归匹配
22. **TestSearchFileSkipsHiddenDirs** — 跳过 .git 等隐藏目录

#### grep
23. **TestGrepSuccess** — 正常匹配内容
24. **TestGrepNoResults** — 无匹配 → Recoverable
25. **TestGrepInvalidRegex** — 非法正则 → Recoverable
26. **TestGrepWithContextLines** — 上下文行正确返回
27. **TestGrepSkipsBinary** — 跳过二进制文件

#### ls
28. **TestListSuccess** — 正常列出目录
29. **TestListNotFound** — 路径不存在 → Recoverable
30. **TestListRecursiveDepth** — 递归深度控制正确

### Registry 测试

31. **TestRegistryRegister** — 注册工具成功
32. **TestRegistryRegisterDuplicate** — 重复注册 panic
33. **TestRegistryList** — List 返回所有已注册工具
34. **TestRegistryGet** — 按名获取
35. **TestRegistryGetNotFound** — 获取不存在工具返回 false
36. **TestRegistryExecuteUnknownTool** — 执行未知工具 → error
37. **TestRegistryExecuteInvalidArgs** — 参数校验失败 → Recoverable

### Mock 组件

- `mockTool` — 可编程控制 Name/Description/Schema/ConcurrentSafe/Execute 返回值
- 各工具测试使用真实文件系统（`t.TempDir()` 提供隔离环境）
- shell 测试使用安全的简单命令（`echo`, `ls`, `true`, `false`）

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/tool/tool.go` | TypedTool[P] 接口 + Tool 接口 + ErasedTool + Wrap + ToolResult + ToolMeta + ToolError |
| 新增 | `pkg/tool/registry.go` | Registry 接口 + 默认实现 + NewDefaultRegistry |
| 新增 | `pkg/tool/schema.go` | JSON Schema 参数校验 |
| 新增 | `pkg/tool/read_file.go` | read_file 工具实现 |
| 新增 | `pkg/tool/write_file.go` | write_file 工具实现 |
| 新增 | `pkg/tool/edit_file.go` | edit_file 工具实现 |
| 新增 | `pkg/tool/shell.go` | shell 工具实现 |
| 新增 | `pkg/tool/search_file.go` | search_file 工具实现 |
| 新增 | `pkg/tool/grep.go` | grep 工具实现 |
| 新增 | `pkg/tool/ls.go` | ls 工具实现 |
| 新增 | `pkg/tool/path_safety.go` | 路径安全检查（沙盒基础） |
| 新增 | `pkg/tool/tool_test.go` | Tool 接口 + Registry 测试 |
| 新增 | `pkg/tool/read_file_test.go` | read_file 测试 |
| 新增 | `pkg/tool/write_file_test.go` | write_file 测试 |
| 新增 | `pkg/tool/edit_file_test.go` | edit_file 测试 |
| 新增 | `pkg/tool/shell_test.go` | shell 测试 |
| 新增 | `pkg/tool/search_file_test.go` | search_file 测试 |
| 新增 | `pkg/tool/grep_test.go` | grep 测试 |
| 新增 | `pkg/tool/ls_test.go` | ls 测试 |

## 集成点

- 通过 `tool.Registry` 接口被 Agent Loop 调用
- 通过 `tool.TypedTool[P]` 接口供外部实现自定义工具
- `ToolSpec` 依赖 `encoding/json` 的 `json.RawMessage` 类型
- 不依赖其他 Waveloom 内部组件
- 文件工具使用标准库 `os`/`io`/`filepath`，shell 使用 `os/exec`
