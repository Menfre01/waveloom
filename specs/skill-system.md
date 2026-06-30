# Skill System 组件规格书

## 组件定位

Skill System 是 Waveloom 的**可复用提示词扩展系统**，允许用户通过 `SKILL.md` 文件定义 specialized 的指令和工作流，
在需要时注入到 LLM 上下文。Skill 不是可执行代码——它是预先写好的 Markdown 提示词，让 LLM 按照指令自行完成工作。

**核心区分：**

| 概念 | 谁执行 | 何时加载 | 用途 |
|------|--------|---------|------|
| Tool | Waveloom（Go 代码） | LLM 调用时 | 执行具体操作（读文件、搜代码...） |
| AGENTS.md | — | 会话启动时全量注入 | 项目约定、编码规范（始终生效） |
| Skill | LLM（读取指令） | 触发时按需注入 | 特定任务流程、操作清单（懒加载） |
| Slash Command | TUI（本地逻辑） | 用户输入 `/` 时 | 本地操作（重置、切换模型...） |

**核心价值：** 与 AGENTS.md 不同，skill body 仅在触发时加载——"long reference material costs almost nothing until you need it"。

## 参考来源

- Claude Code: [Extend Claude with skills](https://docs.anthropic.com/en/docs/claude-code/skills) — SKILL.md 格式、frontmatter 字段、动态注入、生命周期、附属文件
- [Agent Skills](https://agentskills.io) — 开放标准（Claude Code skills 遵循此标准）
- Waveloom 现有组件：`pkg/slashcommand/`、`pkg/tool/`、`pkg/context/`、`pkg/memory/`

## 首版范围（P0）— 100% Claude Code 兼容

- ✅ SKILL.md 解析（YAML frontmatter + Markdown body）
- ✅ 双路径发现：`.claude/skills/` + `.waveloom/skills/`（个人级 + 项目级）
- ✅ `.claude/commands/<name>.md` 兼容（扁平文件视为 skill，无需子目录）
- ✅ 用户手动 `/skill-name [args]` 调用（通过 SlashCommand）
- ✅ LLM 自动调用（通过 `skill` 工具）
- ✅ 变量替换：`$ARGUMENTS`、`$ARGUMENTS[N]`、`$0`/`$1`、`$name`、`${CLAUDE_SESSION_ID}` / `${WAVELOOM_SESSION_ID}`、`${CLAUDE_SKILL_DIR}` / `${WAVELOOM_SKILL_DIR}`、`${CLAUDE_EFFORT}` / `${WAVELOOM_EFFORT}`（双命名空间兼容）
- ✅ 转义 `\$` 为字面 `$`
- ✅ 动态上下文注入：`` !`command` `` 和 ` ```! ` 块
- ✅ 附属文件自动发现与列表注入（skill 目录下所有非 SKILL.md 文件清单附在 body 末尾，LLM 通过 read_file 读取）
- ✅ frontmatter 控制：`description`、`when_to_use`、`argument-hint`、`arguments`、`disable-model-invocation`、`user-invocable`（全字段）
- ✅ 技能列表注入 LLM 上下文（`## Available Skills` 表格）
- ✅ `$ARGUMENTS` 无痛追加（原始 body 排除代码片段后无 `$ARGUMENTS` 变量且 args 非空时自动追加 `ARGUMENTS: <value>`）
- ✅ 代码片段保护：行内 `` `...` `` 和围栏 ` ``` ` 块中的变量不被替换

### P1 延后

- `allowed-tools` / `disallowed-tools` 工具权限控制
- `context: fork` 子代理执行 + `agent` 字段
- `paths` 路径条件激活
- `model` / `effort` 覆盖
- `hooks` 生命周期钩子
- `shell` (bash/powershell) 动态注入解释器选择
- `skillOverrides` 设置（`on` / `name-only` / `user-invocable-only` / `off`）
- 实时文件变更检测（当前需 /clear 重载）
- 嵌套目录 skill（monorepo 场景，子目录 `.claude/skills/` → 目录限定名 `apps/web:deploy`）
- `--add-dir` 目录中的 skill 自动加载

### 不纳入

- Built-in / bundled skills（如 `/code-review`、`/debug`）：首版只支持用户自定义
- 企业级托管 skill（对标 Claude Code managed settings）
- 插件 skill（对标 Claude Code plugin skills）
- `skill-creator` eval 工具链

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| LLM 调用方式 | Skill 工具（`tool.Tool`） | 复用 Tool System 架构；LLM 通过 function calling 自主发现和调用；权限/统计/错误处理统一 |
| 用户调用方式 | SlashCommand（`SideEffectInvokeSkill`） | 复用 SlashCommand Registry；用户界面统一为 `/xxx` |
| 存储路径 | `.claude/skills/` + `.waveloom/skills/` 双路径 + `.claude/commands/` 扁平文件 | 完全兼容 Claude Code skill 生态；用户只需维护一份 skill |
| 路径优先级 | `.claude/skills` > `.waveloom/skills`；`.claude/commands` 同名时 skill 优先 | 对齐 Claude Code 行为：skill 优先于 command |
| 发现时机 | 会话启动时一次性扫描 | 对标 AGENTS.md 加载策略；避免运行期 I/O 抖动；/clear 时重载 |
| 描述注入位置 | system prompt（`## Available Skills` 节） | 让 LLM 始终知道有哪些 skill 可用；system prompt 是前缀缓存起点 |
| Skill body 注入方式（手动） | user 消息注入 → 立即发起 LLM 调用 | 等价于用户输入了 skill body 的内容 |
| Skill body 注入方式（LLM 调用） | tool result 返回 | 复用 Tool System；LLM 看到 result 后按指令继续 |
| body 生命周期 | 注入后保持在上下文中，随对话继续 | 对标 Claude Code；skill body 是"站立指令"，不是一次性提示 |
| 动态注入执行 | `os/exec` 子进程，30s 超时 | 对标 Claude Code；`` !`cmd` `` 在渲染阶段执行，LLM 只看到结果 |
| 变量替换 | 渲染阶段一次性替换 | 对标 Claude Code；`$ARGUMENTS` 等占位符在注入 LLM 前替换完成 |
| 变量名兼容 | `${CLAUDE_*}` 和 `${WAVELOOM_*}` 均支持 | 用户从 Claude Code 迁移时无需修改 SKILL.md；两个命名空间等价 |
| 附属文件 | 自动发现 skill 目录下所有非 SKILL.md 文件，以文件清单形式附在 body 末尾 | 对标 Claude Code：LLM 知道有哪些附属文件，通过 read_file 按需读取 |
| `.claude/commands/` 兼容 | 扁平 `.md` 文件自动视为 skill（目录名为 `_flat_commands_`，name 取自文件名） | Claude Code 已合并 commands 到 skills；原地兼容用户已有 `.claude/commands/` 文件 |
| `when_to_use` 字段 | 追加到 `description` 后注入 system prompt | 对齐 Claude Code：description + when_to_use 共用 1536 字符截断 |
| `$name` 命名参数 | 通过 frontmatter `arguments` 列表定义，名称映射到位置 | 对齐 Claude Code：`arguments: [issue, branch]` → `$issue` = 参数1, `$branch` = 参数2 |
| TUI 统一显示 | 两条路径（SlashCommand / Skill Tool）均渲染为 `paraSkill` 段落 | 用户在 TUI 上看到一致的 "Skill: deploy staging" 视图，不受触发方式影响；skill body 可折叠展开 |

## 组件边界

### 输入

- `cwd string` — 当前工作目录
- `homeDir string` — 用户主目录
- `sessionID string` — 当前 session ID（用于 `${WAVELOOM_SESSION_ID}` 替换）
- `effort string` — 当前 effort level（用于 `${WAVELOOM_EFFORT}` 替换，`low`/`medium`/`high`/`xhigh`/`max`）
- `skillName string` — 被调用的 skill 名称
- `args string` — 传入 skill 的参数（可选）

### 输出

- `*LoadedSkill` — 渲染后的 skill（含 name、description、body、frontmatter 元数据）
- `[]SkillInfo` — 所有可用 skill 的摘要列表（名称 + 描述）
- `error` — 仅在系统级 I/O 错误时非 nil

### 依赖（接口，非具体实现）

- 无内部依赖（仅依赖标准库 `os`、`os/exec`、`path/filepath`、`encoding/yaml`）
- `gopkg.in/yaml.v3` 用于 YAML frontmatter 解析
- `os/exec` 用于 `` !`cmd` `` 动态注入

### 不纳入本组件

- Skill body 的消息注入（由 ContextManager 负责）
- Skill 工具的注册和调用（由 Tool System 负责）
- Skill 命令的注册和匹配（由 SlashCommand 负责）
- TUI 的 SideEffect 处理（由 TUI 负责）

---

## 接口定义

> 所有类型定义在 `pkg/skill/skill.go`。
> 本包不 import Bubble Tea，不 import TUI 代码，不 import LLM 代码。
> 仅依赖标准库 + `gopkg.in/yaml.v3`（YAML frontmatter 解析）。

### SkillInfo — 技能摘要

```go
// SkillInfo 是 Skill 的轻量描述，发送给 LLM 和 SlashCommand Registry。
type SkillInfo struct {
    Name           string // 技能名（目录名或文件名），如 "deploy"
    Description    string // 简短描述，来自 frontmatter description + when_to_use
    Args           string // 参数占位符提示（来自 frontmatter argument-hint）
    UserInvocable  bool   // 用户是否可通过 / 调用
    ModelInvocable bool   // LLM 是否可自动调用
    Source         string // 来源路径，如 "~/.claude/skills/deploy/SKILL.md"
}
```

### LoadedSkill — 完整渲染结果

```go
// LoadedSkill 是渲染后的完整 Skill，在调用 Load 时返回。
type LoadedSkill struct {
    Info    SkillInfo  // 摘要信息
    Body    string     // 渲染后的 Markdown body（变量已替换，!`cmd` 已执行，附属文件清单已追加）
    DirPath string     // SKILL.md 所在目录（用于 ${WAVELOOM_SKILL_DIR}）
}
```

### Loader — 发现 + 加载

```go
// Loader 负责发现、解析和渲染 SKILL.md 文件。
type Loader struct {
    CWD       string // 当前工作目录（用于项目级 skill 发现）
    HomeDir   string // 用户主目录（用于个人级 skill 发现）
    SessionID string // 当前 session ID
    Effort    string // 当前 effort 级别
}

// NewLoader 创建一个新的 Loader。
func NewLoader(cwd, homeDir, sessionID, effort string) *Loader

// List 扫描所有 skill 目录和 .claude/commands/ 文件，返回可用 skill 的摘要列表。
// 不渲染 body，仅解析 frontmatter 的 name/description/when_to_use/user-invocable/disable-model-invocation。
//
// 发现顺序（同名 skill 取前者优先）：
//  1. ~/.claude/skills/<name>/SKILL.md
//  2. ~/.waveloom/skills/<name>/SKILL.md
//  3. ~/.claude/commands/<name>.md
//  4. ~/.waveloom/commands/<name>.md
//  5. {projectRoot}/.claude/skills/<name>/SKILL.md
//  6. {projectRoot}/.waveloom/skills/<name>/SKILL.md
//  7. {projectRoot}/.claude/commands/<name>.md
//  8. {projectRoot}/.waveloom/commands/<name>.md
//
// 同名时 skill（目录形式）优先于 command（扁平文件形式）。
func (l *Loader) List() ([]SkillInfo, error)

// Load 加载并渲染指定名称的 skill。
//
// 步骤：
//  1. 按 List 的优先级顺序查找 SKILL.md 或 commands/*.md
//  2. 解析 YAML frontmatter + Markdown body
//  3. 执行变量替换（$ARGUMENTS / $ARGUMENTS[N] / $0 / $1 / $name / ${WAVELOOM_SESSION_ID} / ${WAVELOOM_SKILL_DIR} / ${WAVELOOM_EFFORT}）
//  4. 执行动态注入（!`cmd` / ```! block）
//  5. 追加附属文件清单（如果 skill 目录下有非 SKILL.md 文件）
//  6. 返回 LoadedSkill
//
// args 为空时（用户仅输入 /deploy 无参数）：
//   - $ARGUMENTS → ""
//   - $0 → ""
//
// args 非空时（/deploy staging）：
//   - $ARGUMENTS → "staging"
//   - $0 → "staging"
//
// 多参数时（/deploy "production" "main"）：
//   - $ARGUMENTS → "production main"
//   - $0 → "production"
//   - $1 → "main"
func (l *Loader) Load(name string, args string) (*LoadedSkill, error)

// FormatSkillListing 格式化所有 skill 的描述列表，注入到 system prompt。
// 输出格式与 Claude Code skill listing 相似。
// description + when_to_use 合并后截断到 1536 字符。
func (l *Loader) FormatSkillListing() string

// Reload 重新扫描 skill 目录（/clear 时调用）。
// 实际等价于再次调用 List()。
func (l *Loader) Reload() ([]SkillInfo, error)
```

---

## SKILL.md 格式

### Frontmatter 字段（首版支持）

```yaml
---
name: deploy                           # 显示名称（默认取目录名或文件名）
description: Deploy to production      # 技能描述（recommended）
when_to_use: When deploying to prod    # 额外的触发条件描述（追加到 description 后）
argument-hint: [environment]           # 参数占位符提示
arguments: [environment, branch]       # 命名参数列表
disable-model-invocation: true         # 禁止 LLM 自动调用
user-invocable: true                   # 用户可通过 / 调用（默认 true）
---
```

### Body

- 标准 Markdown
- 支持 `` !`command` `` 动态注入（行首或空白后 `!` 开头）
- 支持 ` ```! ... ``` ` 多行命令块
- 支持 `$ARGUMENTS`、`$ARGUMENTS[N]`、`$0`、`$1`、`$name` 变量替换
- 支持 `${CLAUDE_SESSION_ID}` / `${WAVELOOM_SESSION_ID}` 环境变量替换
- 支持 `${CLAUDE_SKILL_DIR}` / `${WAVELOOM_SKILL_DIR}` 环境变量替换
- 支持 `${CLAUDE_EFFORT}` / `${WAVELOOM_EFFORT}` 环境变量替换
- 支持 `\$` 转义为字面 `$`

### 附属文件

Skill 目录下除 `SKILL.md` 外的所有文件均为附属文件（`reference.md`、`examples.md`、`scripts/` 等）。
Load 时自动扫描并追加文件清单到 body 末尾，LLM 通过 `read_file` 按需读取。

```
my-skill/
├── SKILL.md           # 主指令（必需）
├── reference.md       # 详细 API 文档（LLM 按需读取）
├── examples.md        # 使用示例（LLM 按需读取）
└── scripts/
    └── validate.sh    # 可执行脚本（LLM 通过 shell 执行）
```

### `.claude/commands/` 兼容

`.claude/commands/<name>.md` 和 `.waveloom/commands/<name>.md` 中的扁平 `.md` 文件自动视为 skill：
- `name` = 文件名去扩展名
- 其他 frontmatter 字段照常解析
- 附属文件不适用（扁平文件无目录）
- 与同名 skill 冲突时 skill（目录形式）优先

### 示例

#### 基础 skill（目录形式）
```markdown
---
description: Summarizes uncommitted changes and flags anything risky.
---

## Current changes

!`git diff HEAD`

## Instructions

Summarize the changes above in two or three bullet points, then list any risks
such as missing error handling, hardcoded values, or tests that need updating.
If the diff is empty, say there are no uncommitted changes.
```

#### 带附属文件的 skill
```markdown
---
description: API design patterns for this codebase
---

# API Conventions

When writing API endpoints:
- Use RESTful naming conventions
- Return consistent error formats
- Include request validation

## Additional resources

- For complete API details, see [reference.md](reference.md)
- For usage examples, see [examples.md](examples.md)
```

#### 扁平 command 文件
```markdown
---
description: Deploy the application to production
disable-model-invocation: true
---

Deploy $ARGUMENTS to production:

1. Run the test suite
2. Build the application
3. Push to the deployment target
4. Verify the deployment succeeded
```

---

## 核心算法

### 1. Skill 发现（List）

```
输入: cwd, homeDir
输出: []SkillInfo

1. projectRoot = findProjectRoot(cwd)  // 同 pkg/memory 逻辑

2. 扫描源列表（按优先级）：
   a. filepath.Join(homeDir, ".claude", "skills")       // 个人 skill（目录形式）
   b. filepath.Join(homeDir, ".waveloom", "skills")
   c. filepath.Join(homeDir, ".claude", "commands")      // 个人 command（扁平文件）
   d. filepath.Join(homeDir, ".waveloom", "commands")
   e-j. 若 projectRoot != ""，同上四个目录的项目版本

3. 对每个扫描源：
   a. 若为 skills/ 目录：
      - os.ReadDir → 获取子目录列表
      - 对每个子目录 dir：
        - skillFile = filepath.Join(sourceDir, dir.Name(), "SKILL.md")
        - 若 skillFile 为常规文件 → 解析 frontmatter 获取 SkillInfo
          - name = frontmatter.name ?? dir.Name()
          - description = frontmatter.description + " " + frontmatter.when_to_use (截断 1536 字符)
          - user-invocable = frontmatter.user-invocable ?? true
          - model-invocable = !(frontmatter.disable-model-invocation ?? false)
        - 若同 name 的 skill 已存在 → 跳过（优先级低的被覆盖）
   b. 若为 commands/ 目录：
      - os.ReadDir → 获取 .md 文件列表
      - 对每个 .md 文件：
        - name = 文件名去 .md 扩展名
        - 解析 frontmatter 获取 SkillInfo
        - 若同 name 的 skill 已存在 → 跳过（skill 目录形式优先于 command 扁平文件）

4. 返回去重后的 SkillInfo 列表（按名称排序）
```

### 2. SKILL.md / command.md 解析

```
输入: filePath string, isFlatFile bool
输出: frontmatter map[string]any, body string, dirPath string, supportingFiles []string

1. content = os.ReadFile(filePath)
2. 检测 YAML frontmatter：
   - 首行是否为 "---"
   - 找到第二个 "---" → frontmatterRaw = 两行之间的内容
   - body = 第二个 "---" 之后的内容
   - 无 frontmatter → body = content
3. yaml.Unmarshal(frontmatterRaw, &raw) → map[string]any
4. 规范化 frontmatter 字段：
   - name: string, default = filepath.Base(filepath.Dir(filePath)) (目录形式)
                              或 文件名去 .md (扁平文件)
   - description: string, default = ""
   - when_to_use: string, default = ""
   - disable-model-invocation: bool, default = false
   - user-invocable: bool, default = true
   - argument-hint: string, default = ""
   - arguments: []string, 从空格分隔字符串或 YAML 列表解析
5. dirPath = filepath.Dir(filePath)
6. 若非扁平文件：
   - 扫描 dirPath 下所有非 "SKILL.md" 的常规文件
   - 递归到子目录
   - supportingFiles = 相对路径列表
7. 返回
```

### 3. Load 流程（完整渲染）

```
输入: name string, args string
输出: *LoadedSkill

1. 找到 SKILL.md 文件路径（按 List 的优先级顺序查找，包括 .claude/commands/）
   若未找到 → 返回 error("skill not found: {name}")

2. 解析 → frontmatter, body, dirPath, supportingFiles

3. ── 变量替换（顺序敏感）──
   argsList = shellSplit(args)  // shell 风格引号解析
   namedArgs = frontmatter.arguments  // ["environment", "branch"]

   替换前保护代码片段：`...` 行内代码和 ```...``` 围栏块替换为占位符，替换完成后还原。
   确保代码示例中的 $ARGUMENTS 等字面不被误替换。

   a. 替换 $ARGUMENTS[N] → argsList[N]（先替换索引形式，避免被 $ARGUMENTS 误匹配）
   b. 替换 $N → argsList[N]
   c. 替换 $ARGUMENTS → args
   d. 对 namedArgs 中每个 name（按 index）：
      - $name → argsList[index]（index 越界时替换为空字符串）
   e. 替换 ${CLAUDE_SESSION_ID} → sessionID
      （同时支持 ${WAVELOOM_SESSION_ID}，等价替换）
   f. 替换 ${CLAUDE_SKILL_DIR} → dirPath
      （同时支持 ${WAVELOOM_SKILL_DIR}，等价替换）
   g. 替换 ${CLAUDE_EFFORT} → effort
      （同时支持 ${WAVELOOM_EFFORT}，等价替换）
   h. 处理转义：\$ → $（恢复字面 $）

4. ── 动态注入 ──
   扫描 body 中每一行：
   a. 行内形式: !`command`
      - 正则: ^\s*!`([^`]+)` → 捕获 command
      - 执行 command（30s 超时）
      - 输出替换整个 !`...` 占位符
      - 注意：! 必须在行首或紧跟空白；否则视为字面文本
   b. 多行形式: ```! ... ```
      - 代码块以 ```! 开头（language = "!"）
      - 块内容作为 command 执行
      - 输出替换整个代码块

   命令执行:
   - ctx, cancel := context.WithTimeout(30s)
   - cmd := exec.CommandContext(ctx, "sh", "-c", command)
   - cmd.Dir = dirPath（以 SKILL.md 所在目录为工作目录）
   - 捕获 stdout + stderr
   - 退出码非 0 → 输出包含 stderr 并标注 "[command exited with code N]"

   安全约束:
   - 仅当 skill 来源不是远程/不可信路径时允许动态注入
   - 首版对所有本地 skill 启用（对标 Claude Code）

