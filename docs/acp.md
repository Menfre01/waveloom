# ACP (Agent Client Protocol)

Waveloom 以 **Agent** 身份实现 [Agent Client Protocol (ACP) v1](https://agentclientprotocol.com)，
通过标准化的 JSON-RPC 2.0 over stdio（行分隔消息）与 ACP Client（如 Zed）通信。
实现对齐官方 `agent-client-protocol schema/v1/schema.json`，协商协议版本为 `1`。

本文档是 **Waveloom 的 ACP 兼容性清单**：声明了什么、实现了什么、哪些官方能力未支持。
客户端接入前请先核对本表。

## 启动方式

```bash
waveloom acp [options]
```

| Flag | 默认 | 说明 |
|------|------|------|
| `--session-dir` | `~/.waveloom/acp-sessions` | ACP session 持久化目录 |
| `--settings` | 自动探测 | 显式指定 settings.json 路径 |
| `--model` / `--provider` | settings | 覆盖 LLM 模型 / Provider |
| `--context-limit` | settings → 1M | 上下文窗口 token 上限（支持 `1M`/`200k`） |
| `--log-level` | `info` | 日志级别（error/warn/info/debug，写入 `~/.waveloom/logs`） |

> ACP 为无交互入口：沙箱自动激活（即使 `sandbox.enabled=false`），权限走二元决策（见下文）。

## 请求方法兼容表（Client → Agent）

| 方法 | 支持 | 说明 |
|------|:---:|------|
| `initialize` | ✅ | 协商 `protocolVersion: 1`；握手前置——其他所有方法必须先 initialize |
| `session/new` | ✅ | `cwd` + `mcpServers`（stdio/http/sse 三种变体，见 MCP 章节） |
| `session/prompt` | ✅ | text / resource / resource_link 内容块；同 session 串行执行，重复 prompt 返回 `-32001` |
| `session/cancel` | ✅ | 取消活跃 prompt；成功无响应（按通知语义实现），参数错误/会话不存在仍返回错误；与 prompt 启动的竞态由 `cancelPending` 标志兜底 |
| `$/cancel_request` | ✅ | LSP 风格按 requestId 取消（通知，无响应）；未知 requestId 静默忽略 |
| `session/close` | ✅ | 取消活跃 prompt、关闭 per-session MCP、移除注册表 |
| `session/list` | ✅ | 返回进程内 + 磁盘持久化 session |
| `session/load` | ✅ | 从磁盘恢复 session 并回放消息历史（session/update 通知形式） |
| `session/resume` | ✅ | 从磁盘恢复 session，不回放历史 |
| `session/delete` | ✅ | 移除注册表 + 删除磁盘 session 文件 |
| `session/request_permission` | ❌ | Agent → Client 方向，**未实现**（ACP 无权限确认通道，见权限章节） |

## initialize 能力声明

| 能力 | 声明值 | 说明 |
|------|:---:|------|
| `agentCapabilities.loadSession` | `true` | 支持 session/load 与 session/resume |
| `promptCapabilities.image` | `false` | 不支持图片输入 |
| `promptCapabilities.audio` | `false` | 不支持音频输入 |
| `promptCapabilities.embeddedContext` | `true` | 支持 resource 内嵌块与 resource_link 文件读取 |
| `mcpCapabilities.http` | `true` | `session/new` 的 mcpServers 支持 `type:"http"` |
| `mcpCapabilities.sse` | `true` | `type:"sse"` 映射为 http 处理（Waveloom 既有 SSE 处理方式） |
| `mcpCapabilities.stdio` | 隐式 | ACP v1 规定 stdio 为 All Agents MUST（无声明字段），已支持 |
| `sessionCapabilities` | resume/close/list/delete | 全量声明 |
| `auth` / `authMethods` | terminal | 终端认证:客户端以 base 启动配置追加 `args` 启动 `waveloom acp setup` 交互式向导,退出码 0 表示成功 |
| `agentInfo` | `waveloom` + 构建版本 | 标识实现 |

## 通知兼容表（Agent → Client，`session/update` 变体）

| 变体 | 支持 | 说明 |
|------|:---:|------|
| `available_commands_update` | ✅ | `session/new` 后发送斜杠命令面板（命令列表见下） |
| `session_info_update` | ✅ | 首条 prompt 时发送标题（截断至 60 字符） |
| `user_message_chunk` | ✅ | 回显用户消息（含 resource/resource_link 展开后的实际文本） |
| `agent_message_chunk` | ✅ | 流式正文（稳定 messageId） |
| `agent_thought_chunk` | ✅ | 思考链（独立 messageId，不与正文混用） |
| `plan` | ✅ | plan 条目（防御性映射；ACP 模式未注册 plan 工具，实际不产生） |
| `tool_call` | ✅ | 工具开始：kind/status/title（含参数描述，如 `bash: ls -la`）/rawInput/content |
| `tool_call_update` | ✅ | 工具流式与结果：status、content（含 diff 块）、locations |
| `usage_update` | ✅ | `used`（压缩感知的当前上下文 tokens）/ `size`（窗口容量）；`cost` 未填充 |

## 工具调用

### ToolKind 映射

| Waveloom 工具 | ACP ToolKind |
|---------------|--------------|
| `read` | `read` |
| `edit` / `write` | `edit` |
| `bash` | `execute` |
| `web_search` | `search` |
| `web_fetch` | `fetch` |
| `agent` | `think` |
| 其他 | `other` |

### tool_call 内容项

| 类型 | 支持 | 说明 |
|------|:---:|------|
| `content` | ✅ | 文本内容块 |
| `diff` | ✅ | edit/write 的 DiffHunks 重建 `{path, oldText, newText}` |
| `terminal` | ❌ | 不产生 |
| `locations` | ✅ | edit/write 目标文件（同文件去重，取首个 hunk 起始行） |

### ACP 模式注册的工具

`read` / `edit` / `write` / `bash`（允许后台）/ `web_fetch` / `web_search` /
`kill_background_task` / `skill`（LLM 可主动调用）/ `agent`（子代理）/ `todo_create` / `todo_update`

**未注册**（依赖 UserResponder 的交互式工具，ACP 无交互必挂，从 schema 层杜绝提议）：
`ask_user_question` / `enter_plan_mode` / `exit_plan_mode`

### 斜杠命令

ACP 模式注册 `/help`、`/model`、`/provider`、`/skill`（user-invocable skills）。
TUI overlay 类命令（`theme`/`locale`/`rewind`/`new`）不注册。

## 错误码

| 错误码 | 语义 | 备注 |
|--------|------|------|
| `-32700` | Parse error | 超长行可恢复，回错误后继续（DoS 防护） |
| `-32600` | Invalid request | 含"initialize 前置"违规 |
| `-32601` | Method not found | |
| `-32602` | Invalid params | |
| `-32603` | Internal error | |
| `-32000` | AuthRequired | 对齐官方;未配置 LLM 时 `session/prompt` 返回,提示先完成终端登录流 |
| `-32001` | SessionBusy | **Waveloom 自定义**（官方 -32001 未占用）：同 session 重复 prompt / 重复 load |
| `-32002` | ResourceNotFound | 对齐官方：session 不存在 |

## 行为约定

- **权限(二元决策)**:ACP v1 无权限确认协议,入口自动 `EnableAutoAllow`——Guard 进入二元决策,
  ASK → ALLOW,仅 DENY/ALLOW 两态;deny 规则、RiskHigh、PathDangerous 硬拦截保留(fail-closed 底线)。
- **终端认证(Terminal Auth)**:`initialize` 声明 `authMethods`(type `terminal`,args `["setup"]`);
  未配置 API key 时 agent 仍正常启动(`waveloom acp` 不退出),`session/prompt` 返回 `-32000`
  AUTH_REQUIRED 引导客户端触发登录流;`/help` 等无需 LLM 的斜杠命令不受影响。登录完成后
  客户端重连并重新 initialize 即可使用。
- **沙箱**:无交互自动激活(即使配置关闭);后端不可用时默认警告 + 降级运行(二元决策不受影响);
  若配置 `failIfUnavailable: true` 且后端不可用则拒绝启动(Windows 平台不支持不阻断)。
- **上下文压缩**：与 TUI 同源（四层 watermark 压缩）；`usage_update.size` = 上下文窗口容量，
  `used` = 当轮上下文 tokens（压缩感知：有压缩减 TokensSaved，Tier 3 摘要后归零）。
- **session 持久化**：每个 session 一个 JSON 文件，prompt 完成后落盘；`session/load` 回放历史，
  `session/resume` 不回放。
- **MaxTurns**：ACP 模式默认不限制 turn 数。
- **安全边界**：`sessionId` 仅允许 `[A-Za-z0-9_-]` 且 ≤64 字符（防路径穿越）；
  `resource_link` 仅接受 `file://` 或绝对路径，且必须位于工作区内（防任意文件读入 LLM 上下文）。
- **MCP**：`mcpServers` 仅 `session/new` 携带（v1 语义）；stdio/http/sse 三种变体均支持，
  非法条目（缺 command/url、重复 name）整体报错（fail-closed）；load/resume 协议无 mcpServers 参数，
  恢复的 session 不带 MCP 工具。

## 未实现 / 降级清单

| 官方能力 | 状态 | 说明 |
|----------|:---:|------|
| prompt 图片/音频输入 | ❌ | 能力声明 `false` |
| `session/request_permission` | ❌ | 无权限 UI；二元决策替代 |
| `usage_update.cost` | ❌ | 字段未填充 |
| tool_call `terminal` 内容项 | ❌ | 不产生 |
| `session/cancel` 成功响应 | ⚠️ | 按通知语义实现(成功无响应);错误路径仍返回错误 |

## Zed 集成

Waveloom 已通过 ACP v1 接入 [Zed](https://zed.dev)(Agent Panel / Threads Sidebar 验证可用)。
waveloom 不在 ACP Registry 中,以 **Custom Agent** 方式注册(Agent Settings →
External Agents → Add Agent → Add Custom Agent,或直接编辑 settings 文件):

```json
{
  "agent_servers": {
    "waveloom": {
      "type": "custom",
      "command": "waveloom",
      "args": ["acp"]
    }
  }
}
```

`command` 需为 PATH 中的可执行名或绝对路径;如需指定工作目录/额外 flag,追加到 `args`
(如 `["acp", "--model", "deepseek-chat"]`)。

### 在 Zed 中可用的能力

| 能力 | 说明 |
|------|------|
| 新建线程 | Agent Panel / Threads Sidebar 新线程菜单选择 waveloom;可用 `agent: new external agent thread` 绑定快捷键 |
| 工具卡片 | title 直接显示参数描述(如 `bash: ls -la`);edit/write 的 diff 块与 locations 支持点击跳转 |
| 斜杠命令面板 | `/help`、`/model`、`/provider`、`/skill`(available_commands_update) |
| 上下文用量 | `usage_update`(used/size)同步上下文占用与窗口容量 |
| 会话导入 | Thread History → Import Threads 导入 waveloom 持久化 session(session/list + load) |
| MCP 转发 | Zed 配置的 MCP server 可经 ACP 转发(waveloom 支持 stdio/http/sse) |

> 配置边界:外部 Agent 的模型/认证/计费由 waveloom 自己负责(读 waveloom 的
> settings.json),与 Zed 的 LLM provider 配置相互独立。

### 调试

Zed 命令面板 `dev: open acp logs` 可查看 Zed ↔ waveloom 的完整 ACP 消息;
waveloom 自身日志在 `~/.waveloom/logs`。

## 参考

- 官方协议规范:<https://agentclientprotocol.com>(schema v1)
- Zed External Agents 文档:<https://zed.dev/docs/ai/external-agents>
- 实现源码:`pkg/acp/`(server / handler / transport / adapter / mcp)
- 入口:`cmd/waveloom/acp.go`、`cmd/waveloom/acp_command.go`
