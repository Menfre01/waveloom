<p align="center">
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./usage.en.md">English</a>
</p>

---

# 使用方式

## 交互模式

```sh
waveloom
```

进入 TUI 后，像聊天一样打字，Enter 发送。Agent 会自主调用工具来读文件、搜代码、编辑、跑测试。

<p align="center">
  <img src="../assets/tui.png" alt="Waveloom 截图" width="720"/>
</p>

每行开头的字符告诉你**谁在说话**：

| 前缀 | 角色 | 含义 |
|------|------|------|
| `›` | 你 | 你的消息，蓝色 |
| `·` / spinner | Assistant | AI 的回复，绿色，支持 Markdown 渲染 |
| `·` / spinner | Thought | AI 的思考过程，灰色，完成后自动折叠为一句话（`Tab` 聚焦 + `Enter` 展开） |
| `•` / spinner | 工具 | AI 的操作（读文件、写文件、跑命令），绿=成功 / 红=失败 |

**快捷键**：

| 按键 | 作用 |
|------|------|
| `Enter` | 发送消息；输入 `exit` 回车退出 |
| `Esc` | 中断正在运行的 Agent |
| `Esc+Esc` | 清空输入框 |
| `↑` `↓` | 空闲态浏览输入历史，到头后 / 运行中滚动对话历史 |
| `Ctrl+E` / `End` | 跳到底部 |
| `Tab` | 聚焦下一个可交互段落（thought / tool 输出） |
| `Shift+Tab` | 聚焦上一个可交互段落；无焦点时进入/退出 Plan 模式 |
| `Enter` | 展开/折叠当前聚焦的段落 |
| `Ctrl+G` | 切换主题（auto / dark / light / darkcolorblind / lightcolorblind） |
| `Ctrl+V` | 粘贴剪贴板内容 |
| `?` | 显示快捷键帮助 |
| `Ctrl+C×2` | 双击退出(防误触) |
| `Shift + 鼠标拖动` | 选中终端中的文本 |
| `鼠标滚轮` | 每次滚动 3 行 |
**底部状态栏**显示：当前模型、上下文用量（进度条）、缓存命中率、Loop 轮数、余额。

## 单次执行

```sh
waveloom "解释 pkg/llm/client.go 的设计"
waveloom --model deepseek-v4-pro "给 UserService 写单元测试"
echo "review pkg/llm/ 下的代码" | waveloom
```

## 会话管理

```sh
waveloom ls                     # 列出最近会话
waveloom --continue             # 恢复最近一次会话
waveloom --resume <session-id>  # 恢复指定会话
waveloom --name <name>          # 为新会话命名
```

## Skill 安装与管理

从任意 git 仓库安装社区 Skill,自动锁定到具体 commit(`skill.lock.json`):

```sh
waveloom skill add https://github.com/user/skills.git@v1.2 --path packages/skills/review
waveloom skill list              # 列出全部 skill 及来源(远程@commit 或本地)
waveloom skill update review     # 拉取该 ref 当前最新 commit
waveloom skill remove review     # 移除(仅限本工具安装的 skill)
```

- `@ref` 支持 branch / tag / commit SHA;缺省 `main`
- 默认安装到项目级 `.waveloom/skills/`,`--global` 安装到用户级 `~/.waveloom/skills/`
- 手动创建的 skill 不受 `skill.lock.json` 管理,`remove` 会拒绝误删

## @ 文件引用

在输入框里打 `@`，会弹出文件选择器，支持模糊过滤（前缀 > 子串匹配），`Tab` 进入子目录。选中的文件内容会自动注入到消息上下文。

```
帮我优化 @pkg/auth/login.go 的错误处理逻辑
```

### AGENTS.md 自动加载

## / 命令面板

在输入框打 `/` 会弹出命令面板，支持模糊搜索。