5. ── 追加附属文件清单 ──
   若 supportingFiles 非空：
     body += "\n\n## Supporting files\n\n"
     body += "The following files are available in the skill directory. "
     body += "Use read_file to load their content when needed:\n\n"
     for each file in supportingFiles:
       body += "- " + file + "\n"

6. ── $ARGUMENTS 追加 ──
    若原始 body（排除 `` ` `` 代码片段和 ` ``` ` 围栏块中的字面 `$ARGUMENTS`）中不含 `$ARGUMENTS` 变量，且 args 非空：
      body += "\n\nARGUMENTS: " + args
    （对标 Claude Code：skill 定义了参数但未使用 $ARGUMENTS 时自动追加）

7. 返回 LoadedSkill{
      Info:    SkillInfo{Name, Description, Args, UserInvocable, ModelInvocable, Source},
      Body:    body,
      DirPath: dirPath,
   }
```

### 4. FormatSkillListing

```
输入: skills []SkillInfo
输出: string（注入到 system prompt）

格式:
  ## Available Skills

  | Skill | Description |
  |-------|-------------|
  | /deploy | Deploy the application to production |
  | /summarize-changes | Summarize uncommitted changes and flag risks |

  To invoke a skill, either:
  - Type /skill-name [arguments] to invoke directly
  - Or call the skill tool with the skill name and arguments

