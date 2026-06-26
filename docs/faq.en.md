<p align="center">
  <a href="./faq.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>English</strong>
</p>

---

# Troubleshooting

**Q: "command not found" when running `wvl`?**

The install path is not in PATH. Pre-built binaries install to `/usr/local/bin` by default — ensure it's in PATH. If installed to `~/.local/bin`, add `export PATH="$HOME/.local/bin:$PATH"` to `~/.bashrc` or `~/.zshrc`.

**Q: "api_key is required" error?**

No API Key detected. Run `wvl setup` to complete first-time configuration, or set the `LLM_API_KEY` environment variable. Config is written to `~/.waveloom/settings.json`.

**Q: macOS "cannot verify developer"?**

Run `xattr -d com.apple.quarantine /usr/local/bin/wvl` to remove the quarantine attribute.

**Q: How can I verify prefix caching is working?**

The TUI footer status bar shows the cache hit rate. You can also check `.waveloom/wvl.log` (requires `--verbose`) for `cache_hit_tokens` info.

**Q: LSP tools not working?**

Ensure the corresponding language server is installed and in PATH. For Go projects, install gopls: `go install golang.org/x/tools/gopls@latest`.

**Q: Do @ file references work in one-shot mode?**

`@` file references are currently only supported in TUI interactive mode. In one-shot mode, `@pkg/foo.go` is treated as plain text.