| 命令 | 别名 | 说明 |
|------|------|------|
| `/new` | `/clear` | 创建全新 session |
| `/model` | — | 显示或切换模型,可输入模型名快速过滤;选择器内按 `e` 配置思考档位 |
| `/rename` | — | 重命名当前会话 |
| `/theme` | — | 选择主题（auto / dark / light / darkcolorblind / lightcolorblind） |
| `/locale` | `/lang` | 切换语言（zh-CN / en-US） |
| `/provider` | — | 查看或切换 LLM Provider(DeepSeek / Kimi / GLM / OpenAI) |
| `/rewind` | — | 回退到历史消息（恢复文件状态） |
| `/help` | — | 显示所有可用命令 |
`.claude/skills/` 中 `user-invocable: true` 的 Skill 也会自动注册为 `/` 命令，命令名即 Skill 名。此外，已安装的 Claude Code 插件中的 skills/commands 会自动发现并加载（通过 `~/.claude/plugins/installed_plugins.json` + `enabledPlugins` 配置）。

## Plan 模式

Plan 模式是"先规划后执行"的二阶段工作流。适合 3 个以上文件改动、涉及架构决策、或存在多种可行方案的任务。

**进入方式**：
- **快捷键**：空闲态按 `Shift+Tab`（无段落聚焦时）直接进入
- **Agent 主动调用**：LLM 判断任务复杂度后调用 `enter_plan_mode`，弹出确认框

**Plan 模式下**：
- 所有工具正常可见,但 `write` / `edit` 仅允许写入 plan 文件
- Shell 分析命令（`go test`、`git log`、`npm ls` 等）自动放行，危险命令硬拦截
- LLM 通过 `ask_user_question` 与你持续沟通澄清需求
- Plan 内容写入 `~/.waveloom/plans/<slug>.md`

**退出方式**：
- **快捷键**：plan 模式空闲态按 `Shift+Tab`，弹出审批框确认 approve / reject
- **Agent 调用**：LLM 就绪后调用 `exit_plan_mode`，同样弹出审批框
- 审批通过 → 恢复正常模式，LLM 开始编码
- 审批拒绝 → 留在 plan 模式，LLM 根据反馈修改 plan

输入框左侧 `▌Plan` 标记表示当前处于 Plan 模式。

## 沙箱执行

Waveloom 提供 OS 级执行隔离(bubblewrap(Linux)/ Seatbelt(macOS)):只读根、工作区可写、敏感路径遮蔽(`~/.ssh`、钥匙串、token 等)、环境变量剥离、网络可控。

### 经典组合:沙箱 + bypass-permissions + 网络 on(CI/无交互)

```sh
waveloom --bypass-permissions --sandbox-network on "go build && go test"
```

这一组合的效果:

| 层 | 行为 |
|----|------|
| 权限 | `--bypass-permissions` → 二元决策(仅 DENY/ALLOW,无弹窗,deny 规则与高危硬拦截仍生效) |
| 沙箱 | 自动激活 → 命令在沙箱内执行(只读根 + 工作区可写 + 凭据遮蔽 + 环境变量剥离) |
| 网络 | `--sandbox-network on` → 沙箱内直连(拉依赖、git fetch、gh 等可用);`off` 为默认全断网 |

> [!TIP]
> 网络 `on` 时建议配置凭据遮蔽(`settings.json` 的 `credentials.files` / `filesystem.denyRead`),防止未遮蔽的用户文件被读走外传。未配置时沙箱仍启用,但会输出警告提示。

### 其他常见用法

```sh
# 沙箱 + 权限放行 + 断网(本地构建/测试,默认网络策略)
waveloom --bypass-permissions "make build && make test"

# TUI 常规模式显式启用沙箱(需要 .waveloom/settings.json 配置 enabled: true)
waveloom

# 需要 docker / 联网特权命令时,配置逃生舱(项目级 .waveloom/settings.json):
# { "sandbox": { "excludedCommands": ["docker *"] } }
```

**配置样例**(`.waveloom/settings.json`):

```json
{
  "sandbox": {
    "enabled": true,
    "excludedCommands": ["docker *"],
    "env": {
      "GOPATH": "./.waveloom-gopath",
      "GOMODCACHE": "./.waveloom-gomodcache",
      "GOCACHE": "./.waveloom-gocache"
    },
    "network": { "mode": "on" },
    "credentials": { "files": ["~/.ssh", "~/.aws/credentials"] }
  }
}
```