注意：
  - description 已合并 when_to_use，截断到 1536 字符
  - disable-model-invocation 为 true 的 skill 不列入表格（对标 Claude Code：不注入上下文）
```

### 5. Shell 风格参数解析

```
输入: args string（如 `"production" "feature/login" extra`）
输出: []string

规则（对标 Claude Code）：
  - 空格分隔参数
  - 双引号包裹的值作为单个参数（如 "production staging" → ["production staging"]）
  - 不支持单引号（对标 Claude Code 行为）
  - 空字符串 → 空切片
```

---

## 三方集成

### 集成 1：Skill 工具（pkg/tool/）

新增 `skill_tool.go`，实现 `TypedTool[SkillParams]`，注册为 `skill` 工具。

```go
package tool

// SkillParams 是 skill 工具的参数。
type SkillParams struct {
    Name      string `json:"name"`      // skill 名称
    Arguments string `json:"arguments"` // 传入 skill 的参数（可选）
}

// SkillTool 让 LLM 可以调用用户定义的 skill。
// 实现 TypedTool[SkillParams]。
type SkillTool struct {
    loader *skill.Loader
}

func NewSkillTool(loader *skill.Loader) *SkillTool

// Tool 接口实现
func (t *SkillTool) Name() string             // "skill"
func (t *SkillTool) Description() string      // "Invoke a user-defined skill by name..."
func (t *SkillTool) Schema() json.RawMessage
func (t *SkillTool) ConcurrentSafe() bool     // false（修改上下文状态）
func (t *SkillTool) Execute(ctx context.Context, p SkillParams) (*ToolResult, error)
```

**Execute 流程：**

```
1. loaded, err := t.loader.Load(p.Name, p.Arguments)
   若 err → ToolResult{Error: ErrorClassRecoverable, Message: "Skill not found: ..."}

