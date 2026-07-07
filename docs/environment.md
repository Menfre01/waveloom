<p align="center">
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./environment.en.md">English</a>
</p>

---

# 环境配置

Agent 启动时会自动探测当前环境可用的编译器、运行时和构建工具（`go`、`python3`、`node`、`rustc`、`gcc`、`java` 等 20 项），并将结果注入 System Prompt 的 `## Environment` 节，告知模型当前可用命令。

> **Windows 用户**：Waveloom 依赖 [Git for Windows](https://git-scm.com/downloads/win) 提供的 `bash.exe` 执行 Shell 命令。安装 Git for Windows 后，Waveloom 会自动探测 `bash.exe` 路径（支持 `WAVELOOM_GIT_BASH_PATH` 环境变量覆盖）。

## tools 覆盖

如果某个工具安装了但不在 PATH 中，或想强制使用特定版本，可在 `settings.json` 中配置 `environment.tools`：

```json
{
    "environment": {
        "tools": {
            "go": "/opt/homebrew/bin/go",
            "python3": "/usr/local/bin/python3"
        }
    }
}
```

**合并规则**：

- 项目配置（`.waveloom/settings.json`）优先级高于全局配置（`~/.waveloom/settings.json`），同 key 项目覆盖全局；
- 一旦 key 出现在 `tools` 中，该工具的探针结果被忽略——即使 PATH 中包含同名的其他版本，Agent 也只使用配置的路径；
- 未命中配置的 key 仍由探针自动检测。

**典型场景**：

| 需求 | 配置方式 |
|------|---------|
| 工具不在 PATH 中，但已知路径 | 填入完整路径 |
| 系统装了多个版本，指定某个 | 填入目标版本的完整路径 |
| 代理或容器环境，PATH 无工具 | 逐一配置路径 |
| 不需要特殊覆盖 | 留空即可，自动探测 |
