# Reference Context 组件规格书

## 组件定位

Reference Context 是 Waveloom Code Agent 的**上下文引用展开层**，负责在用户输入发送到 Agent Loop 之前，解析用户输入中的 `@` 引用语法，将引用的文件内容或目录结构展开为实际内容，拼接进 user message。

组件包含两个正交子功能：
1. **路径展开器（Expander）** — 解析 `@path` → 读取文件/目录 → 拼接 `@@` 围栏块
2. **文件选择器（FilePicker 覆盖层）** — 用户输入 `@` 时弹出模糊查找列表，实时过滤，选择后回填路径

**核心价值：** 用户通过简洁的 `@` 语法精确指定上下文范围（一个文件、一个目录），无需手动复制粘贴或让 LLM 浪费 tool call 轮次去探索代码库。引用内容在发送前确定性地展开，LLM 看到的是一条"完整上下文 + 用户指令"的消息。

## 范围

本 Wave 实现 `@file` 和 `@folder` 两类引用，以及输入时 `@` 自动弹出文件选择器。暂不纳入：

- ❌ `@file:N-M` 行范围引用（后续 Wave）
- ❌ `@symbol` 符号引用（需语言感知索引）
- ❌ `@git` Git 上下文引用
- ❌ 引用缓存/增量更新

这些在后续 Wave 中按需添加。

## 参考来源

- Cursor: `@file`, `@folder`, `@symbol` 上下文引用语法 + 文件选择器
- GitHub Copilot: `#file`, `#symbol` 引用 + 文件选择器
- Cody (Sourcegraph): `@file`, `@symbol` 上下文 mention
- Claude Code: 无显式 `@` 语法，但在 system prompt 中引导模型主动调用工具探索代码库

**关键差异：** Claude Code 让 LLM 通过工具调用自行获取上下文，Waveloom 的 `@` 则是在发送前确定性地展开——两者互补。`@` 消除"探索式"tool call 的延迟和 token 开销，LLM 仍可额外调用工具补充信息。

### bubbles filepicker 组件评估