2. 返回 ToolResult{
      Content: loaded.Body,
      Meta: ToolMeta{
          // 通过 Meta 传递 skill 信息，供 TUI 渲染 paraSkill 段落
          FilePath:  loaded.DirPath,
          LineCount: len(loaded.Body),
          ByteCount: len(loaded.Body),
      },
   }
   → Loop 将 Content 作为 tool 消息追加 → LLM 读到 body → 按指令继续
   → TUI 检测工具名为 "skill"，渲染为 paraSkill 段落（而非普通 paraTool）
```

**ToolSpec 发送给 LLM：**

```json
{
  "name": "skill",
  "description": "Invoke a user-defined skill. Use this when a task matches an available skill's description. Call with skill name and optional arguments.",
  "parameters": {
    "type": "object",
    "properties": {
      "name": {
        "type": "string",
        "description": "The skill name (e.g., 'deploy', 'summarize-changes')"
      },
      "arguments": {
        "type": "string",
        "description": "Optional arguments to pass to the skill"
      }
    },
    "required": ["name"]
  }
}
```

### 集成 2：SlashCommand（pkg/slashcommand/）

新增 `SkillCommand`，将 `user-invocable` 的 skill 注册为 SlashCommand。修改 `Registry` 使其可以动态注册 skill 命令。

```go
package slashcommand

