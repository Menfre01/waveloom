# Security Policy

## 报告漏洞

如果你发现安全漏洞，请**不要**提交公开 Issue。

请发送邮件至 menfre@proton.me，以 PGP 加密为佳。

我们会在 48 小时内确认收到报告，并在修复后公开致谢（如果你希望匿名请注明）。

## 支持版本

| 版本 | 支持状态 |
|------|----------|
| v0.1.x (alpha) | ✅ 安全修复 |

## 安全检查清单

- Waveloom 不会静默执行命令或修改文件 —— 所有写操作默认需要用户确认
- `--bypass-permissions` 仅应在受信任的 CI 环境中使用
- 请勿将 `settings.json` 中的 API Key 提交到公开仓库
- Shell 命令以当前用户身份运行，请确保 `make`、`go`、`npm` 等构建工具来自可信来源
