<p align="center">
  <a href="./faq.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>English</strong>
</p>

---

# Troubleshooting

**Q: "command not found" when running `waveloom`?**

The install path is not in PATH. Pre-built binaries install to `~/.local/bin` — ensure it's in PATH: `export PATH="$HOME/.local/bin:$PATH"` and add to `~/.bashrc` or `~/.zshrc`.

**Q: "api_key is required" error?**

No API Key detected. Run `waveloom setup` to complete first-time configuration, or set the `LLM_API_KEY` environment variable. Config is written to `~/.waveloom/settings.json`.

**Q: macOS "cannot verify developer"?**

Run `xattr -d com.apple.quarantine ~/.local/bin/waveloom` to remove the quarantine attribute.

**Q: "Git for Windows is required" error on Windows?**

Waveloom requires Git Bash for a Unix-compatible shell environment. Download and install Git for Windows from [git-scm.com](https://git-scm.com/downloads/win) (default options are fine). If already installed in a non-standard location, set the `WAVELOOM_GIT_BASH_PATH` environment variable pointing to `bash.exe`.

**Q: How can I verify prefix caching is working?**

The TUI footer status bar shows the cache hit rate. You can also check logs under `~/.waveloom/logs/` (default level info; `--log-level debug` for more detail) for `cache_hit_tokens` info.

**Q: Do @ file references work in one-shot mode?**

`@` file references are supported in both TUI interactive mode and one-shot mode; if expansion fails, the text is used as-is.

**Q: How does Waveloom verify code after edit/write?**

Waveloom automatically runs LSP diagnostics. Supported LSP servers: gopls (Go), rust-analyzer (Rust), typescript-language-server (TS/JS), clangd (C/C++). Other languages via `lsp.servers` in `settings.json`. Uninstalled servers are silently skipped. See [`lsp.en.md`](./lsp.en.md).

**Q: How do I add a custom LSP Server?**

Add an `lsp.servers` section to `settings.json`, keyed by file extension with `{"command": "..."}` . See [`lsp.en.md`](./lsp.en.md).
