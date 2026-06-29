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

> macOS 首次运行若提示"无法验证开发者"，执行：
> ```sh
> xattr -d com.apple.quarantine ~/.local/bin/waveloom
> ```

## 从源码构建

前置条件：**Go 1.25+**。

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
