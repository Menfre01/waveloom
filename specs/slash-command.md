# SlashCommand 组件规格书

## 组件定位

SlashCommand 是 Waveloom 的**本地命令解释层**，拦截用户输入中以 `/` 开头的文本，
在到达 Agent Loop 之前执行本地操作。它运行在 TUI/CLI 层，不消耗 LLM Token。

**核心区分：** 工具由 LLM 决策调用，slash 命令由用户直接输入触发。

## 参考来源

- Claude Code: `/clear`, `/compact`, `/config`, `/init`, `/doctor`
- Codex CLI: `core/src/commands/` — `ResetCommand`, `ConfigCommand`
- Waveloom 现有参考：`ContextManager.Reset()`、settings.json 模型、TUI @picker 基础设施

## 命令清单（P0，首版交付）

| 命令 | 别名 | 说明 |
|------|------|------|
| `/new` | `/clear` | 创建全新 session（新 session ID，全新上下文） |
| `/model [name]` | — | 无参显示模型列表（交互式选择），有参校验并切换模型 |
| `/theme` | — | 调起主题选择列表（Auto / Dark / Light），用户上下选择确认 |
| `/help` | — | 显示所有可用命令列表 |

> `/status` 不需要 — Footer HUD 已实时显示 session 状态。
> `/provider` 不需要 — 用户直接编辑 settings.json 修改即可。
> `/config` 不需要 — 用户直接编辑 settings.json 修改即可。
> `/skill` 不纳入本组件 — 后续作为独立 spec 实现。

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 放置位置 | `pkg/slashcommand/` | 与 tool、permission、context 平级，TUI 依赖它 |
| 注册方式 | Registry.Register() 构造期注册 | 与 Tool Registry 模式一致 |
| 参数解析 | 首个空格分割命令名和参数，后续空格属于参数 | `/model` 接受 0 或 1 个参数 |
| 输出 | `*Result`（含显示文本 + 副作用列表） | TUI 追加为 `paraSystem` 段落展示；特殊副作用触发覆盖层 |
| 自动补全 | 复用 @picker 的 list.Model 基础设施 | 输入 `/` 时弹出命令列表 |
| 上下文访问 | 构造器注入（每个命令接收其最小依赖接口） | 命令不感知 TUI，仅声明副作用 |
| 不归属 LLM | 命令描述不发送给 LLM | slash 是用户→客户端交互，LLM 不需要感知 |
| `/theme` UI | TUI 列表选择器覆盖层 | 复用 bubbles/list，与命令补全选择器的交互模式一致 |
| `/model` 热替换 | 调用 ListModels API 校验模型名，仅在校验通过后替换 llmClient 中的 model 字段 + 更新 HUD | API 失败时返回错误，不写入配置 |
| `/model` 列表 | 无参时通过 Provider API 动态获取可用模型列表，弹出交互式列表覆盖层 | API 失败时返回错误信息；复用 bubbles/list |
| Settings 全量读写 | SaveLLM 必须全量 read-modify-write | `llm.WriteSettingsFile` 只写 `{"llm":{...}}`，会破坏其他 section |

## 组件边界

### 输入
- `input string` — 用户输入的原始文本（以 `/` 开头）
- 各命令构造期注入的依赖接口（`SessionCreator`, `SettingsStore`）

### 输出
- `*Result` — 执行结果（含 TUI 渲染文本和副作用列表）
- `error` — 仅在系统级错误时返回

### 依赖（接口，非具体实现）
- `SessionCreator` — /new 所需（由 TUI 实现）
- `SettingsStore` — /model 所需（读写 llm section，内部实现全量 read-modify-write）
- `ModelLister` — /model 所需（通过 Provider API 获取可用模型列表）

### 不纳入本组件
- 命令的 TUI 渲染（由 TUI 的 `paraSystem` 段落负责）
- `/theme` 的主题选择列表覆盖层（由 TUI 负责）
- Skill 系统（独立 spec）
- settings.json 的全量编辑器（用户直接编辑文件即可）
- Agent Loop 相关逻辑

---

## 接口定义

> 所有类型定义在 `pkg/slashcommand/command.go`。
> `pkg/slashcommand/` 不 import Bubble Tea，不 import TUI 代码。
> 命令只声明副作用，TUI 执行实际操作。

### Command 接口 + 依赖接口 + Result + Registry

