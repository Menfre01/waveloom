<p align="center">
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./faq.en.md">English</a>
</p>

---

# 常见问题

**Q: 运行 `waveloom` 提示 "command not found"？**

安装路径不在 PATH 中。预编译二进制安装到 `~/.local/bin`，确认该路径在 PATH 中：`export PATH="$HOME/.local/bin:$PATH"` 并写入 `~/.bashrc` 或 `~/.zshrc`。

**Q: 提示 "api_key is required"？**

未检测到 API Key。运行 `waveloom setup` 完成首次配置，或设置 `LLM_API_KEY` 环境变量。配置文件写入 `~/.waveloom/settings.json`。

**Q: macOS 提示 "无法验证开发者"？**

执行 `xattr -d com.apple.quarantine ~/.local/bin/waveloom` 移除隔离标记。

**Q: Windows 上运行 waveloom 提示需要 Git for Windows？**

Waveloom 依赖 Git Bash 提供 Unix 兼容的 shell 环境。请从 [git-scm.com](https://git-scm.com/downloads/win) 下载安装 Git for Windows（安装时选择默认选项即可）。若已安装在非标准路径，设置 `WAVELOOM_GIT_BASH_PATH` 环境变量指向 `bash.exe`。

**Q: 如何确认前缀缓存正在生效？**

TUI 底部状态栏显示缓存命中率。也可查看 `.waveloom/waveloom.log`（需启用 `--verbose`）中的 `cache_hit_tokens` 信息。

**Q: @ 文件引用在单次执行模式下能用吗？**

`@` 文件引用当前仅在 TUI 交互模式中支持。单次执行模式下将 `@pkg/foo.go` 当作普通文本处理。