// SkillCommand 将 user-invocable skill 包装为 SlashCommand。
// 每个 skill 实例化一个 SkillCommand，注册到 Registry。
type SkillCommand struct {
    info   skill.SkillInfo
    loader *skill.Loader
}

func NewSkillCommand(info skill.SkillInfo, loader *skill.Loader) *SkillCommand

// Command 接口实现
func (c *SkillCommand) Name() string             // skill name
func (c *SkillCommand) Description() string      // skill description
func (c *SkillCommand) ArgsPlaceholder() string  // skill args hint
func (c *SkillCommand) Aliases() []string        // nil（skill 无别名）
func (c *SkillCommand) Execute(ctx context.Context, args string) (*Result, error)
```

**Execute 流程：**

```
1. loaded, err := c.loader.Load(c.info.Name, args)
   若 err → Result{Text: "Skill 不可用: ..."}

2. 返回 Result{
      Text: "",  // 不在 paraSystem 中显示
      SideEffects: []SideEffect{
        {Kind: SideEffectInvokeSkill, Detail: loaded.Body, Detail2: loaded.Info.Name, Detail3: args},
      },
   }
```

**新增 SideEffect 类型（command.go）：**

```go
const (
    // ... 现有常量 ...
    SideEffectInvokeSkill SideEffectKind = "invoke_skill" // TUI: 注入 skill body → doTurn
)
```

**TUI 处理（tui.go）— 统一显示路径：**

```go
case slashcommand.SideEffectInvokeSkill:
    skillBody := sideEffect.Detail
    skillName := sideEffect.Detail2
    skillArgs := sideEffect.Detail3
    // 统一：追加 paraTool 段落（默认展开，Markdown 渲染 body）
    m.paras = append(m.paras, Paragraph{
        Type:       paraTool,
        State:      stateDone,
        ToolName:   "skill",
        ToolArgs:   skillName + " " + skillArgs,
        ToolResult: skillBody,
    })
    // skill body 注入上下文
    m.cm.InjectSkill(skillBody)
    // 立即发起 LLM 调用
    go m.doTurn(skillBody)
