# MCP (Model Context Protocol)

Waveloom 内置完整的 MCP 客户端，可连接外部 MCP Server 并自动发现和注册其工具。

## 配置来源

Waveloom 启动时从多个来源自动加载 MCP Server 配置，按优先级合并（高覆盖低）：

| 优先级 | 来源 | 说明 |
|--------|------|------|
| 6（最低） | Claude 桌面版 | 自动发现 Claude 桌面版已安装的 MCP Server |
| 5 | `~/.claude.json` → `mcpServers` | Claude Code 用户级配置 |
| 4 | `~/.claude.json` → `projects.<cwd>` | Claude Code 项目级配置 |
| 3 | `~/.waveloom.json` → `mcpServers` | Waveloom 用户级配置 |
| 2 | `~/.waveloom.json` → `projects.<cwd>` | Waveloom 项目级配置 |
| 1（最高） | `.mcp.json` | 项目根目录配置文件 |

> **注意**：同名 Server 会被高优先级配置覆盖。例如 `.mcp.json` 和 Claude 桌面版都定义了 `pencil`，则使用 `.mcp.json` 的配置。

## CLI 管理命令

```bash
# 添加 stdio 类型 Server（本地命令行程序）
waveloom mcp add --transport stdio [--scope user|local] <name> -- <command> [args...]

# 添加 HTTP 类型 Server（远程服务）
waveloom mcp add --transport http [--scope user|local] <name> <url>

# 从 JSON 添加（批量或复杂配置）
waveloom mcp add-json <name> '<json>'

# 查看所有已配置的 Server
waveloom mcp list

# 查看指定 Server 详情
waveloom mcp get <name>

# 删除 Server
waveloom mcp remove <name>
```

### 示例

```bash
# 添加本地 Pencil 设计工具（stdio）
waveloom mcp add --transport stdio --scope local pencil -- \
  /Applications/Pencil.app/Contents/Resources/app.asar.unpacked/out/mcp-server-darwin-arm64 \
  --app desktop

# 添加远程 HTTP Server
waveloom mcp add --transport http --scope user my-api https://api.example.com/mcp

# 带自定义 Header
waveloom mcp add --transport http --header "Authorization: Bearer token123" my-api https://api.example.com/mcp

# 添加环境变量
waveloom mcp add --transport stdio --env "NODE_ENV=production" my-tool -- npx my-mcp-server
```

### scope 参数

| 值 | 写入位置 | 生效范围 |
|----|---------|---------|
| `user`（默认） | `~/.waveloom.json` | 全局，所有项目 |
| `local` | `.mcp.json` | 仅当前项目 |

## 配置文件格式

### `.mcp.json` / `~/.waveloom.json`

```json
{
    "mcpServers": {
        "pencil": {
            "type": "stdio",
            "command": "/path/to/mcp-server",
            "args": ["--app", "desktop"],
            "env": {
                "NODE_ENV": "production"
            }
        },
        "my-api": {
            "type": "http",
            "url": "https://api.example.com/mcp",
            "headers": {
                "Authorization": "Bearer token123"
            }
        }
    }
}
```

### `~/.claude.json`（兼容 Claude Code）

```json
{
    "mcpServers": {
        "github": {
            "type": "http",
            "url": "https://api.githubcopilot.com/mcp/"
        }
    },
    "projects": {
        "/path/to/project": {
            "mcpServers": {
                "local-server": {
                    "type": "stdio",
                    "command": "node",
                    "args": ["server.js"]
                }
            }
        }
    }
}
```

## 环境变量展开

配置中的 `${VAR}` 和 `${VAR:-default}` 会自动展开：

```json
{
    "type": "http",
    "url": "https://api.example.com/mcp",
    "headers": {
        "Authorization": "Bearer ${MCP_API_KEY}"
    }
}
```

## 注册与工具命名

- MCP Server 连接成功后，通过 `tools/list` 协议发现其工具
- 每个工具注册到 Waveloom 的全局工具注册表，命名格式：`mcp__<server>__<tool>`
- 例如 Pencil 的 `batch_design` → `mcp__pencil__batch_design`
- LLM 在每次请求中看到所有工具（内置 + MCP），按需选择调用

## 故障排查

```bash
# 查看 MCP 连接日志
waveloom --verbose "test" 2>&1 | grep "\[mcp\]"

# 验证 Server 是否注册成功
waveloom mcp list

# 单独验证特定 Server
waveloom mcp get <name>
```

常见错误：

| 错误 | 原因 | 解决 |
|------|------|------|
| `executable file not found` | command 路径不存在或不在 `$PATH` | 使用绝对路径或确认命令已安装 |
| `connection refused` | HTTP Server 未启动或端口错误 | 检查 URL 和端口 |
| `4xx/5xx` | 认证失败或 Server 内部错误 | 检查 Headers/Token 或 Server 日志 |
