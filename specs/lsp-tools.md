# LSP 工具系统 — Waveloom

## 定位

为 Waveloom 提供基于 LSP（Language Server Protocol）的代码智能工具：诊断信息、定义跳转、引用查找、悬浮文档。支持多语言，通过标准 LSP 协议与各语言官方 Language Server 通信。

LSP 工具让 agent 不再是"盲人摸象"——改代码后立即看到编译错误，跳转到第三方库的类型定义，追踪符号的所有引用点。这些是 terminal-based coding agent 的关键能力杠杆。

## 参考来源

| 来源 | 核心贡献 |
|------|---------|
| LSP 3.17 规范 | 协议类型定义、生命周期、JSON-RPC 传输 |
| gopls (Go) | Go 语言标准 LSP 实现，零配置即可启动 |
| rust-analyzer | Rust 事实标准，同样 JSON-RPC over stdio |
| typescript-language-server | TypeScript/JavaScript 生态 |
| pyright | Python 类型检查 LSP server |

## 设计原则

```
               ┌─────────────────────────────────┐
               │            Tool 接口              │
               │  lsp_diagnostic  lsp_definition   │
               │  lsp_references  lsp_hover        │
               └──────────────┬──────────────────┘
                              │
               ┌──────────────▼──────────────────┐
               │         Server Manager            │
               │  按文件扩展名路由到正确 LSP Server  │
               │  管理进程生命周期（懒启动/空闲回收） │
               └──────────────┬──────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
  ┌─────▼─────┐       ┌──────▼──────┐       ┌─────▼─────┐
  │   gopls   │       │rust-analyzer│       │  pyright  │  ...
  │  (stdio)  │       │  (stdio)    │       │  (stdio)  │
  └───────────┘       └─────────────┘       └───────────┘
```

**核心设计决策：**

1. **LSP 协议：语言无关** — 所有语言 Server 通过相同 JSON-RPC 协议通信，客户端只写一次
2. **Server 发现：配置驱动** — 文件扩展名 → LSP Server 命令的映射，内置默认值 + 用户可覆盖
3. **进程模型：懒启动 + 空闲回收** — 只在首次请求时启动 Server，5 分钟无活动后关闭
4. **并发模型：串行化请求** — 每个 LSP Server 一个 goroutine，JSON-RPC 请求串行发送，避免协议竞态
5. **文件同步：didOpen/didChange/didClose** — 诊断需要完整文件内容，Server 不读取磁盘

## 架构

### 组件划分

```
pkg/lsp/
├── protocol.go       # LSP 3.17 类型定义（仅所需子集）
├── client.go         # JSON-RPC over stdio 客户端
├── manager.go        # Server 进程管理器（启动/停止/路由）
├── tool_diagnostic.go    # lsp_diagnostic 工具实现
├── tool_definition.go    # lsp_definition 工具实现
├── tool_references.go    # lsp_references 工具实现
├── tool_hover.go         # lsp_hover 工具实现
├── tool_test.go          # 集成测试（使用真实 gopls）
└── config.go         # 语言配置映射
```

### Server Manager 生命周期

```
                    Start()
                       │
              ┌────────▼────────┐
              │   IDLE           │──── 5min 空闲 ────► Stop()
              └────────┬────────┘
                       │ 首次请求到达
              ┌────────▼────────┐
              │   STARTING       │  启动子进程 + initialize 握手
              └────────┬────────┘
                       │ initialize 成功
              ┌────────▼────────┐
              │   READY          │── 文件同步 + 请求处理
              └────────┬────────┘
                       │ Stop() 或进程崩溃
              ┌────────▼────────┐
              │   STOPPED        │── 清理，下次请求重新 Start
              └─────────────────┘
```

### JSON-RPC 通信模型

```json
// 请求: client → server
{"jsonrpc":"2.0","id":1,"method":"textDocument/definition","params":{...}}

// 响应: server → client
{"jsonrpc":"2.0","id":1,"result":{...}}

// 通知: server → client（无 id，无响应）
{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{...}}
```