```go
package slashcommand

import (
    "context"
    "waveloom/pkg/llm"
)

// ── Command 接口 ──

type Command interface {
    Name() string
    Description() string
    Aliases() []string
    Execute(ctx context.Context, args string) (*Result, error)
}

// ── /new 所需 ──

// SessionCreator 由 TUI 实现，编排新 session 创建流程：
// 生成 session ID → 设置 sessionPath → ContextManager 初始化 → transcript 更新。
type SessionCreator interface {
    NewSession() error
}

// ── /model 所需 ──

// SettingsStore 抽象 settings.json 中 llm section 的读写。
// SaveLLM 内部实现全量 read-modify-write，确保其他 section 不丢失。
type SettingsStore interface {
    LoadLLM() (*llm.LLMSettings, error)
    SaveLLM(settings *llm.LLMSettings) error
}

// ModelLister 通过 Provider API 获取可用模型列表。
// 由 TUI 实现，内部调用 llm.Client.ListModels。
type ModelLister interface {
    ListModels(ctx context.Context) ([]llm.ModelInfo, error)
}

// ── Result ──

type SideEffectKind string

const (
    SideEffectNone            SideEffectKind = ""
    SideEffectSessionReset    SideEffectKind = "session_reset"     // TUI: 清空 paras + 追加通知
    SideEffectModelSwitched   SideEffectKind = "model_switched"    // TUI: 更新 HUD + 热替换 llmClient
    SideEffectOpenThemePicker SideEffectKind = "open_theme_picker" // TUI: 弹出主题选择列表
    SideEffectOpenModelPicker SideEffectKind = "open_model_picker" // TUI: 弹出模型选择列表（/model 无参时）
)

type SideEffect struct {
    Kind   SideEffectKind
    Detail string // model_switched → 新模型名；open_model_picker → 模型列表 JSON
}

type Result struct {
    Text        string
    SideEffects []SideEffect
}

// ── Registry ──

type Registry struct {
    commands map[string]Command
    aliases  map[string]string
}

func NewRegistry() *Registry
func (r *Registry) Register(cmd Command)
func (r *Registry) Match(input string) (Command, string)  // "/model v4" → (cmd, "v4")
func (r *Registry) List() []CommandInfo

type CommandInfo struct {
    Name        string
    Aliases     []string
    Description string
}
```

### 构造器

```go
func NewNewCommand(creator SessionCreator) *NewCommand
func NewModelCommand(store SettingsStore, lister ModelLister, currentModel string) *ModelCommand
func NewThemeCommand() *ThemeCommand
func NewHelpCommand(registry *Registry) *HelpCommand
```

---

## 命令规格

### `/new` — 新建 Session

```
构造器: NewNewCommand(creator SessionCreator)

调用 creator.NewSession():
  a. 生成新 session ID
  b. 设置新 sessionPath 到 ContextManager
  c. ContextManager.Reset()
  d. ContextManager.InjectUserInstructions(agentsMdText)
  e. 更新 Transcript 路径

输出: "新 session 已创建。"
副作用: SideEffectSessionReset → TUI 清空 paras + 追加通知 + Footer HUD 归零

别名: /clear
```

### `/model [name]` — 模型显示与切换

```
构造器: NewModelCommand(store SettingsStore, lister ModelLister, currentModel string)

无参数:
  1. 调用 lister.ListModels(ctx) 获取可用模型列表
  2. 若 API 失败 → 返回错误信息 "无法获取模型列表: <error>"，不做任何更改
  3. 若成功 → 返回 SideEffectOpenModelPicker，Detail = JSON 序列化的模型列表
     TUI 弹出模型选择列表覆盖层，当前模型高亮
     用户 Enter 确认 → TUI 调用 store.SaveLLM() + 热替换 llmClient + 更新 HUD
     用户 Esc 取消 → 关闭列表，不做任何更改

有参数:
  1. 调用 lister.ListModels(ctx) 获取可用模型列表
  2. 若 API 失败 → 返回 "无法获取模型列表，请检查网络连接后重试。"
     不写入 settings，不切换模型
  3. 若成功 → 校验 name 是否在列表中
     无效 → 返回 "未知模型: <name>。输入 /model 查看可用列表。"
     有效 → store.LoadLLM() → 修改 Model → store.SaveLLM()（全量 read-modify-write）
            返回 SideEffectModelSwitched，Detail = 新模型名

输出: "模型已切换为 deepseek-v4-pro。" 或错误信息
副作用: SideEffectModelSwitched 或 SideEffectOpenModelPicker

注意: 命令不操作 llmClient。TUI 收到副作用后执行对应操作。
      ModelLister 由 TUI 实现，内部委托给 llm.Client.ListModels()。
      API 失败时绝不静默降级写入 — 用户必须明确感知错误。
```

#### 模型选择列表覆盖层

