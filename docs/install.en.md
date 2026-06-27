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

> Installs to `~/.local/bin` — no sudo needed. If the directory isn't in PATH, run `export PATH="$HOME/.local/bin:$PATH"` and add to `~/.bashrc` or `~/.zshrc`.

**macOS (ARM64 — Apple Silicon)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin wvl
```

**macOS (AMD64 — Intel)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_amd64.tar.gz | tar -xz -C ~/.local/bin wvl
```

**Linux (AMD64)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_amd64.tar.gz | tar -xz -C ~/.local/bin wvl
```

**Linux (ARM64)**

```sh
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_linux_arm64.tar.gz | tar -xz -C ~/.local/bin wvl
```

> macOS Gatekeeper? Allow it with:
> ```sh
> xattr -d com.apple.quarantine ~/.local/bin/wvl
> ```

## Build from Source

Prerequisites: **Go 1.25+**.

```sh
git clone https://github.com/Menfre01/waveloom.git
cd waveloom && make install
# wvl is installed to $HOME/go/bin — make sure it's on PATH:
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
wvl setup
# → Choose Provider → Enter API Key → Choose Model → Done

# Or skip config entirely with an env var:
LLM_API_KEY=sk-... wvl
```

## Quick Start

```sh
# 1. Install (macOS ARM64 example)
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/wvl_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin wvl

# 2. First-time setup (once only)
wvl setup

# 3. Start using
wvl "Hello, tell me about yourself"
```

> Config is saved to `~/.waveloom/settings.json`. Project-level config can be placed at `.waveloom/settings.json`, with the same fields and higher priority than the global config.