关键约束：
- `Content-Length: <N>\r\n\r\n` 头部编码（非行分隔）
- 请求/响应 id 单调递增，client 自适应 server 的 id（某些 server 不从 1 开始）
- `initialize` 必须在所有其他请求之前完成
- `textDocument/didOpen` 必须在请求诊断/定义/etc 之前

## LSP 协议子集

### 必须实现的消息

| 消息 | 方向 | 用途 | 优先级 |
|------|------|------|--------|
| `initialize` | C→S | 能力协商 | P0 |
| `initialized` | C→S | 通知初始化完成 | P0 |
| `textDocument/didOpen` | C→S | 打开文件（同步内容给 Server） | P0 |
| `textDocument/didChange` | C→S | 文件内容变更（增量更新可选） | P0 |
| `textDocument/didClose` | C→S | 关闭文件 | P1 |
| `textDocument/diagnostic` | C→S | 拉取诊断（pull model，LSP 3.17） | P0 |
| `textDocument/definition` | C→S | 跳转到定义 | P1 |
| `textDocument/references` | C→S | 查找所有引用 | P2 |
| `textDocument/hover` | C→S | 悬浮文档/类型信息 | P2 |
| `textDocument/publishDiagnostics` | S→C | 推送诊断（push model，传统方式） | P0 |
| `shutdown` | C→S | 优雅关闭 | P1 |
| `exit` | C→S | 进程退出通知 | P1 |

### 类型定义

```go
// URI: LSP 使用 URI（file:///）标识文件，不是普通路径
type DocumentURI string // "file:///absolute/path/to/file.go"

type Position struct {
    Line      uint32 // 0-based
    Character uint32 // 0-based, UTF-16 code units
}

type Range struct {
    Start Position
    End   Position
}

type Location struct {
    URI   DocumentURI
    Range Range
}

type Diagnostic struct {
    Range    Range
    Severity DiagnosticSeverity // 1=Error, 2=Warning, 3=Info, 4=Hint
    Code     string             // 如 "missing_return"
    Source   string             // 如 "gopls"
    Message  string
}

type Hover struct {
    Contents MarkupContent // {Kind: "markdown", Value: "..."}
    Range    *Range
}
```

## 工具接口

### 1. lsp_diagnostic

获取指定文件的诊断信息（编译错误、警告、lint 提示）。

```
参数:
  file_path: string        # 文件绝对路径
  working_dir: string      # 工作目录（可选，用于 LSP Server 项目上下文）

返回:
  成功: 列出所有诊断，按严重级别分组，包含文件、行号、列号、消息
  失败: ToolError（文件不存在 / 无对应 LSP Server / Server 启动失败）
```

输出格式示例：
```
file.go:15:3: error: missing return statement
file.go:22:7: warning: unused variable 'x'
file.go:30:1: info: function 'helper' is unused
```

### 2. lsp_definition

跳转到光标位置的符号定义。

```
参数:
  file_path: string        # 文件绝对路径
  line: int                # 行号（0-based）
  character: int           # 列号（0-based）
  working_dir: string      # 工作目录（可选）

返回:
  成功: Location{URI, Range} 列表
  失败: ToolError（无符号 / Server 不支持）
```

### 3. lsp_references

查找符号的所有引用（包括定义）。

```
参数:
  file_path: string        # 文件绝对路径
  line: int                # 行号（0-based）
  character: int           # 列号（0-based）
  include_declaration: bool # 是否包含定义位置（默认 true）
  working_dir: string      # 工作目录（可选）

返回:
  成功: Location 列表（可能分页，限制 100 条）
  失败: ToolError
```

### 4. lsp_hover

获取光标位置符号的类型信息、文档注释。

```
参数:
  file_path: string        # 文件绝对路径
  line: int                # 行号（0-based）
  character: int           # 列号（0-based）
  working_dir: string      # 工作目录（可选）

返回:
  成功: Markdown 格式的类型签名 + 文档
  失败: ToolError
```

