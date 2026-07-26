# LSP 诊断

Waveloom 在每次 `edit` / `write` 后自动运行 LSP diagnostics，将编译错误和警告注入 tool output，让 LLM 即时发现并修正问题。

## 工作原理

```
edit / write 执行完成
  → 提取变更文件路径
  → 匹配 LSP Server（按文件扩展名）
  → 同步文件内容到 Server（didOpen / didChange）
  → 等待 Server 分析并推送诊断（2s 超时）
  → 格式化诊断结果并注入 tool output
  → LLM 在下一轮看到诊断信息
```

诊断在 tool output 中的格式：

```
## LSP Diagnostics

### `pkg/lsp/pathutil.go`
L13:5: error: undefined: err
L16:2: error: undefined: abs
```

## 支持的语言

| 语言 | LSP Server | 探测命令 | 备注 |
|------|-----------|---------|------|
| Go | `gopls` | `gopls version` | 内置，自动检测 |
| Rust | `rust-analyzer` | `rust-analyzer --version` | 内置，自动检测 |
| TypeScript / JavaScript | `typescript-language-server` | `typescript-language-server --version` | 内置，需 `npm install -g` |
| C / C++ | `clangd` | `clangd --version` | 内置，自动检测 |

其他语言通过 `settings.json` 配置。

## 配置

### 添加自定义 LSP Server

在 `~/.waveloom/settings.json` 或 `.waveloom/settings.json` 中添加 `lsp.servers` 段：

```json
{
  "lsp": {
    "servers": {
      ".py": {
        "command": "pyright-langserver",
        "args": ["--stdio"]
      },
      ".java": {
        "command": "jdtls"
      }
    },
    "idle_timeout_ms": 300000
  }
}
```

- `servers`：扩展名 → Server 配置。命令必须在 PATH 中或使用绝对路径。
- `idle_timeout_ms`：Server 空闲回收超时（默认 5 分钟，可选）。

### 禁用 LSP

如果不想使用 LSP 诊断，无需配置 — Waveloom 仅在探针检测到 LSP Server 时才启用。如果误装了但不想用，从 PATH 中移除对应命令即可。

## 设计决策

- **自动注入、零工具调用**：诊断在 edit/write 后自动追加到 tool output，不占 LLM 工具槽位
- **静默降级**：LSP Server 未安装、超时、崩溃时不影响编辑操作
- **最终一致**：首次 edit 某文件时诊断可能为空（Server 异步分析中），下次编辑同一文件时缓存已就绪
- **安全管线**：诊断文本经过 Unicode 清洗、注入扫描、外部数据标记、256KB 截断