```
┌─ 选择模型 ──────────────────────┐
│                                  │
│  ▶ deepseek-v4-pro              │
│    deepseek-v4-flash            │
│    gpt-4o                       │
│    ...                          │
│                                  │
│  ↑↓ 导航   Enter 确认   Esc 取消  │
└──────────────────────────────────┘
```

交互模式与 `/theme` 覆盖层一致：`↑↓` 导航，`Enter` 确认，`Esc` 取消。
确认后 TUI 负责：`store.SaveLLM()` + `handleModelSwap()` + `reconfigureLLMClient()`。

#### `llm.Client.ListModels` 接口

`/model` 的可用模型列表来源于 Provider API，而非硬编码。`llm.Client` 新增 `ListModels` 方法：

```go
// Client 接口（pkg/llm/client.go）
type Client interface {
    // ... 现有方法 ...

    // ListModels 获取 Provider 支持的模型列表。
    // 对应 GET /models（DeepSeek）或 GET /models（OpenAI）。
    ListModels(ctx context.Context) ([]ModelInfo, error)
}

// ModelInfo（pkg/llm/types.go）
type ModelInfo struct {
    ID      string `json:"id"`       // 模型标识符，如 "deepseek-v4-pro"
    Object  string `json:"object"`   // 对象类型，其值为 "model"
    OwnedBy string `json:"owned_by"` // 拥有该模型的组织
}
```

- `providerAdapter` 接口新增 `ListModels(ctx, httpClient) ([]ModelInfo, error)`
- DeepSeek adapter：`GET {baseURL}/models`，与 `GetBalance` 同模式
- OpenAI adapter：`GET {baseURL}/models`，与 `GetBalance` 同模式

### `/theme` — 主题选择

```
构造器: NewThemeCommand()  // 零依赖

返回 SideEffectOpenThemePicker → TUI 弹出主题选择列表覆盖层

列表内容: Auto（自动检测）/ Dark / Light
交互: ↑↓ 导航，Enter 确认，Esc 取消
确认后 TUI 应用主题 + 保存到 settings.json
```

```
┌─ 主题 ────────────────────────────┐
│                                   │
│  ▶ Auto（自动检测终端背景色）       │
│    Dark                           │
│    Light                          │
│                                   │
│  ↑↓ 导航   Enter 确认   Esc 取消   │
└───────────────────────────────────┘
```

### `/help` — 帮助

```
构造器: NewHelpCommand(registry *Registry)

输出: 格式化的命令表格（名称 | 别名 | 说明）
副作用: SideEffectNone
```

---

## 自动补全

- 用户输入 `/` 时弹出命令列表（复用 @picker 的 `list.Model` + `fuzzyFilter`）
- 覆盖层类型 `overlayCommandPicker`，仅 `↑` `↓` `Enter` `Esc` 可用
- `Enter` 自动补全命令名到输入框，`Esc` 关闭列表

```
> /mod_
  ┌─ 命令 ────────────────────────┐
  │ /model  显示/切换模型          │
  └────────────────────────────────┘
```

---

## TUI 集成点

### 注册表构造

```go
func newSlashRegistry(creator slashcommand.SessionCreator,
    store slashcommand.SettingsStore, lister slashcommand.ModelLister,
    currentModel string) *slashcommand.Registry {

    r := slashcommand.NewRegistry()
    r.Register(slashcommand.NewNewCommand(creator))
    r.Register(slashcommand.NewModelCommand(store, lister, currentModel))
    r.Register(slashcommand.NewThemeCommand())
    r.Register(slashcommand.NewHelpCommand(r))
    return r
}
```

### SettingsStore 实现（TUI 侧）

```go
type tuiSettingsStore struct {
    projectPath string
    globalPath  string
}

func (s *tuiSettingsStore) LoadLLM() (*llm.LLMSettings, error) {
    global, _ := llm.LoadSettingsIfExists(s.globalPath)
    project, _ := llm.LoadSettingsIfExists(s.projectPath)
    return llm.MergeLLMSettings(global, project), nil
}

// SaveLLM 全量 read-modify-write，保留其他 section。
func (s *tuiSettingsStore) SaveLLM(settings *llm.LLMSettings) error {
    full, _ := loadFullFile(s.projectPath, s.globalPath)  // map[string]any
    llmMap, _ := toMap(settings)
    full["llm"] = llmMap
    return writeFullFile(s.projectPath, full)
}
```

### ModelLister 实现（TUI 侧）