样例输出：
```
```go
func strings.HasPrefix(s, prefix string) bool
```

HasPrefix tests whether the string s begins with prefix.
```

## Server 配置

### 内置默认映射

```go
var defaultServerConfig = map[string]ServerConfig{
    ".go":   {Command: "gopls", Args: []string{}},
    ".mod":  {Command: "gopls", Args: []string{}},
    ".sum":  {Command: "gopls", Args: []string{}},
    ".rs":   {Command: "rust-analyzer", Args: []string{}},
    ".ts":   {Command: "typescript-language-server", Args: []string{"--stdio"}},
    ".tsx":  {Command: "typescript-language-server", Args: []string{"--stdio"}},
    ".js":   {Command: "typescript-language-server", Args: []string{"--stdio"}},
    ".jsx":  {Command: "typescript-language-server", Args: []string{"--stdio"}},
    ".py":   {Command: "pyright-langserver", Args: []string{"--stdio"}},
    ".pyi":  {Command: "pyright-langserver", Args: []string{"--stdio"}},
}
```

### 用户覆盖（settings.json）

```json
{
  "lsp": {
    "servers": {
      ".go": ["gopls", "-v"],
      ".ts": ["typescript-language-server", "--stdio"],
      ".custom": ["my-custom-ls", "--flag"]
    },
    "idle_timeout_sec": 300
  }
}
```

### 扩展名匹配规则

1. 取文件扩展名（含点，如 `.go`）
2. 在 `servers` 映射中精确匹配
3. 未匹配 → 回退到 `.go` 默认值（如无对应 Server 返回错误）
4. 复合扩展名（`.test.ts`）仅取最后一个 `.ts`

## Manager 实现细节

### 并发模型

```
┌──────────────────────────────────────┐
│            ServerManager             │
│                                      │
│  servers map[string]*serverEntry     │
│  mu      sync.RWMutex                │
│                                      │
│  GetOrCreate(ext) → *serverEntry     │
│    ├─ RLock 查找                     │
│    ├─ RUnlock, Lock 创建             │
│    └─ 懒启动 goroutine               │
│                                      │
│  serverEntry:                        │
│    cmd      *exec.Cmd                │
│    stdin    io.WriteCloser           │
│    stdout   io.ReadCloser            │
│    rpc      *jsonrpc.Client          │
│    state    State (IDLE/READY/...)   │
│    lastUsed time.Time                │
│    pending  map[ID]chan Response     │
│    mu       sync.Mutex               │
└──────────────────────────────────────┘
```

### 请求处理流程

```
1. 工具调用 Execute(filePath, ...)
2. ext = filepath.Ext(filePath)
3. entry = manager.GetOrCreate(ext)
4. entry.Lock()
5. if entry.state == IDLE: 启动子进程 + initialize 握手
6. if 文件未同步: 发送 textDocument/didOpen
7. entry.Unlock()
8. entry.rpc.Call("textDocument/diagnostic", params) → Response
9. entry.lastUsed = time.Now()
10. 解析 Response 为 ToolResult
```

### 空闲回收

```go
// 后台 goroutine，每 30s 扫描一次
func (m *ServerManager) reapLoop() {
    for range time.Tick(30 * time.Second) {
        for ext, entry := range m.servers {
            if time.Since(entry.lastUsed) > m.idleTimeout {
                m.stopServer(ext)
            }
        }
    }
}
```

## 集成点

### 与现有组件的关系

| 组件 | 集成方式 |
|------|---------|
| `pkg/tool` | 4 个工具实现 `TypedTool[P]` 接口，通过 `Wrap()` 注册 |
| `pkg/tool/registry.go` | `NewDefaultRegistry()` 注册 4 个 LSP 工具（条件编译：如 gopls 不存在则不注册） |
| `pkg/agentloop` | 不需要修改 — Loop 通过 Registry 统一调用工具 |
| `cmd/waveloom/main.go` | 初始化 `ServerManager`，传入 settings 覆盖配置 |