> `sandbox.env` 可把构建工具缓存重定向到 workspace 可写区(go 的 `GOPATH`/`GOMODCACHE`/`GOCACHE`、npm 的 `npm_config_cache` 等),避免只读根下宿主缓存写入失败。完整字段说明见 [settings.md](settings.md#沙箱内环境变量注入env)。

**平台支持**:Linux(bubblewrap,`apt install bubblewrap`)/ macOS(Seatbelt,系统自带)/ Windows 不支持(建议 WSL2 走 Linux 后端)。沙箱后端不可用时自动降级并警告;`failIfUnavailable: true` 可改为拒绝启动。

### Linux 首次使用:bubblewrap 安装引导

bubblewrap **不是 Linux 默认安装**——首次启用沙箱时若缺失,启动日志会按发行版给出安装命令:

| 发行版 | 命令 |
|--------|------|
| Ubuntu / Debian / Mint | `sudo apt install bubblewrap` |
| Fedora / RHEL / CentOS | `sudo dnf install bubblewrap` |
| Arch / Manjaro | `sudo pacman -S bubblewrap` |
| Alpine | `sudo apk add bubblewrap` |
| openSUSE | `sudo zypper install bubblewrap` |

> [!TIP]
> 装有 **Flatpak** 的系统通常已自带 bubblewrap(Flatpak 沙箱的核心依赖)——先 `which bwrap` 确认,可能无需安装。

Ubuntu 24.04+ 若被 AppArmor 拦截(unprivileged userns 限制),按错误信息指引:

```sh
# 临时方案
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
# 永久方案:安装允许 bwrap userns 的 AppArmor profile(见错误输出指引)
```

### excludedCommands:经典逃生舱场景

`excludedCommands` 让指定命令**不进沙箱**(裸跑),但权限仍受 Guard 约束(deny 规则与高危硬拦截保留)。典型场景:

**1. docker(最常用)——沙箱内 docker 无法工作**

沙箱遮蔽了 `~/.docker/config.json` 并限制 daemon socket 连接,因此 docker 命令必须在沙箱外运行:

```json
{
  "sandbox": { "excludedCommands": ["docker *"] }
}
```

```sh
waveloom --bypass-permissions --sandbox-network on "docker build -t app . && docker push app"
```

> [!NOTE]
> 复合命令中只要含逃逸命令(`A && docker ps && B`),**整条命令都会逃逸沙箱**——模型应把 docker 命令单独执行,其余命令保持沙箱内。

**2. 部分联网——只放行特定命令,其余保持断网**

不想全局开 `network.mode: on`,但需要个别命令联网(如 `git push`、`npm install`):

```json
{
  "sandbox": {
    "excludedCommands": ["git push *", "npm install *"],
    "network": { "mode": "off" }
  }
}
```

效果:沙箱内其他命令全部断网,只有逃逸命令可以联网。

**3. 逃生舱——沙箱内失败的命令**

命令在沙箱内失败时(`allowUnsandboxedCommands` 默认开启),输出会提示可加入 `excludedCommands` 重试;确认命令本身安全后按提示配置即可。

### AGENTS.md 自动加载

Waveloom 启动时会自动发现并加载 `AGENTS.md`(查找路径:`~/.waveloom/AGENTS.md` → 项目根 `.git` 所在目录 → CWD),按"由外到内"顺序拼接,作为第一条 user 消息注入上下文。Agent 在对话中自动遵循其中的项目约定、编码规范和操作流程。

### AGENTS.md 内 @ 展开

`AGENTS.md` 内部同样支持 `@` 引用语法,可用于将大型约定文档拆分为多个文件:

```
# AGENTS.md
@docs/coding-style.md
@docs/release-process.md
```

Waveloom 在加载 AGENTS.md 后会自动展开其中的 `@` 引用,多个引用按出现顺序展开,同一文件自动去重。

