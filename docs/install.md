<p align="center">
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./install.en.md">English</a>
</p>

---

# 安装

依赖：[DeepSeek API Key](https://platform.deepseek.com/api_keys)。

## 预编译二进制（推荐）

无需 Go 环境，下载即用。前往 [Releases](https://github.com/Menfre01/waveloom/releases/latest) 下载对应平台的 `waveloom`。

### Homebrew

```sh
brew install Menfre01/tap/waveloom
```

> 若提示 "untrusted tap"，执行 `brew trust menfre01/tap` 后重试。

### 手动下载

> 安装到 `~/.local/bin`，无需 sudo。若该路径不在 PATH 中，执行 `export PATH="$HOME/.local/bin:$PATH"` 并写入 `~/.bashrc` 或 `~/.zshrc`。

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
> Waveloom 使用 Git Bash 的 `bash.exe` 执行所有 shell 命令，因此**安装和使用都必须在 Git Bash 中进行**。cmd 和 PowerShell 不可作为运行终端。
>
> [!TIP]
> **追求最佳体验？推荐使用 [WSL2](https://learn.microsoft.com/zh-cn/windows/wsl/install)。** 在 WSL2 内按 Linux 方式安装，无需 Git Bash 转发层，终端渲染更流畅，shell 命令性能更佳。

**Step 1 — 安装 Git for Windows**

如果尚未安装，从 https://git-scm.com/downloads/win 下载安装（默认选项即可）。

**Step 2 — 下载 Waveloom**

打开 **PowerShell**（不是 Git Bash），运行：

```powershell
powershell -c "irm https://raw.githubusercontent.com/Menfre01/waveloom/main/install.ps1 | iex"
```

> 安装到 `%USERPROFILE%\.local\bin`。脚本会自动检测 Git Bash 是否安装，并提示 PATH 设置。

**Step 3 — 配置 PATH**

安装脚本结束时如果提示 PATH 未配置，在**管理员 PowerShell** 中执行：

```powershell
[Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";$env:USERPROFILE\.local\bin", "User")
```

然后**重启 Git Bash**（如果已打开的话），验证安装：

```sh
which waveloom
# 应输出 /c/Users/<你的用户名>/.local/bin/waveloom
```

> 如果在 Git Bash 中仍找不到 `waveloom`，手动将 `export PATH="$HOME/.local/bin:$PATH"` 追加到 `~/.bashrc`，然后执行 `source ~/.bashrc`。

**Step 4 — 首次配置**

在 **Git Bash** 中运行：

```sh
waveloom setup
# → 选主题 → 选语言 → 选 Provider → 粘贴 API Key → 确认模型 → 保存
```

**Step 5 — 开始使用**

```sh
waveloom "帮我创建一个 Go HTTP 服务"
# 或在项目目录下直接启动交互式 TUI：
waveloom
```

> 如果 `waveloom` 启动后立即退出并提示 "requires Git for Windows"，说明 Git Bash 的安装路径不在默认位置。设置环境变量 `WAVELOOM_GIT_BASH_PATH` 指向你的 `bash.exe` 位置，例如：
> ```sh
> export WAVELOOM_GIT_BASH_PATH="/c/Program Files/Git/bin/bash.exe"
> ```
> 写入 `~/.bashrc` 以持久化。

> macOS 首次运行若提示"无法验证开发者"，执行：
> ```sh
> xattr -d com.apple.quarantine ~/.local/bin/waveloom
> ```

## 从源码构建

前置条件：**Go 1.25+**。Windows 用户还需 `make`（Git for Windows 不自带，可通过 `choco install make` 安装，或直接用 `go build` 替代）。

```sh
git clone https://github.com/Menfre01/waveloom.git
cd waveloom && make install
# waveloom 安装到 $HOME/go/bin，确保该路径在 PATH 中：
export PATH=$HOME/go/bin:$PATH
```

## 更新

**预编译二进制**：重新执行安装命令，覆盖旧版本即可。

**从源码构建**：

```sh
cd waveloom && git pull && make install
```

## 首次配置

```sh
# 交互式引导（只需一次）
waveloom setup
# → 选择 Provider → 输入 API Key → 选择模型 → 完成

# 或跳过配置，直接用环境变量：
LLM_API_KEY=sk-... waveloom
```

## 快速开始

```sh
# 1. 安装（以 macOS ARM64 为例）
mkdir -p ~/.local/bin && curl -fsSL https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin waveloom

# 2. 首次配置（只需一次）
waveloom setup

# 3. 开始使用
waveloom "你好，介绍一下你自己"
```

> 配置保存在 `~/.waveloom/settings.json`。项目级配置可放在 `.waveloom/settings.json`，字段相同，优先级高于全局配置。
