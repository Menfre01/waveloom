<p align="center">
  <a href="./install.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>English</strong>
</p>

---

# Install

Requires: [DeepSeek API Key](https://platform.deepseek.com/api_keys).

## Pre-built Binary (Recommended)

No Go required. Grab the right binary from [Releases](https://github.com/Menfre01/waveloom/releases/latest).

### Homebrew

```sh
brew install Menfre01/tap/waveloom
```

> If prompted "untrusted tap", run `brew trust menfre01/tap` and retry.

### Manual Download

> Installs to `~/.local/bin` — no sudo needed. If the directory isn't in PATH, run `export PATH="$HOME/.local/bin:$PATH"` and add to `~/.bashrc` or `~/.zshrc`.

**macOS (ARM64 — Apple Silicon)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin waveloom
```

**macOS (AMD64 — Intel)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_darwin_amd64.tar.gz | tar -xz -C ~/.local/bin waveloom
```

**Linux (AMD64)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_linux_amd64.tar.gz | tar -xz -C ~/.local/bin waveloom
```

**Linux (ARM64)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_linux_arm64.tar.gz | tar -xz -C ~/.local/bin waveloom
```

**Windows**

> [!IMPORTANT]
> Waveloom relies on Git Bash's `bash.exe` to execute shell commands — you must **install and run waveloom in Git Bash**. cmd and PowerShell are not supported as the runtime terminal.

**Step 1 — Install Git for Windows**

If not already installed, download from https://git-scm.com/downloads/win (default options are fine).

**Step 2 — Download Waveloom**

Open **PowerShell** (not Git Bash) and run:

```powershell
powershell -c "irm https://raw.githubusercontent.com/Menfre01/waveloom/main/install.ps1 | iex"
```

> Installs to `%USERPROFILE%\.local\bin`. The script automatically checks for Git Bash and prompts for PATH setup.

**Step 3 — Configure PATH**

If the installer warns that PATH is not configured, run this in an **elevated PowerShell**:

```powershell
[Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";$env:USERPROFILE\.local\bin", "User")
```

Then **restart Git Bash** (if already open) and verify:

```sh
which waveloom
# Should output /c/Users/<yourname>/.local/bin/waveloom
```

> If `waveloom` is still not found in Git Bash, manually add `export PATH="$HOME/.local/bin:$PATH"` to `~/.bashrc`, then run `source ~/.bashrc`.

**Step 4 — First-time Setup**

In **Git Bash**, run:

```sh
waveloom setup
# → Choose theme → Choose language → Choose Provider → Paste API Key → Confirm model → Save
```

**Step 5 — Start Using**

```sh
waveloom "Create a Go HTTP server for me"
# Or launch the interactive TUI from your project directory:
waveloom
```

> If `waveloom` starts then immediately exits with "requires Git for Windows", Git Bash is installed at a non-standard location. Set the `WAVELOOM_GIT_BASH_PATH` environment variable to your `bash.exe` path, e.g.:
> ```sh
> export WAVELOOM_GIT_BASH_PATH="/c/Program Files/Git/bin/bash.exe"
> ```
> Add to `~/.bashrc` to persist.

> macOS Gatekeeper? Allow it with:
> ```sh
> xattr -d com.apple.quarantine ~/.local/bin/waveloom
> ```

## Build from Source

Prerequisites: **Go 1.25+**. Windows users also need `make` (not bundled with Git for Windows — install via `choco install make`, or use `go build` directly).

```sh
git clone https://github.com/Menfre01/waveloom.git
cd waveloom && make install
# waveloom is installed to $HOME/go/bin — make sure it's on PATH:
export PATH=$HOME/go/bin:$PATH
```

## Update

**Pre-built binary**: re-run the install command to overwrite the old version.

**Build from source**:

```sh
cd waveloom && git pull && make install
```

## First-time Setup

```sh
# Interactive guide (once only)
waveloom setup
# → Choose Provider → Enter API Key → Choose Model → Done

# Or skip config entirely with an env var:
LLM_API_KEY=sk-... waveloom
```

## Quick Start

```sh
# 1. Install (macOS ARM64 example)
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin waveloom

# 2. First-time setup (once only)
waveloom setup

# 3. Start using
waveloom "Hello, tell me about yourself"
```

> Config is saved to `~/.waveloom/settings.json`. Project-level config can be placed at `.waveloom/settings.json`, with the same fields and higher priority than the global config.