```go
// tuiModelLister 委托给 llm.Client.ListModels()。
// 在 TUI 构造期与 llmClient 同时创建，通过闭包注入 slash command。
type tuiModelLister struct {
    client llm.Client
}

func (l *tuiModelLister) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
    return l.client.ListModels(ctx)
}
```

### 输入拦截（handleKeyPress → Enter）

```
Enter → 若 userInput 以 "/" 开头:
            cmd, args := m.slashRegistry.Match(userInput)
            若 cmd != nil:
                result, _ := cmd.Execute(ctx, args)
                追加 paraSystem（若 result.Text 非空）
                switch SideEffect:
                  session_reset      → 清空 m.paras + 追加通知
                  open_theme_picker  → m.overlay = overlayThemePicker
                  open_model_picker  → m.overlay = overlayModelPicker; 从 Detail 反序列化模型列表
                  model_switched     → m.handleModelSwap(detail)
                return
            若 cmd == nil:
                追加 "未知命令: /xxx。输入 /help 查看可用命令。"
                return
        否则:
            doTurn(userInput)
```

### 副作用处理

```go
func (m *model) handleModelSwap(newModel string) {
    m.hudModel = newModel
    m.reconfigureLLMClient(newModel)
}

func (m *model) reconfigureLLMClient(newModel string) {
    // 读取最新 settings → 合并 newModel → llm.NewClient(...) → 替换 m.llmClient
}
```

### 覆盖层扩展

```go
type Overlay int
const (
    overlayNone           Overlay = iota
    overlayPermission
    overlayHelp
    overlayThemePicker            // /theme 触发（新增）
    overlayModelPicker            // /model 无参触发（新增）
    overlayCommandPicker          // / 命令补全（新增）
)
```

---

## 不变量

1. **不经过 LLM**：slash 命令的文本绝不发送给 LLM API
2. **不阻断 Loop**：slash 命令在 Loop 空闲时执行（`m.running == false`）
3. **副作用分离**：命令只声明副作用，具体执行由 TUI 负责
4. **命令名大小写不敏感**：`/New`、`/NEW`、`/new` 均有效
5. **Registry 不可变**：构造期注册，运行期不增删
6. **一致的基础设施**：命令补全与 @ 文件补全共享 list.Model + fuzzyFilter + overlay 路由
7. **构造器注入**：每个命令通过 `New*Command(...)` 接收最小依赖接口，`Execute(ctx, args)` 无全局 Dependencies
8. **网络错误透传，不静默降级**：`ListModels` API 失败时返回明确错误信息，不写入 settings，不静默回退

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/slashcommand/command.go` | Command 接口 + Result + SideEffect + SessionCreator + SettingsStore + ModelLister |
| 新增 | `pkg/slashcommand/registry.go` | Registry 实现 + Match + List + CommandInfo |
| 新增 | `pkg/slashcommand/new.go` | `/new` 命令（别名 /clear；依赖: SessionCreator） |
| 新增 | `pkg/slashcommand/model.go` | `/model` 命令（依赖: SettingsStore + ModelLister + currentModel） |
| 新增 | `pkg/slashcommand/theme.go` | `/theme` 命令（零依赖） |
| 新增 | `pkg/slashcommand/help.go` | `/help` 命令（依赖: Registry.List） |
| 新增 | `pkg/slashcommand/registry_test.go` | Registry 单元测试 |
| 修改 | `pkg/llm/types.go` | 新增 ModelInfo 类型 |
| 修改 | `pkg/llm/adapter.go` | providerAdapter 接口新增 ListModels |
| 修改 | `pkg/llm/adapter_deepseek.go` | DeepSeek adapter 实现 ListModels |
| 修改 | `pkg/llm/adapter_openai.go` | OpenAI adapter 实现 ListModels |
| 修改 | `pkg/llm/client.go` | Client 接口新增 ListModels + client 实现 |
| 新增 | `pkg/llm/adapter_deepseek_test.go` | DeepSeek ListModels 测试（追加到已有） |
| 新增 | `pkg/llm/adapter_openai_test.go` | OpenAI ListModels 测试（追加到已有） |
| 修改 | `cmd/waveloom/tui.go` | handleKeyPress Enter 拦截 + 副作用处理（含 open_model_picker）+ reconfigureLLMClient + 注册表构造 + tuiSettingsStore + tuiModelLister + SessionCreator 实现 |
| 修改 | `cmd/waveloom/tui_overlay.go` | overlayThemePicker / overlayModelPicker / overlayCommandPicker 覆盖层类型扩展 |
| 修改 | `cmd/waveloom/main.go` | runTUI 签名新增 globalPath, projectPath |
| 新增 | `specs/slash-command.md` | 本规格书 |