```

### 集成 3：ContextManager（pkg/context/）

新增 `InjectSkill` 方法，将渲染后的 skill body 作为 user 消息注入。

```go
// InjectSkill 注入 skill body 作为 user 消息。
// 与 InjectUserInstructions 不同：skill body 作为一条独立的 user 消息追加，
// 而非插入到 system prompt 之后。
// 调用后立即通过 PrepareRun 将 body 发送给 LLM。
func (cm *ContextManager) InjectSkill(body string) {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    cm.messages = append(cm.messages, llm.Message{
        Role:    llm.RoleUser,
        Content: body,
    })
}
```

### 集成 4：system prompt 注入

在会话初始化时，将 skill 描述列表注入 system prompt。

**位置**：`cmd/waveloom/main.go` 中 system prompt 构造。

```
system prompt 末尾追加：

## Available Skills

| Skill | Description |
|-------|-------------|
| /deploy | Deploy the application to production |
| /summarize-changes | Summarize uncommitted changes and flag risks |

To invoke a skill, either type /skill-name [arguments] to invoke directly,
or call the skill tool with the skill name and arguments.
```

**注入时机**：会话启动 + /clear 重置后

**规则**：`disable-model-invocation: true` 的 skill **不**列入表格（对标 Claude Code）

### 集成 5：SlashCommand Registry 构造

修改 `cmd/waveloom/tui.go` 中的 Registry 构造流程：

```go
func newSlashRegistry(..., skillLoader *skill.Loader) *slashcommand.Registry {
    r := slashcommand.NewRegistry()

    // 内置命令（现有）
    r.Register(slashcommand.NewNewCommand(creator))
    r.Register(slashcommand.NewModelCommand(store, lister, currentModel))
    r.Register(slashcommand.NewThemeCommand())
    r.Register(slashcommand.NewHelpCommand(r))

    // 注册 user-invocable skills（新增）
    skills, _ := skillLoader.List()
    for _, info := range skills {
        if info.UserInvocable {
            r.Register(slashcommand.NewSkillCommand(info, skillLoader))
        }
    }

    return r
}
```

### 集成 6：Tool Registry 构造

修改 `cmd/waveloom/main.go` 中 `registerBuiltinTools`：

```go
func registerBuiltinTools(r tool.Registry, lspProvider *tool.LSPProvider, skillLoader *skill.Loader) {
    // ... 现有工具注册 ...

    // Skill 工具（新增）
    if skillLoader != nil {
        r.Register(tool.Wrap(tool.NewSkillTool(skillLoader)))
    }
}
```

---

## 启动流程（整合视图）

```
main()
  │
  ├─ 1. 加载 AGENTS.md（pkg/memory.Loader.Load）
  │
  ├─ 2. 构造 skill.Loader
  │     loader := skill.NewLoader(cwd, homeDir, sessionID, effort)
  │
  ├─ 3. 发现 skills
  │     skills, _ := loader.List()
  │
  ├─ 4. 构造 system prompt
  │     prompt := buildSystemPrompt(cwd) + envTools + loader.FormatSkillListing()
  │
  ├─ 5. 构造 ContextManager
  │     cm := ctxpkg.NewWithCompaction(prompt, ...)
  │     cm.InjectUserInstructions(agentsMdText)
  │
  ├─ 6. 构造 Tool Registry
  │     registerBuiltinTools(registry, lspProvider, loader)
  │
  ├─ 7. 构造 SlashCommand Registry
  │     newSlashRegistry(..., loader)
  │
  └─ 8. 启动 TUI / One-shot
