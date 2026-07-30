# 接入 IDE MCP Server

Waveloom 支持通过 MCP 协议连接 IntelliJ IDEA 或 VS Code 的 MCP Server，利用 IDE 的代码索引能力（符号查找、文件搜索、构建诊断等）替代 shell 命令。

## 为什么接入 IDE？

| 场景 | Shell 方案 | IDE MCP 方案 |
|------|-----------|-------------|
| 文件搜索 | `find` / `grep`(遍历磁盘) | IDE 索引(毫秒级,自动排除 `node_modules`) |
| 符号查找 | `grep`（文本匹配） | PSI/LSP 精确语义查找 |
| 当前文件 | 需要 `@` 引用 | IDE 直接提供打开文件列表 |

## 支持的功能

| 功能 | IntelliJ IDEA | VS Code |
|------|:---:|:---:|
| 静态能力引导（工具使用提示） | ✅ | ✅ |
| 动态上下文注入（打开文件列表） | ✅ | ❌ * |
| 项目工作区匹配（CWD 校验） | ✅ | ✅ |

*VS Code MCP Server 没有列出所有打开文件的工具，但会通过 `get_workspace_folders` 校验 workspace 是否匹配。

## IntelliJ IDEA

**前置条件**：IntelliJ IDEA 2025.2+，MCP Server 插件已启用（默认启用）。

### 1. 启用 MCP Server

1. 打开 IDEA → `Settings | Tools | MCP Server`
2. 点击 `Enable MCP Server`
3. 在 `Manual Client Configuration` 区域，点击 `Copy Stdio Config`

### 2. 配置 Waveloom

将复制的配置粘贴到项目根目录的 `.mcp.json`：

```json
{
  "mcpServers": {
    "idea": {
      "type": "stdio",
      "command": "/Applications/IntelliJ IDEA.app/Contents/MacOS/idea",
      "args": ["mcp-server"]
    }
  }
}
```

> **注意**：command 和 args 以 IDEA 的 `Copy Stdio Config` 输出为准，不同平台路径不同。
>
> - macOS: `/Applications/IntelliJ IDEA.app/Contents/MacOS/idea`
> - Windows: `idea64.exe`
> - Linux: `/usr/local/bin/idea`

### 3. 验证

启动 Waveloom，检查 system prompt 中是否出现 `## IDE Integration — IntelliJ IDEA` 段落。

---

## VS Code

**前置条件**：VS Code + 社区扩展 `nabheet.vscode-ide-mcp`。

### 1. 安装扩展

```sh
code --install-extension nabheet.vscode-ide-mcp
```

或在 VS Code Marketplace 搜索安装 `vscode-ide-mcp`。

### 2. 配置 Waveloom

在项目根目录的 `.mcp.json` 中添加：

```json
{
  "mcpServers": {
    "vscode": {
      "type": "sse",
      "url": "http://127.0.0.1:9876/mcp"
    }
  }
}
```

扩展默认监听 `http://127.0.0.1:9876`，无需额外配置。

### 3. 验证

```sh
curl -s -X POST http://127.0.0.1:9876/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

启动 Waveloom，检查 system prompt 中是否出现 `## IDE Integration — VS Code` 段落。

---

## 工作原理

- **静态注入**：首次启动 Waveloom 时，检测已连接的 IDE 类型，将工具使用指导注入 system prompt（不破坏前缀缓存）
- **动态上下文**：后续迭代中将支持每轮查询 IDE 当前打开文件等信息，注入到 user 消息中
- **MCP 工具隔离**：IDE 工具以 `mcp__<server>__<tool>` 格式注册，与 Waveloom 内置工具互不冲突