[charm.land/bubbles/v2](https://pkg.go.dev/charm.land/bubbles/v2) 提供了 `filepicker` 包，定位为"文件选择器"。评估后决定**不使用**：

| 维度 | bubbles filepicker | Waveloom 需求 | 匹配? |
|------|-------------------|--------------|-------|
| 数据源 | `os.ReadDir` 单目录逐层浏览 | 全工作区文件列表（`search_file **/*`） | ❌ |
| 交互模式 | 目录树导航（j/k 上下，h/l 进出目录） | 输入即模糊过滤（fzf 风格） | ❌ |
| 过滤能力 | 无——靠目录导航定位文件 | 实时模糊匹配（前缀 > 子串 > 首字母） | ❌ |
| 渲染密度 | 权限+大小+文件名 三列表格 | 仅路径名的紧凑列表（20 行覆盖层） | ❌ |
| 选择回填 | `DidSelectFile()` 返回绝对路径，外部自行处理 | `Enter` 后回填 `@relativePath` 到 textinput | 部分 |
| 隐藏文件 | `ShowHidden` 布尔开关 | 固定过滤 `.git` / `node_modules` 等 | 接近 |

**结论：** bubbles `filepicker` 是 vim `:e` 风格的**目录浏览器**，Waveloom 需要的是 **fzf 模糊查找器**。两者 UX 范式不同，继续使用 `search_file` + 内存模糊过滤方案。

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 展开时机 | 发送前展开（TUI 侧），而非让 LLM 调用工具 | 上下文引用是确定性操作，不应消耗 tool call 轮次 |
| `@file` 语义 | 读取文件完整内容并在消息中以 `@@` 围栏块呈现 | LLM 能清晰区分"用户原文"和"引用注入" |
| `@folder` 语义 | 列出目录结构（调用 `ls` 工具，depth=2），而非递归读取所有文件 | 目录引用关注的是"结构概览"，递归读取开销大且 token 爆炸 |
| 文件/目录判定 | 展开时通过 `os.Stat` 判定路径是文件还是目录，不依赖用户在 `@` 中显式区分 | 用户只需 `@path`，系统自动判断 |
| 展开结果格式 | 文件用 `@@` 围栏 + 代码块；目录用 `@@` 围栏 + 树形列表 | 统一 `@@` 边界，内容格式适配类型 |
| 文件读取 | expander 直接调用 `tool.Registry` 中的 `read_file` | 复用现有工具实现，不引入新的文件读取路径 |
| 目录列表 | expander 直接调用 `tool.Registry` 中的 `ls` | 复用现有工具实现 |
| 权限检查 | expander 调用工具时经过 `permission.Guard` | 读文件/列目录通常 allow，但安全底线不能绕过 |
| 解析策略 | 正则匹配 + 路径规范化，不依赖 glob 或其他花哨语法 | 保持简单；`@path` 就是路径的字面量 |
| 错误处理 | 展开失败时保留原始 `@ref` 文本，追加警告标记 | 用户能看到引用未展开，LLM 也能看到并自行处理 |
| 段落模型 | 展开后的引用内容作为 user 段落的一部分，不新增段落类型 | 最小侵入，user 段落内容就是"引用 + 指令" |
| 文件选择器触发 | 用户在输入框中键入 `@` 后，自动弹出覆盖层 | 无需额外快捷键，自然交互 |
| 文件选择器类型 | **非阻断式**覆盖层（类似 Help），Esc 关闭 | 用户可以不选，继续手动输入路径或直接发送 |
| 文件列表来源 | 通过 `search_file` 工具扫描工作区，实时模糊过滤 | 复用现有工具，不引入新的文件遍历逻辑 |
| 目录后缀标识 | 目录项追加 `/` 后缀 | 视觉区分文件和目录，与 `ls` 工具风格一致 |
| `@` 有效性规则 | `@` 必须在行首或空格之后，且之后紧跟非空格字符 | 避免 `email@example.com` 被误解析；与选择器触发条件一致 |
| 展开内容大小限制 | 单文件 ≤ 32KB，总展开内容 ≤ 128KB；超出截断 + warning | 防止大文件爆掉上下文窗口 |
| 重复引用去重 | 按绝对路径去重，保留首次出现位置 | 同一文件多次引用只展开一次，避免 token 浪费 |
| 代码块语言标注 | 根据文件扩展名推测语言标识符，无匹配则省略语言标注 | 触发 LLM 的语法高亮感知，提升代码理解准确度 |
| `search_file` 输出解析 | `search_file` 仅返回文件路径；文件选择器的目录项从文件路径中提取祖先目录并去重 | 匹配 `search_file` 实际行为（只列文件，不列目录） |

## 组件边界

### 输入
- `userInput string` — 用户原始输入，可能包含 `@` 引用
- `cwd string` — 当前工作目录（用于解析相对路径和扫描文件列表）

### 输出
- `expandedInput string` — 展开后的文本（`@ref` 被替换为实际内容）
- `refs []ResolvedRef` — 已解析的引用列表（供 TUI 渲染高亮）

### 依赖
- `waveloom/pkg/tool` — `tool.Registry`（用于 `read_file`、`ls`、`search_file` 工具调用）
- `waveloom/pkg/permission` — `permission.Guard`（权限检查）
- `context.Context` — 取消/超时信号

### 不依赖
- 不依赖 LLM Client
- 不依赖 Agent Loop
- 不依赖 ContextManager（展开后的文本仍然通过 `PrepareRun` 进入）

---

## 接口定义

### pkg/reference — 展开器

```go
package reference

import (
	"context"

	"waveloom/pkg/permission"
	"waveloom/pkg/tool"
)

// Kind 表示引用的类型。
type Kind int

const (
	KindFile   Kind = iota // @file — 读取文件内容
	KindFolder             // @folder — 列出目录结构
)

// Ref 表示用户输入中解析出的一个 @ 引用。
type Ref struct {
	Raw  string // 原始文本，如 "@auth/login.go"
	Path string // 解析后的绝对路径
	Kind Kind   // 引用类型（文件或目录）
}

// ResolvedRef 表示一个已解析完成的引用，含展开后的内容。
type ResolvedRef struct {
	Ref
	Content string // 展开后的内容（文件内容带行号，或目录树形列表）
	Bytes   int    // 内容字节数
	Error   string // 展开失败时的错误信息（空 = 成功）
}

// Expander 负责解析和展开 @ 引用。
type Expander struct {
	registry tool.Registry
	guard    permission.Guard
}

// New 创建一个新的 Expander。
func New(registry tool.Registry, guard permission.Guard) *Expander

// Expand 解析 userInput 中的 @ 引用，展开为实际内容，返回替换后的文本。
//
// 展开逻辑:
//   1. 扫描 userInput，提取所有 @ref
//   2. 对每个 ref，通过 os.Stat 判定是文件还是目录
//   3. 文件 → 调用 read_file 获取内容
//      目录 → 调用 ls 获取树形列表
//   4. 将 @ref 替换为 @@ 围栏块包裹的实际内容
//   5. 展开失败时保留 @ref 原文 + 追加错误标记
//
// 返回:
//   - expanded: 替换后的完整文本
//   - refs: 已解析的引用列表（含内容和错误信息）
//   - err: 仅在系统级错误时非 nil（如 registry 不可用）
func (e *Expander) Expand(ctx context.Context, userInput string, cwd string) (expanded string, refs []ResolvedRef, err error)
```

### cmd/waveloom — 文件选择器（TUI 覆盖层）

文件选择器是 `model` 的内部状态，不单独导出包。关键新增：

```go
// 在 model struct 中新增字段
type model struct {
	// ... 现有字段 ...

	// @ 引用展开器
	expander *reference.Expander

	// 文件选择器状态
	pickerVisible    bool           // 文件选择器是否可见
	pickerFilter     string         // 当前过滤文本（@ 之后用户继续输入的内容）
	pickerItems      []pickerItem   // 当前过滤后的候选列表
	pickerIdx        int            // 当前高亮项索引
	pickerAllItems   []pickerItem   // 全量候选列表（@ 触发时缓存）
}

// pickerItem 表示文件选择器中的一个候选项。
type pickerItem struct {
	Path    string // 相对于 cwd 的路径
	IsDir   bool   // 是否为目录
	Display string // 渲染用的显示文本（目录带 / 后缀）
}
```

---

## 核心算法

### 1. 展开器算法

#### 解析（Parse）

```
输入: userInput = "看一下 @pkg/auth 这个目录和 @pkg/auth/login.go 的结构"
      cwd = "/home/user/project"

1. 正则扫描: /(?m)(?:^|\s)@([^\s]+)/g
   → 只匹配行首或空格后的 @，排除 email@example.com 等误匹配
   → 匹配 ["@pkg/auth", "@pkg/auth/login.go"]

2. 对每个匹配，解析为 Ref:
   a. 去掉 @ 前缀，获取原始路径: "pkg/auth", "pkg/auth/login.go"
   b. 路径规范化:
      - 相对路径 → filepath.Join(cwd, path)
      - 绝对路径 → 直接使用
      - filepath.Clean 清理 .. 和 .
   c. 通过 os.Stat 判定 Kind:
      - 路径存在且为目录 → KindFolder
      - 路径存在且为常规文件 → KindFile
      - 路径不存在 → KindFile（先假定文件，展开时再报错）
   d. 构造 Ref{Raw: "@pkg/auth", Path: "/abs/path/pkg/auth", Kind: KindFolder}

3. 去重：按 Path 字段去重，保留首次出现的 Ref

4. 返回 []Ref
```

#### 展开（Expand）

```
输入: refs []Ref

常量:
  MAX_FILE_BYTES = 32KB   // 单文件上限
  MAX_TOTAL_BYTES = 128KB // 总展开内容上限

对每个 Ref:

前置检查:
  - 如果当前 totalBytes >= MAX_TOTAL_BYTES → 跳过后续 ref，追加截断警告
  - 权限检查: guard.Check(ctx, "read_file"|"ls", input) → ASK/DENY → 跳过

分支 1 — KindFile:
  1. 调用 tool.Registry.Execute(ctx, "read_file", json.RawMessage(`{
       "file_path": "{ref.Path}"
     }`))
  2. 检查结果:
     - 成功 → 截断: 如果 len(result.Content) > MAX_FILE_BYTES，取前 MAX_FILE_BYTES + 追加 `[truncated]` 标记
       ResolvedRef{
         Content: result.Content,
         Bytes:   len(result.Content),
       }
     - 失败 → ResolvedRef{ Error: result.Error.Message }

分支 2 — KindFolder:
  1. 调用 tool.Registry.Execute(ctx, "ls", json.RawMessage(`{
       "path": "{ref.Path}",
       "depth": 2
     }`))
  2. 同上成功/失败处理

3. 累加 totalBytes += len(ResolvedRef.Content)

4. 构造替换文本:

   KindFile 成功:
     @@ {相对路径} (file)
     ```{语言标识符}
     {带行号的代码内容}
     ```
     @@

   KindFolder 成功:
     @@ {相对路径} (directory)
     ```
     {树形目录结构}
     ```
     @@

   失败（file 或 folder）:
     @@ {原始 @ref}  [not found]
     @@

语言标识符推测规则（文件扩展名 → 标识符）:

| 扩展名 | 语言标识符 |
|--------|-----------|
| .go | go |
| .py | python |
| .js, .ts, .tsx, .jsx | javascript / typescript |
| .rs | rust |
| .java | java |
| .c, .h | c |
| .cpp, .hpp, .cc, .hh, .cxx, .hxx | cpp |
| .sh, .bash | bash |
| .yaml, .yml | yaml |
| .json | json |
| .toml | toml |
| .md, .mdx | markdown |
| .sql | sql |
| .proto | protobuf |
| .css, .scss, .less | css |
| .html, .htm | html |
| .xml | xml |
| .Dockerfile | dockerfile |
| Dockerfile | dockerfile |
| Makefile | makefile |
| 其他 / 无扩展名 | 省略语言标注（` ```  `） |
```

#### 替换（Replace）

```
输入: userInput, []ResolvedRef

替换位置策略：引用块统一放在用户消息的顶部（所有 @ref 被移除，合并为上下文块），
原始指令文本保留在下方。LLM 先看到上下文，再看到指令，符合"先给材料再提问"的认知顺序。

算法:
  1. 从 userInput 中移除所有 @ref token
  2. 构建上下文块: 遍历 ResolvedRef，将每个成功展开的 Content 拼接为 @@ 围栏块
  3. 最终消息 = 上下文块 + 空行 + 清理后的用户指令

示例:
  输入: "看一下 @pkg/auth 这个目录和 @pkg/auth/login.go 的结构"
  输出:
    @@ pkg/auth (directory)
    ```
    pkg/auth/
      login.go
      logout.go
      middleware/
        rate_limiter.go
    ```
    @@

    @@ pkg/auth/login.go (file)
    ```go
    [1] package auth
    [2]
    [3] import (
    [4]     "context"
    [5]     "fmt"
    [6] )
    [7]
    [8] // LoginManager 负责用户认证。
    [9] type LoginManager struct {
   [10]     store SessionStore
   [11] }
    ...
    ```
    @@

    看一下 这个目录和 的结构
```

**注意：** 当 @ref 被移除后，用户原文可能会留下不自然的空格或语法断裂（如上例"看一下 这个目录和 的结构"）。
这是可接受的——LLM 能理解上下文块 + 残余指令的意图。后续 Wave 可优化为用占位符替换，但当前保持简单。

---

### 2. 文件选择器算法

#### 触发检测

```
在 Update() 中，每次 tea.KeyPressMsg 后检查 textinput.Value():

1. 找到输入框中最后一个 @ 的位置
2. @ 的触发条件（满足任一即可）:
   - @ 是行首字符
   - @ 前面是空格
3. 如果满足触发条件，且当前 pickerVisible == false:
   → 设置 pickerVisible = true
   → 从 @ 之后到光标位置提取 pickerFilter
   → 调用 scanFiles() 获取全量候选列表
   → 调用 filterItems() 生成 pickerItems
   → pickerIdx = 0
4. 如果 pickerVisible == true:
   → 更新 pickerFilter = @ 之后的文本
   → 重新 filterItems()
5. 如果输入框中不再有有效的 @（用户删掉了 @ 或空格前无 @）:
   → pickerVisible = false
```

#### 文件扫描（scanFiles）

```
输入: cwd string

1. 调用 tool.Registry.Execute(ctx, "search_file", json.RawMessage(`{
     "pattern": "**/*",
     "working_dir": "{cwd}"
   }`))

2. 解析 ToolResult.Content 为文件路径列表:
   `search_file` 实际输出格式（仅文件，不含目录）:
     Found 5 file(s) matching "**/*" in . (1ms):
     pkg/auth/login.go
     pkg/auth/login_test.go
     pkg/llm/client.go

   解析算法:
     - 按 `\n` 分割
     - 跳过首行（摘要头）和截断警告行（以 "⚠️" 开头）
     - 去除首尾空白
     - 跳过空行
     - 每行即为一个文件相对路径

3. 构建文件候选列表:
   - 每个文件路径 → pickerItem{Path: path, IsDir: false, Display: path}

4. 从文件路径中提取祖先目录:
   - 对每个文件路径 "a/b/c/file.go"，提取 "a/", "a/b/", "a/b/c/"
   - 去重后构建目录项 → pickerItem{Path: dir, IsDir: true, Display: dir（末尾 `/`）}
   - 不包含仅含隐藏目录的路径段（如 ".git/objects/pack" 整个跳过）

5. 过滤隐藏目录和文件:
   - 路径中任一段以 `.` 开头 → 过滤
   - 常见巨型目录也过滤: `node_modules`, `__pycache__`, `vendor`, `dist`, `build`

6. 二进制文件过滤（按扩展名）:
   `.exe`, `.dll`, `.so`, `.dylib`, `.o`, `.a`, `.class`, `.pyc`, `.jar`,
   `.war`, `.zip`, `.tar`, `.gz`, `.bz2`, `.7z`, `.rar`, `.png`, `.jpg`,
   `.jpeg`, `.gif`, `.ico`, `.pdf`, `.woff`, `.woff2`, `.ttf`, `.eot`, `.wasm`

7. 限制: 合并后最多保留 500 项（文件 + 目录），超过后按字母序截断并追加 warning

8. 按 Display 字母排序，目录排在文件前面
```

#### 模糊过滤（filterItems）

```
输入: filter string, allItems []pickerItem

如果 filter 为空 → 返回 allItems 的前 20 项

否则:
  1. 将 filter 按小写规范化
  2. 对每个 item，计算模糊匹配分数:
     - 前缀匹配: item.Display 以 filter 开头 → 优先级最高
     - 子串匹配: item.Display 包含 filter → 优先级次之
     - 首字母匹配: item 的每个路径段首字母匹配 filter → 优先级最低
  3. 排序（优先级高 → 低，同优先级字母序）
  4. 返回前 20 项

注意: 过滤是对缓存的全量列表做内存操作，不重新调用 search_file。
```

#### 选择确认

```
Enter 键（仅在 pickerVisible 时）:
  1. 获取 pickerItems[pickerIdx].Path
  2. 在 textinput.Value() 中定位最后一个 @ 的位置
  3. 将 @ 及其后的文本替换为 @{选中的路径}
  4. pickerVisible = false
  5. 焦点回到 textinput（用户可继续输入或再按 Enter 发送）

Esc 键（仅在 pickerVisible 时）:
  1. pickerVisible = false
  2. @ 及其后的过滤文本保留在输入框中（用户可以选择手动输入完整路径）
```

---

## 文件选择器覆盖层规范

### 触发条件

- 用户在输入框中键入 `@`，且 `@` 位于行首或空格之后
- 覆盖层在 Footer HUD 上方弹出，**不阻断**对话流（类似 Help）
- 用户可继续输入文本作为过滤条件
- `Esc` 关闭、`Enter` 确认选择、`↑↓` 导航

### 渲染

```
┌─ 文件选择器 ──────────────────────────────────────────────────────┐
│  @auth/lo█                                                       │
│                                                                   │
│  ▶ pkg/auth/login.go                                             │
│    pkg/auth/login_test.go                                        │
│    pkg/auth/logout.go                                            │
│    pkg/auth/middleware/                                          │
│                                                                   │
│  ↑↓ 导航   Enter 确认   Esc 关闭      (5 项)                       │
└──────────────────────────────────────────────────────────────────┘
```

### 样式

| 元素 | 样式 |
|------|------|
| 覆盖层边框 | `colorOverlayBorder`（`#5f5faf`），单线框 |
| 标题行 | "文件选择器"，`colorOK` 绿，加粗 |
| 过滤文本 | 暗背景上的亮前景，`colorHeaderFg` |
| 当前高亮项 | `▶` 前缀 + `colorOK` 绿 + Bold |
| 非高亮项 | 普通前景 `colorHeaderFg` |
| 目录项 | 路径末尾 `/`，颜色与文件区分（`colorMDAccent` 金） |
| 底部提示 | `colorMuted` 暗灰 |
| 背景 | `colorOverlayBg`（`#2a2a3e`） |

### 键盘路由

文件选择器活跃时（`pickerVisible == true`），`handleKeyPress()` 优先路由：

| 按键 | 行为 |
|------|------|
| `↑` | `pickerIdx--`（循环到底部） |
| `↓` | `pickerIdx++`（循环到顶部） |
| `Enter` | 确认选择，回填路径到输入框，关闭选择器 |
| `Esc` | 关闭选择器，保留输入框当前内容 |
| `PgUp` | 上翻 5 项 |
| `PgDn` | 下翻 5 项 |
| 其他可打印字符 | 正常传递给 textinput，触发 re-filter |

### 与现有覆盖层的关系

文件选择器使用**独立布尔标志** `pickerVisible`，不与现有 `overlay` 枚举冲突：

```go
type Overlay int

const (
    overlayNone       Overlay = iota // 无覆盖层
    overlayPermission                // 权限确认框（阻断式）
    overlayHelp                      // 帮助（参考式）
)

// pickerVisible 独立于 overlay 字段——文件选择器可以在 overlayNone
// 状态下激活，但不可与 permission/help 同时出现。
```

**互斥规则：**
- `pickerVisible == true` 时：`overlay == overlayNone` 且 `running == false`
- Permission 弹出时 (`overlay == overlayPermission`)：强制 `pickerVisible = false`
- 用户按 `Enter` 发送消息（`doTurn`）时：强制 `pickerVisible = false`

---

## 状态图

```
┌──────────────────────────────────────────────────────────────────┐
│                        输入态 (Idle)                              │
│  用户在 textinput 中输入...                                        │
└────────────┬─────────────────────────────────────────────────────┘
             │
             │ 输入 @
             ▼
┌────────────────────────┐     ┌─────────────────────────────────┐
│   文件选择器活跃        │────▶│ 用户继续输入 → filterItems()     │
│   pickerVisible=true   │     │ ↑↓ 导航                         │
│   显示候选列表          │     │ Enter → 回填路径, 关闭选择器     │
│                        │     │ Esc  → 关闭选择器, 保留输入      │
└───────────┬────────────┘     └─────────────────────────────────┘
            │
            │ 用户 Enter 发送（含 @ref）
            ▼
   ┌──────────────┐
   │   Parse      │  正则扫描 → os.Stat 判定文件/目录 → []Ref
   └──────┬───────┘
          │
          ▼
   ┌──────────────┐     ┌─────────────────┐
   │   Expand     │────▶│ Guard.Check()   │  每个 ref 经过权限检查
   │  (per ref)   │     │ (read_file/ls)  │
   └──────┬───────┘     └─────────────────┘
          │                    │
     ┌────┴────┐         allow │     deny
     │         │               │
     ▼         ▼               ▼
   File      Folder     ┌──────────────┐
   read_file  ls        │ 失败: 保留    │
     │         │        │ @ref + 错误   │
     ▼         ▼        │ 标记          │
   ┌──────────────┐     └──────┬───────┘
   │ 成功: 构造    │            │
   │ @@ 围栏块    │            │
   │ 代码 / 树形  │            │
   └──────┬───────┘            │
          │                   │
          └─────────┬─────────┘
                    ▼
           ┌──────────────┐
           │   Replace    │  拼接最终消息
           │  合并上下文块 │  = @@块... + 用户指令
           └──────┬───────┘
                  │
                  ▼
           ┌──────────────┐
           │ PrepareRun   │  传给 ContextManager（现有链路）
           └──────────────┘
```

---

## 集成点

### `cmd/waveloom/main.go`

```go
// 8.5 创建 @ 引用展开器（文件选择器在 TUI 内部懒初始化）
expander := reference.New(registry, guard)

// ... 现有初始化代码 ...

// 11. 分支：无 prompt → 交互式 TUI，有 prompt → 单次执行
if cfg.OneShot == "" {
    runTUI(llmClient, registry, guard, expander, cfg.Model, cfg.Theme, verboseLog, cfg.ContextLimit, ctxMgr)
    return
}

runOneShot(cfg, llmClient, registry, guard, expander, cwd, verboseLog, ctxMgr)
```

**`runTUI` 签名变更：**

```go
func runTUI(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int, ctxMgr *ctxpkg.ContextManager) {
    m := newTUIModel(llmClient, registry, guard, expander, modelName, theme, verboseLog, contextLimit)
    // ...
}
```

**`newTUIModel` 签名变更：**

```go
func newTUIModel(llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, modelName string, theme string, verboseLog io.Writer, contextLimit int) *model {
    // ...
    return &model{
        // ... 现有字段 ...
        expander: expander,
        // ...
    }
}
```

### `cmd/waveloom/runner.go`

单次模式仅接入 Expander（无文件选择器）：

```go
func runOneShot(cfg CLIConfig, llmClient llm.Client, registry tool.Registry, guard permission.Guard, expander *reference.Expander, cwd string, verboseLog io.Writer, cm *ctxpkg.ContextManager) {
    // ... 现有管道处理 ...

    // 展开 @ 引用
    expandedInput, _, err := expander.Expand(context.Background(), userInput, cwd)
    if err != nil {
        expandedInput = userInput
    }

    // 通过 Context Manager 获取完整消息历史
    messages := cm.PrepareRun(expandedInput)
    // ...
}
```

### TUI 集成（`cmd/waveloom/tui.go`）

#### model 新增字段

```go
type model struct {
    // ... 现有字段 ...

    // @ 引用展开器
    expander *reference.Expander

    // 文件选择器
    pickerVisible  bool
    pickerFilter   string
    pickerItems    []pickerItem
    pickerIdx      int
    pickerAllItems []pickerItem // 全量列表（@ 触发时扫描一次，后续过滤在内存中完成）
}
```

#### handleKeyPress 修改

在 `handleKeyPress` 中，文件选择器活跃时优先消费按键：

```go
func (m *model) handleKeyPress(msg tea.KeyPressMsg) (bool, tea.Cmd) {
    // 文件选择器活跃时优先路由
    if m.pickerVisible {
        return m.handlePickerKey(msg)
    }

    // 现有逻辑: 覆盖层 → 全局快捷键 → 未消费
    // ...
}
```

#### handlePickerKey 新增

```go
func (m *model) handlePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
    keyStr := msg.String()

    switch keyStr {
    case "esc":
        m.pickerVisible = false
        return true, nil

    case "enter":
        if m.pickerIdx >= 0 && m.pickerIdx < len(m.pickerItems) {
            m.commitPickerSelection()
        }
        m.pickerVisible = false
        return true, nil

    case "up":
        if m.pickerIdx > 0 {
            m.pickerIdx--
        } else {
            m.pickerIdx = len(m.pickerItems) - 1
        }
        return true, nil

    case "down":
        if m.pickerIdx < len(m.pickerItems)-1 {
            m.pickerIdx++
        } else {
            m.pickerIdx = 0
        }
        return true, nil

    case "pgup":
        m.pickerIdx = max(m.pickerIdx-5, 0)
        return true, nil

    case "pgdown":
        m.pickerIdx = min(m.pickerIdx+5, len(m.pickerItems)-1)
        return true, nil

    default:
        // 可打印字符 → 传给 input，Update() 中会触发 re-filter
        return false, nil
    }
}
```

#### Update 中的过滤器同步

```go
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // ... 在 KeyPressMsg 处理后，textinput 更新后 ...

    if m.pickerVisible {
        // 从 input 值中提取当前 @ 之后的过滤文本
        m.pickerFilter = extractFilterAfterAt(m.input.Value())
        m.pickerItems = fuzzyFilter(m.pickerFilter, m.pickerAllItems)
        if m.pickerIdx >= len(m.pickerItems) {
            m.pickerIdx = 0
        }
    } else {
        // 检测是否需要激活选择器
        if shouldActivatePicker(m.input.Value()) && !m.running && m.overlay == overlayNone {
            m.activatePicker()
        }
    }

    // ... 其余逻辑 ...
}
```

#### 辅助函数

```go
// shouldActivatePicker 检测输入框当前光标位置是否在有效的 @ 之后。
// 有效: @ 在行首或空格之后，且光标在 @ 之后（含同一位置）。
func shouldActivatePicker(value string) bool {
    // 找到最后一个 @
    idx := strings.LastIndex(value, "@")
    if idx < 0 {
        return false
    }
    // @ 前必须是行首或空格
    if idx > 0 && value[idx-1] != ' ' {
        return false
    }
    // @ 之后不能已经包含空格（避免输入完整路径后重新触发）
    afterAt := value[idx+1:]
    if strings.Contains(afterAt, " ") {
        return false
    }
    return true
}

// extractFilterAfterAt 提取最后一个 @ 之后的文本作为过滤条件。
func extractFilterAfterAt(value string) string {
    idx := strings.LastIndex(value, "@")
    if idx < 0 {
        return ""
    }
    return value[idx+1:]
}

// commitPickerSelection 将选中路径回填到 textinput。
func (m *model) commitPickerSelection() {
    if m.pickerIdx < 0 || m.pickerIdx >= len(m.pickerItems) {
        return
    }
    selected := m.pickerItems[m.pickerIdx].Path
    value := m.input.Value()
    atIdx := strings.LastIndex(value, "@")
    if atIdx < 0 {
        return
    }
    // 替换 @ 及其后的内容为 @{selectedPath}
    newValue := value[:atIdx] + "@" + selected
    m.input.SetValue(newValue)
    // 光标移到末尾
    m.input.CursorEnd()
}
```

#### doTurn 修改

```go
func (m *model) doTurn(userInput string) tea.Cmd {
    // 关闭文件选择器（如有）
    m.pickerVisible = false

    // 0. 解析并展开 @ 引用
    expanded, refs, err := m.expander.Expand(context.Background(), userInput, m.cwd)
    if err != nil {
        expanded = userInput
    }

    // 1. PrepareRun — 使用展开后的输入
    messagesSnapshot := m.cm.PrepareRun(expanded)

    // 2. 追加 user 段落
    m.paras = append(m.paras, Paragraph{
        Type:  paraUser,
        State: stateDone,
        Text:  userInput,
    })

    // ... 后续不变
}
```

#### doReset 修改

`doReset` 重置上下文时需关闭文件选择器：

```go
func (m *model) doReset() {
    // 关闭文件选择器（如有）
    m.pickerVisible = false

    // ... 现有清理逻辑 ...
}
```

### 单次模式集成（`cmd/waveloom/runner.go`）

单次模式**不使用**文件选择器（无 TUI）。Expander 接入代码见上文 `### cmd/waveloom/runner.go` 部分。

### TUI 渲染集成（`cmd/waveloom/tui_renderer.go`）

User 段落中 `@ref` 使用高亮样式渲染（蓝色/青色前景），让用户直观看到哪些引用被识别：

```
> 看一下 @pkg/auth 这个目录和 @pkg/auth/login.go 的结构
          ^^^^^^^^               ^^^^^^^^^^^^^^^^^^ 高亮蓝色
```

展开后的 `@@` 围栏块作为 user 段落内容的一部分渲染（代码块有语法高亮，目录树保持等宽）。

### View 集成（`cmd/waveloom/tui.go`）

文件选择器在 View() 中叠加渲染，位置在 Input 上方、Viewport 下方——与 Permission 覆盖层的缩小 viewport 策略不同，选择器是**非阻断式**的，直接覆盖在 Input 上方区域：

```go
func (m *model) View() tea.View {
    // ... 现有三明治布局 ...

    // 文件选择器覆盖层（在 input 上方插入，不缩小 viewport）
    var mainBody string
    if m.pickerVisible {
        pickerOverlay := renderFilePicker(
            m.pickerFilter,
            m.pickerItems,
            m.pickerIdx,
            contentWidth,
        )
        mainBody = lipgloss.JoinVertical(
            lipgloss.Left,
            header,
            "",
            m.viewport.View(),
            pickerOverlay,
            separator,
            inputView,
            footer,
        )
    } else if m.overlay == overlayPermission && m.permReq != nil {
        // ... 现有权限覆盖层逻辑 ...
    } else {
        // ... 现有正常布局 ...
    }

    // ...
}
```

### `cmd/waveloom/tui_renderer.go` — `renderFilePicker`

```go
// renderFilePicker 渲染文件选择器覆盖层。
//
// 参数:
//   - filter: 当前过滤文本（@ 之后的用户输入）
//   - items:  过滤后的候选项
//   - idx:    当前高亮索引（-1 表示无高亮）
//   - width:  覆盖层宽度
//
// 返回: 覆盖层的 lipgloss 渲染字符串（含边框）。
//
// 渲染约定:
//   - 标题行 "文件选择器" + filter 展示
//   - 每个 item: "  " + Display（高亮项 ▶ 前缀 + 绿色加粗）
//   - 底栏: "↑↓ 导航   Enter 确认   Esc 关闭"
//   - 边框: 单线框，colorOverlayBorder
//   - 最大高度: 12 行（含边框），超出部分不可见
func renderFilePicker(filter string, items []pickerItem, idx int, width int) string
```

实现细节:
- 使用 `lipgloss.NewStyle().Border(lipgloss.NormalBorder())` 绘制单线边框
- 列表区域高度 = min(len(items), 8)，超过 8 项时显示 idx 附近的窗口（类似 viewport 跟随）
- 空列表时显示 "无匹配文件"

---

1. **展开是幂等的：** 对同一个 `userInput` 多次调用 `Expand` 得到相同结果（文件内容不变的前提下）
2. **原始输入保留：** TUI 显示用户原始输入（含 `@ref` 高亮），LLM 收到展开后的版本
3. **权限不绕过：** 每个 `read_file` / `ls` 调用都经过 `permission.Guard.Check()`
4. **失败可见：** 展开失败的 `@ref` 保留在文本中，追加 `[not found]` 标记，LLM 可自行处理
5. **路径安全：** 所有路径通过 `filepath.Clean` 规范化，拒绝 `..` 提权
6. **不修改 ContextManager：** 展开后的文本只是 user message 的内容，对 `ContextManager` 和 `agentloop.Loop` 完全透明
7. **类型自动判定：** 用户无需在 `@` 中区分文件还是目录——`os.Stat` 自动判定，`KindFile` / `KindFolder` 只是内部优化路径
8. **选择器非阻断：** 文件选择器活跃时，用户仍可通过 `Esc` 关闭并继续正常输入；不影响 viewport 滚动
9. **选择器互斥：** `pickerVisible` 仅在 `overlay == overlayNone` 且 `running == false` 时可激活
10. **全量列表一次性扫描：** `search_file` 在激活时调用一次，后续过滤纯内存操作，不产生额外 I/O
11. **大小限制：** 单文件 ≤ 32KB，总展开 ≤ 128KB，超出截断 + warning
12. **去重：** 同一路径多次引用仅展开一次
13. **@ 有效性：** 仅行首或空格后的 `@` 触发解析，避免误匹配
14. **`search_file` 输出格式：** `search_file` 仅返回文件路径（不含目录），文件选择器从文件路径中提取目录结构

---

## 测试计划

### 解析器测试

1. **TestParseNoRef** — 无 `@` 的输入返回空 refs
2. **TestParseSingleFile** — `@auth/login.go` 正确解析为 KindFile
3. **TestParseSingleFolder** — `@pkg/auth` 正确解析为 KindFolder
4. **TestParseFileWithDotSlash** — `@./auth/login.go` 正确规范化路径
5. **TestParseAbsolutePath** — `@/home/user/project/a.go` 绝对路径不拼接 cwd
6. **TestParseRelativePath** — `@pkg/auth/login.go` 相对路径拼接 cwd
7. **TestParseMultipleRefs** — `@pkg/auth @pkg/context/context.go` 解析出两个 ref
8. **TestParseMixedFileAndFolder** — `@pkg/auth/login.go @pkg/llm` 分别解析为 KindFile 和 KindFolder
9. **TestParseIgnoreEscaped** — `\@file.go` 被转义的 @ 不解析
10. **TestParsePathWithSpaces** — `@path/to/my file.go` 空格正确处理
11. **TestParsePathNotExist** — 不存在的路径 → KindFile（展开时再报错）

### 展开器测试

12. **TestExpandFileSuccess** — 展开一个存在的文件，返回带行号的代码内容
13. **TestExpandFolderSuccess** — 展开一个存在的目录，返回树形列表
14. **TestExpandFileNotFound** — 不存在的文件 → ResolvedRef.Error != ""
15. **TestExpandFolderNotFound** — 不存在的目录 → ResolvedRef.Error != ""
16. **TestExpandMultipleRefs** — 展开多个文件和目录，各自独立
17. **TestExpandBinaryFile** — 二进制文件 → 展开失败
18. **TestExpandPermissionDenied** — 权限拒绝 → 展开失败

### 替换器测试

19. **TestReplaceSingleFileRef** — 单个 @file 被替换为 @@ 围栏代码块
20. **TestReplaceSingleFolderRef** — 单个 @folder 被替换为 @@ 围栏目录树
21. **TestReplaceMixedRefs** — 文件和目录混合引用合并为一个上下文块
22. **TestReplacePreservesUserText** — 用户指令文本在替换后保留
23. **TestReplaceFailedRef** — 展开失败的 ref 保留原文 + 错误标记
24. **TestReplaceNoRefPassthrough** — 无 @ref 的输入直接透传

### 文件选择器测试

25. **TestPickerActivateOnAt** — 输入 `@` 后 `shouldActivatePicker` 返回 true
26. **TestPickerNoActivateOnEscapedAt** — `\@` 不触发选择器
27. **TestPickerNoActivateMidWord** — `foo@bar` 不触发选择器
28. **TestPickerNoActivateAfterSpace** — `@auth/login.go `（@ref 后已有空格）不触发
29. **TestPickerExtractFilter** — `@auth` 提取 filter 为 `auth`
30. **TestPickerFuzzyFilterPrefix** — 前缀匹配排在首位
31. **TestPickerFuzzyFilterSubstring** — 子串匹配排在前缀之后
32. **TestPickerCommitSelection** — Enter 确认后 textinput 值正确替换
33. **TestPickerEscCancel** — Esc 关闭选择器，输入框内容不变
34. **TestPickerUpDownNavigate** — ↑↓ 正确移动高亮并循环
35. **TestPickerItemsSorted** — 目录在前，文件在后，各按字母序

### 端到端测试

36. **TestE2EExpandAndSend** — 模拟完整流程：用户输入含 @file 和 @folder → 展开 → PrepareRun → 消息到达 LLM
37. **TestE2ENoRefPassthrough** — 无 @ref 的输入直接透传，无修改

### Mock 组件

- `mockRegistry` — 可编程控制 `Execute` 返回值，模拟 read_file、ls、search_file 成功/失败
- `mockGuard` — 可编程控制 `Check` 返回值，模拟 allow/deny
- 所有测试使用临时目录（`t.TempDir()`）提供真实文件和子目录

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/reference/reference.go` | Ref / ResolvedRef 类型 + 解析器 + 替换逻辑 |
| 新增 | `pkg/reference/expander.go` | Expander 实现（调用 tool.Registry 展开） |
| 新增 | `pkg/reference/reference_test.go` | 解析器 + 展开器 + 替换器 + 端到端测试 |
| 新增 | `specs/reference-context.md` | 本规格书 |
| 修改 | `cmd/waveloom/tui.go` | `doTurn()` 前调用 `Expander.Expand()`；新增文件选择器状态字段 + `handlePickerKey` + 过滤器同步 + `shouldActivatePicker` / `extractFilterAfterAt` / `commitPickerSelection`；View() 叠加选择器渲染 |
| 修改 | `cmd/waveloom/tui_renderer.go` | User 段落 `@ref` 高亮渲染；新增 `renderFilePicker` 渲染函数 |
| 修改 | `cmd/waveloom/tui_styles.go` | 新增文件选择器覆盖层样式常量（如有需要） |
| 修改 | `cmd/waveloom/main.go` | 创建 `Expander` 实例并注入 TUI / Runner |
| 修改 | `cmd/waveloom/runner.go` | `runOneShot` 中接入 `Expander` |

---

## 验收流程

按 AGENTS.md 的 Wave 流程规范：

1. **TDD 开发**：Red → Green → Refactor 循环，先写测试、再写实现，目标 100% 测试覆盖率
2. **组件测试**：启动 cold agent 执行测试计划中全部 37 个用例
3. **组件验收**：启动 cold agent 进行 review，对照本 spec 检查实现是否完备
4. **验收标准**：
   - 所有单元测试通过，覆盖率 ≥ 90%（parity_test 生成的 settings 测试用例可能覆盖不到）
   - `pkg/reference` 包不依赖 LLM、Agent Loop、TUI
   - 文件选择器非阻断 — viewport 可正常滚动
   - `@` 引用展开结果作为 user message 一部分进入 `PrepareRun`
   - 解析器正则不误匹配 `email@example.com`
   - 展开超 128KB 时截断 + warning
   - 文件选择器在 Enter 发送 / Esc 关闭 / doReset 时正确清理状态

---

## 后续扩展（Wave 8+）

### Wave 8: `@file:N-M` 行范围引用

```
@auth/login.go:42-58          → 读取指定行范围
@auth/login.go:42              → 从 42 行到末尾
```

在 `Ref` 结构体中新增 `LineStart` / `LineEnd` 字段，`read_file` 调用时传入 `offset` / `limit`。

### Wave 9: `@symbol` 符号引用

```
@LoginManager.Authenticate
→ grep "func.*Authenticate" → 定位定义行
→ read_file 从定义行开始截取函数体
→ 注入函数签名 + 文档注释 + 前 20 行实现
```

### Wave 10: `@git` Git 上下文

```
@HEAD~1          → git diff HEAD~1
@staged          → git diff --staged
@main..feature   → git log --oneline + git diff
```

### 文件选择器增强

- 选择器预览窗格：高亮项右侧显示文件前几行或目录子项数
- 多选：`Tab` 标记多个文件，`Enter` 批量回填
- 引用缓存：对同一文件路径的多次引用，缓存 read_file/ls 结果（按修改时间校验新鲜度）
