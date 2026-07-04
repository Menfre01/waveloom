<p align="center">
  <a href="./environment.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>English</strong>
</p>

---

# Environment Configuration

On startup, the agent probes the current environment for available compilers, runtimes, and build tools (19 entries including `go`, `python3`, `node`, `rustc`, `gcc`, `java`, etc.), then injects the results into the System Prompt's `## Environment` section so the model knows which commands are available.

> **Windows users**: Waveloom relies on [Git for Windows](https://git-scm.com/downloads/win) to provide `bash.exe` for shell command execution. After installing Git for Windows, Waveloom auto-detects the `bash.exe` path (override via `WAVELOOM_GIT_BASH_PATH` environment variable).

## Tools Override

If a tool is installed but not in PATH, or you want to force a specific version, configure `environment.tools` in `settings.json`:

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

**Merge rules**:

- Project config (`.waveloom/settings.json`) takes priority over global config (`~/.waveloom/settings.json`); for the same key, project wins;
- Once a key appears in `tools`, the probe result for that tool is ignored — even if another binary with the same name exists in PATH, the agent uses only the configured path;
- Tools not listed in `tools` are still auto-detected by probes.

**Common scenarios**:

| Need | Configuration |
|------|---------------|
| Tool installed but not in PATH | Provide the full path |
| Multiple versions installed, pin one | Provide the target version's full path |
| Proxy or container with stripped PATH | Configure each needed tool |
| No special override needed | Leave empty, auto-detection works |