### 不变量

1. LSP Server 崩溃不影响 agent 主进程 — 请求返回错误，下次请求自动重启
2. 同一文件扩展名的所有请求共享同一个 LSP Server 实例
3. `didOpen` 发送完整文件内容（从磁盘读取），确保 Server 看到的始终是最新状态
4. 诊断结果不缓存 — 每次 `lsp_diagnostic` 调用都发起新请求

## 测试策略

### 单元测试

- `protocol_test.go`: LSP 类型 JSON 序列化/反序列化
- `config_test.go`: 扩展名解析、配置合并

### 集成测试（需要 gopls 在 PATH 中）

- `tool_test.go`: 使用临时 Go 项目，写入有错误的 `.go` 文件，调用 `lsp_diagnostic` 验证返回预期诊断
- 测试 `lsp_definition` 跳转到标准库函数
- 测试 `lsp_references` 查找定义和引用
- 集成测试标记 `//go:build integration`

### Mock 测试

- 使用内存 JSON-RPC pipe 模拟 LSP Server 行为，测试请求/响应匹配、超时处理、Server 崩溃恢复

## Wave 拆分

### Wave 1: 协议层 + Client（~2 天）

新增文件:
- `pkg/lsp/protocol.go` — LSP 类型定义
- `pkg/lsp/client.go` — JSON-RPC over stdio 客户端
- `pkg/lsp/client_test.go` — 单元测试

验收:
- 启动 gopls 子进程，完成 initialize 握手
- 发送 `textDocument/didOpen` → 收到 `textDocument/publishDiagnostics` 通知
- 发送 `textDocument/definition` → 解析 Location 响应

### Wave 2: Manager + 配置（~1 天）

新增文件:
- `pkg/lsp/manager.go` — Server 进程管理器
- `pkg/lsp/config.go` — 语言配置映射
- `pkg/lsp/manager_test.go` — 集成测试

验收:
- 多语言文件路由到正确 Server
- 懒启动 + 空闲回收正常工作
- Server 崩溃后自动恢复重连

### Wave 3: 四个工具（~1 天）

新增文件:
- `pkg/lsp/tool_diagnostic.go`
- `pkg/lsp/tool_definition.go`
- `pkg/lsp/tool_references.go`
- `pkg/lsp/tool_hover.go`
- `pkg/lsp/tool_test.go`

修改文件:
- `pkg/tool/registry.go` — 注册 LSP 工具

验收:
- 4 个工具在测试 Go 项目中返回正确结果
- 错误处理：无对应 LSP Server、文件不存在、Server 超时

### Wave 4: 用户配置 + 边界处理（~0.5 天）

修改文件:
- `cmd/waveloom/main.go` — 初始化 Manager + settings 覆盖

验收:
- settings.json 中自定义 LSP Server 覆盖默认映射
- 无 gopls 环境下优雅降级（工具列表不包含 LSP 工具）

## 风险与限制

| 风险 | 缓解措施 |
|------|---------|
| LSP Server 启动慢 | 首次请求时懒启动，后续请求无需等待 |
| 大项目诊断慢（如 Kubernetes） | 诊断请求设 30s 超时，超时返回部分结果 |
| UTF-16 列号转换 | Go 使用 `unicode/utf8` 包逐字符计算 UTF-16 code units |
| Server 内存泄漏/僵尸进程 | Manager 维护进程 ctx，空闲超时后 `cmd.Process.Kill()` |
| 用户未安装 LSP Server | `exec.LookPath` 检查，未找到的工具不注册，日志警告 |

## 未来扩展

- `lsp_completion` — 代码补全建议
- `lsp_rename` — 符号重命名
- `lsp_codeAction` — 快速修复（自动导入、错误修正）
- `lsp_formatting` — 代码格式化
- `lsp_symbol` — 工作区符号搜索
- `lsp_callHierarchy` — 调用层次结构
- Tree-sitter 降级 — 无 LSP Server 时的纯静态分析