```

---

## TUI 统一显示

两条路径均复用 `paraTool`，默认折叠一行标题 + Enter 展开提示，展开后 Markdown 渲染 body。

```
● skill deploy staging               Enter 展开
```

- SlashCommand 路径 → `paraTool{ToolName:"skill"}` + `InjectSkill` + `doTurn`
- Tool 路径 → `SkillTool.Execute` → Loop → `paraTool` 渲染

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/skill/skill.go` | Loader 类型 + SKILL.md 解析 + 发现逻辑 + 渲染引擎 + 附属文件扫描 + .claude/commands/ 兼容 |
| 新增 | `pkg/skill/skill_test.go` | 单元测试 |
| 新增 | `pkg/tool/skill_tool.go` | SkillTool 实现（TypedTool[SkillParams]） |
| 新增 | `pkg/tool/skill_tool_test.go` | SkillTool 单元测试 |
| 新增 | `pkg/slashcommand/skill.go` | SkillCommand 实现（Command 接口适配器） |
| 新增 | `pkg/slashcommand/skill_test.go` | SkillCommand 单元测试 |
| 修改 | `pkg/slashcommand/command.go` | 新增 SideEffectInvokeSkill 常量 |
| 修改 | `pkg/context/context.go` | 新增 InjectSkill 方法 |
| 修改 | `cmd/waveloom/main.go` | 构造 skill.Loader + 注入 skill listing 到 system prompt + 传递 loader 给 registerBuiltinTools |
| 修改 | `cmd/waveloom/tui.go` | newSlashRegistry 接受 loader 参数 + 注册 SkillCommand + handleSlashCommand 处理 SideEffectInvokeSkill → paraSkill 段落 |
| 修改 | `cmd/waveloom/tui_renderer.go` | toolArgsDisplay 新增 `case "skill"` 参数格式化 |
| 修改 | `go.mod` | 新增 gopkg.in/yaml.v3 依赖 |
| 新增 | `specs/skill-system.md` | 本规格书 |

---

## 不变量

1. **SKILL.md 格式不变**：完全兼容 Claude Code SKILL.md 语法（YAML frontmatter + Markdown body）
2. **双路径无冲突**：`.claude` 优先于 `.waveloom`；skill（目录形式）优先于 command（扁平文件）；同名 skill 以高优先级路径为准
3. **`.claude/commands/` 向后兼容**：扁平 `.md` 文件自动作为 skill 发现，用户已有文件无需迁移
4. **Skill body 注入不可逆**：一旦 skill body 进入消息历史，无法撤销（与用户消息一致）
5. **动态注入在渲染阶段**：`` !`cmd` `` 在 Load 时执行，LLM 只看到结果，看不到命令本身
6. **变量替换在动态注入前**：`$ARGUMENTS` 替换先于 `` !`cmd` `` 执行（命令中可以使用参数）
7. **/clear 重载 skills**：Reset 时重新扫描 skill 目录（对标 AGENTS.md /clear 重载）
8. **Skill Tool 不可并发**：`ConcurrentSafe() = false`（修改上下文状态）
9. **Skill 命令不经过 LLM**：用户 `/skill-name` 在 TUI 层拦截 → 渲染 → 注入 → 自动 doTurn（不消耗 LLM 调用判断）
10. **LLM 调用 skill 通过 Tool**：LLM 主动调用 skill → Tool System → 返回 body → LLM 继续
11. **Skill 描述始终在上下文**：system prompt 中包含所有 model-invocable skill 的摘要列表（对标 Claude Code skill listing）
12. **disable-model-invocation skill 不注入 system prompt**：仅通过 SlashCommand 暴露，LLM 默认不可见（对标 Claude Code）
13. **参数解析遵循 shell 引号规则**：双引号包裹参数，空格分隔（对标 Claude Code）
14. **$ARGUMENTS 无痛追加**：skill body 中无 `$ARGUMENTS` 且 args 非空时，自动追加 `ARGUMENTS: <value>`
15. **附属文件不自动加载**：仅将文件清单注入 body，LLM 按需通过 `read_file` 读取（对标 Claude Code）
16. **索引参数先于 $ARGUMENTS 替换**：`$ARGUMENTS[N]` 和 `$N` 先替换，避免 `$ARGUMENTS` 整体替换时误伤索引形式
17. **`\$` 转义在流程末尾处理**：确保变量替换完成后恢复字面 `$` 符号
18. **扁平 command 文件无附属文件**：附属文件清单仅适用于目录形式 skill
19. **TUI 统一显示**：两条路径均复用 `paraTool`，`● skill deploy staging` 默认折叠，Enter 展开 Markdown body

---

## 测试计划

### 单元测试（`pkg/skill/skill_test.go`）

