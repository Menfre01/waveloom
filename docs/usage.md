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
| `↑` `↓` / `PgUp` `PgDn` | 滚动对话历史 |
| `Ctrl+E` / `End` | 跳到底部 |
| `Tab` | 聚焦下一个可交互段落（thought / tool 输出） |
| `Shift+Tab` | 聚焦上一个可交互段落；无焦点时进入/退出 Plan 模式 |
| `Enter` | 展开/折叠当前聚焦的段落 |
| `Ctrl+G` | 切换主题（auto / dark / light / darkcolorblind / lightcolorblind） |
| `Ctrl+V` | 粘贴剪贴板内容 |
| `?` | 显示快捷键帮助 |
| `Ctrl+C` | 退出 |
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
```

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
| `/model` | — | 显示或切换模型，可输入模型名快速过滤 |
| `/theme` | — | 选择主题（auto / dark / light / darkcolorblind / lightcolorblind） |
| `/locale` | `/lang` | 切换语言（zh-CN / en-US） |
| `/provider` | — | 查看或切换 LLM Provider（DeepSeek / Kimi / OpenAI） |
| `/rewind` | — | 回退到历史消息（恢复文件状态） |
| `/help` | — | 显示所有可用命令 |
`.claude/skills/` 中 `user-invocable: true` 的 Skill 也会自动注册为 `/` 命令，命令名即 Skill 名。此外，已安装的 Claude Code 插件中的 skills/commands 会自动发现并加载（通过 `~/.claude/plugins/installed_plugins.json` + `enabledPlugins` 配置）。

## Plan 模式

Plan 模式是"先规划后执行"的二阶段工作流。适合 3 个以上文件改动、涉及架构决策、或存在多种可行方案的任务。

**进入方式**：
- **快捷键**：空闲态按 `Shift+Tab`（无段落聚焦时）直接进入
- **Agent 主动调用**：LLM 判断任务复杂度后调用 `enter_plan_mode`，弹出确认框

**Plan 模式下**：
- 所有工具正常可见，但 `write_file` / `edit_file` 仅允许写入 plan 文件
- Shell 分析命令（`go test`、`git log`、`npm ls` 等）自动放行，危险命令硬拦截
- LLM 通过 `ask_user_question` 与你持续沟通澄清需求
- Plan 内容写入 `~/.waveloom/plans/<slug>.md`

**退出方式**：
- **快捷键**：plan 模式空闲态按 `Shift+Tab`，弹出审批框确认 approve / reject
- **Agent 调用**：LLM 就绪后调用 `exit_plan_mode`，同样弹出审批框
- 审批通过 → 恢复正常模式，LLM 开始编码
- 审批拒绝 → 留在 plan 模式，LLM 根据反馈修改 plan

输入框左侧 `▌Plan` 标记表示当前处于 Plan 模式。

## Advisor 模式

Advisor 模式是成本优化的双模型路由策略——次模型处理日常任务，主模型负责深度推理。在 `settings.json` 中开启：

```json
{
  "llm": {
    "provider": "deepseek",
    "model": "deepseek-v4-pro",
    "sub_model": "deepseek-v4-flash",
    "mode": "advisor"
  }
}
```

**工作原理**：

- **默认**：Agent 使用次模型（`sub_model`，如 `deepseek-v4-flash`）——约 2 倍便宜，擅长阅读、搜索和实现明确的变更。
- **Plan 模式**：进入 plan mode 自动切换为主模型（`model`，如 `deepseek-v4-pro`）进行深度架构推理。退出 plan mode 自动切回。
- **Advisor 子代理**：当次模型遇到无法自信决策的问题时，派发 advisor 子代理（主模型，只读）分析方案权衡并给出建议。
- **代码审查**：`evaluate` 和 `verification` 子代理始终使用主模型——审查质量不降级。

**前置条件**：`sub_model` 必须非空且不等于 `model`。DeepSeek provider 下 `sub_model` 自动配对为 `deepseek-v4-flash`；其他 provider 需显式设置。

不设置 `mode` 或设为 `"normal"` 则保持全程使用主模型（默认行为）。

### AGENTS.md 自动加载

Waveloom 启动时会自动发现并加载 `AGENTS.md`（查找路径：`~/.waveloom/AGENTS.md` → 项目根 `.git` 所在目录 → CWD），按"由外到内"顺序拼接，作为第一条 user 消息注入上下文。Agent 在对话中自动遵循其中的项目约定、编码规范和操作流程。

### AGENTS.md 内 @ 展开

`AGENTS.md` 内部同样支持 `@` 引用语法，可用于将大型约定文档拆分为多个文件：

```
# AGENTS.md
@docs/coding-style.md
@docs/release-process.md
```

Waveloom 在加载 AGENTS.md 后会自动展开其中的 `@` 引用，多个引用按出现顺序展开，同一文件自动去重。