1. **TestParseFrontmatter_Basic** — 解析含 name + description 的 YAML frontmatter
2. **TestParseFrontmatter_NoFrontmatter** — 无 frontmatter 的 SKILL.md 正确回退默认值
3. **TestParseFrontmatter_MalformedYAML** — 畸形 YAML → 返回错误
4. **TestParseFrontmatter_DisableModelInvocation** — disable-model-invocation 正确解析
5. **TestParseFrontmatter_UserInvocable** — user-invocable 正确解析
6. **TestParseFrontmatter_Arguments** — arguments 列表正确解析（空格分隔 + YAML 列表）
7. **TestParseFrontmatter_WhenToUse** — when_to_use 字段正确解析
8. **TestParseFrontmatter_AllFields** — 全字段综合解析
9. **TestDiscover_PersonalClaude** — ~/.claude/skills/ 下 skill 被发现
10. **TestDiscover_PersonalWaveloom** — ~/.waveloom/skills/ 下 skill 被发现
11. **TestDiscover_ProjectClaude** — .claude/skills/ 下 skill 被发现
12. **TestDiscover_ProjectWaveloom** — .waveloom/skills/ 下 skill 被发现
13. **TestDiscover_DuplicateName** — .claude 和 .waveloom 同名 skill → .claude 优先
14. **TestDiscover_NoSkillsDir** — 无任何 skill 目录 → 返回空列表，无 error
15. **TestDiscover_EmptySkillsDir** — skill 目录存在但为空 → 返回空列表
16. **TestDiscover_FlatCommands** — .claude/commands/*.md 文件被发现
17. **TestDiscover_SkillOverridesCommand** — 同名 skill（目录）和 command（文件）→ skill 优先
18. **TestDiscover_PersonalCommandClaude** — ~/.claude/commands/*.md 被发现
19. **TestDiscover_PersonalCommandWaveloom** — ~/.waveloom/commands/*.md 被发现
20. **TestLoad_RendersVariables** — $ARGUMENTS / $ARGUMENTS[N] / $0 / $1 正确替换
21. **TestLoad_NamedArguments** — frontmatter arguments 定义的 $name 正确替换
22. **TestLoad_SessionIDVariable** — ${CLAUDE_SESSION_ID} / ${WAVELOOM_SESSION_ID} 正确替换
23. **TestLoad_SkillDirVariable** — ${CLAUDE_SKILL_DIR} / ${WAVELOOM_SKILL_DIR} 正确替换
24. **TestLoad_EffortVariable** — ${CLAUDE_EFFORT} / ${WAVELOOM_EFFORT} 正确替换
25. **TestLoad_ArgsAutoAppend** — body 无 $ARGUMENTS 变量时自动追加 ARGUMENTS 行
26. **TestLoad_EmptyArgs** — args 为空时不追加 ARGUMENTS 行
27. **TestRegression_CodeSpanProtected** — 代码片段中的 $ARGUMENTS 不被替换，片段外正常替换
28. **TestRegression_CodeSpanAutoAppend** — 仅代码片段含 $ARGUMENTS 字面时仍触发自动追加
29. **TestLoad_DynamicInjection** — `` !`command` `` 正确执行并替换
30. **TestLoad_MultilineInjection** — ` ```! ` 块正确执行
31. **TestLoad_DynamicInjectionTimeout** — 命令超时（30s）→ 返回超时占位符
32. **TestLoad_DynamicInjectionError** — 命令退出码非 0 → 输出包含 stderr
33. **TestLoad_DynamicInjectionNotAtLineStart** — 非行首 `!` 不触发动态注入
34. **TestLoad_ShellArgsQuoting** — 双引号参数正确解析
35. **TestLoad_EscapedDollar** — `\$` 转义正确保留为 `$`
36. **TestLoad_SkillNotFound** — 加载不存在的 skill → error
37. **TestLoad_SupportingFiles** — 附属文件清单正确追加到 body 末尾
38. **TestLoad_NoSupportingFiles** — 无附属文件时不追加清单
39. **TestLoad_FlatCommandFile** — .claude/commands/*.md 正确加载（无附属文件）
40. **TestFormatSkillListing** — 列表格式正确，包含 skill 名和描述，合并 when_to_use
41. **TestFormatSkillListing_Empty** — 无 skill 时返回空字符串
42. **TestFormatSkillListing_ExcludesDisabledModelInvocation** — disable-model-invocation 的 skill 不出现
43. **TestFormatSkillListing_TruncatesDescription** — description + when_to_use 超过 1536 字符时截断
44. **TestReload** — Reload 重新扫描并反映文件变更

### SkillTool 测试（`pkg/tool/skill_tool_test.go`）

45. **TestSkillTool_Execute** — 调用存在的 skill → 返回渲染后 body
46. **TestSkillTool_NotFound** — 调用不存在的 skill → Recoverable error
45. **TestSkillTool_Schema** — Schema 格式符合 function calling 规范

### SkillCommand 测试（`pkg/slashcommand/skill_test.go`）

46. **TestSkillCommand_Name** — 命令名与 skill 名一致
47. **TestSkillCommand_Execute** — 返回 SideEffectInvokeSkill 副作用
48. **TestSkillCommand_NotFound** — skill 被删除后执行 → 返回错误文本

### ContextManager 测试

49. **TestInjectSkill_AppendsUserMessage** — InjectSkill 正确追加 user 消息
50. **TestInjectSkill_MultipleSkills** — 多次 InjectSkill 正确累积

### 集成测试

51. **TestIntegration_SkillUserInvocation** — 端到端：用户 /deploy staging → skill body 注入 → LLM 看到 body
52. **TestIntegration_SkillLLMInvocation** — 端到端：LLM 调用 skill 工具 → body 作为 tool result 返回
53. **TestIntegration_FlatCommandInvocation** — 端到端：.claude/commands/deploy.md → /deploy 正常调用
