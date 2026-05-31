# AGENTS.md 持久记忆 spec

## 定位

将工作区中 `AGENTS.md` 文件作为持久记忆，在会话启动时发现、加载并注入到 agent 上下文，实现“项目约定自动进入模型上下文”。

**核心价值：** 用户只需在项目根（或任意子目录）放置 `AGENTS.md` 描述项目约定、编码规范、架构决策等，agent 在每次对话中自动遵循这些约定。

## 参考实现：Codex

本设计对标 Codex `codex-rs/core/src/agents_md.rs` 的实现，仅在标注粒度上做一处增强（见[与 Codex 的关键差异](#与-codex-的关键差异)）。

### Codex 实现要点

| 维度 | Codex 做法 |
|------|-----------|
| **发现策略** | 从 CWD 向上遍历到项目根（通过 `.git` root marker），收集路径上所有 `AGENTS.md`（根→叶序） |
| **文件名候选** | `AGENTS.override.md` > `AGENTS.md` > 可配置 fallback 列表 |
| **全局记忆** | `~/.codex/AGENTS.md` + `~/.codex/AGENTS.override.md` |
| **拼接方式** | 各文件内容以 `\n\n` 连接 |
| **大小限制** | `project_doc_max_bytes` 配置项 |
| **注入位置** | 作为 `ContextualUserFragment` 注入 user 消息区域 |
| **注入格式** | `# AGENTS.md instructions for {cwd}\n\n<INSTRUCTIONS>\n{全文}\n</INSTRUCTIONS>` |
| **加载时机** | 会话初始化时一次性读盘，存入 `TurnContext.user_instructions`，后续 turn 复用缓存值 |
| **变更处理** | **不感知**。文件改动后需新开会话才生效 |
| **Feature flag** | `MemoryTool` 控制是否启用 |

### 与 Codex 的关键差异

| 维度 | Codex | Waveloom | 原因 |
|------|-------|----------|------|
| **注入位置** | User 消息（`ContextualUserFragment`） | User 消息（`PrepareRun` 中插入） | 一致 |
| **Root marker** | 多层 config 合并的 `project_root_markers` | 固定 `.git` | Waveloom 无 config layer 系统 |
| **全局记忆** | `~/.codex/AGENTS.md` | `~/.waveloom/AGENTS.md` | 路径命名约定 |
| **Fallback 文件名** | 可配置 | 不支持 | 仅 `AGENTS.md`，保持简单 |
| **Override** | `AGENTS.override.md` | 暂不支持 | YAGNI |
| **每块标注** | 仅标注 CWD（整个拼接体一个标签） | 标注每个文件的来源路径 | **唯一增强**：使模型能区分不同目录的约定，实现真正的渐进式纰漏 |

---

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 注入位置 | User 消息 | 对标 Codex；AGENTS.md 是“用户提供的上下文材料”，非“系统级指令”；保持 system prompt 短小稳定（前缀缓存友好） |
| 加载时机 | 会话初始化一次性读盘 | 对标 Codex；减少 I/O；内容在会话期间不变 |
| 发现策略 | 从 CWD 向上遍历到 Git 根，收集路径上所有 AGENTS.md | 对标 Codex；支持子目录 scope 限定 |
| 文件名 | 仅 `AGENTS.md`，无 fallback | 对标 Codex 默认文件名；去掉 CLAUDE.md 保持简单 |
| 全局记忆 | `~/.waveloom/AGENTS.md` | 对标 Codex `~/.codex/AGENTS.md` |
| 大小限制 | 硬编码 64KB，超出截断 + warning | 对标 Codex `project_doc_max_bytes` |
| 拼接顺序 | 全局 → 项目根 → ... → CWD（由外到内） | 对标 Codex；内层约定更具体 |
| 拼接格式 | 每块以来源路径标注，`\n\n` 连接，外围 `<INSTRUCTIONS>` 围栏 | 对标 Codex 围栏格式，增加每块路径标注 |
| 变更处理 | **不感知**。文件改动 → 用户按 Ctrl+L 重置会话才生效 | 对标 Codex；避免 diff 心智合并风险、避免中部注意力衰减 |
| 配置开关 | 无需，默认启用 | 有文件就加载，没有就跳过 |
| CWD 变化 | 不适用。Waveloom 无 cd 工具，CWD 固定 | 加载即终态，零动态联动 |

---

## 变更处理的设计理由

Codex 在 `updates.rs` 中处理了环境变化、权限变化、collaboration mode 变化，唯独不给 AGENTS.md 做动态更新。这不是疏漏——是刻意选择。理由如下：

**1. Lost in the Middle**

Transformer 模型对 context 的注意力呈 U 型分布：头部和尾部高，中部低。AGENTS.md 全文放在 context 头部（第一条 user 消息）→ 始终在高注意力区。如果后续注入 diff 更新，diff 落在 context 中部（已完成多轮对话后）→ 模型几乎 attend 不到。

**2. 心智合并歧义**

```
messages[1]:  原始 AGENTS.md 全文
messages[20]: diff1（修改约定 A）
messages[35]: diff2（修改约定 B）
```

模型无法正确从 `原始 + diff1 + diff2` 重建“当前有效约定”。这是人类开发者都需要 git blame 才能理清的操作。

**3. 重复失敏**

如果每个 turn 都检查变化 → 每次无变化时重复注入相同内容 → 模型将标注为“重复噪声”并降低注意力。

Codex 的权衡：**宁愿约定过时（用户重启会话），也不愿约定解析出错（模型心智合并失败）。**

Waveloom 在此问题上比 Codex 更简单：没有 cd 工具，CWD 在会话期间固定，AGENTS.md 加载后天然不存在"新目录发现"的触发点。唯一变更来源是用户手动编辑文件 → Ctrl+L 重载覆盖。

---

## 组件边界

### 新增包：`pkg/memory`

```
pkg/memory/
  memory.go        // Loader 类型 + 加载入口
  memory_test.go   // 单元测试
```

不依赖 LLM、Agent Loop、TUI。仅依赖标准库。

### 输入
- `cwd string` — 当前工作目录
- `homeDir string` — 用户主目录

### 输出
- `text string` — 拼接带标注的 AGENTS.md 内容（可能为空）
- `warnings []string` — 非致命警告（文件不存在不算 warning）
- `err error` — 仅在系统级 I/O 错误时非 nil

---

## 接口定义

```go
package memory

// Loader 负责发现和加载 AGENTS.md 文件。
type Loader struct {
    CWD     string // 当前工作目录（起点）
    HomeDir string // 用户主目录（用于 ~/.waveloom/AGENTS.md）
}

// NewLoader 创建一个新的 Loader。
func NewLoader(cwd, homeDir string) *Loader

// Load 发现并加载所有 AGENTS.md 文件，返回带来源标注的拼接文本。
//
// 发现顺序（拼接按此顺序）：
//   1. ~/.waveloom/AGENTS.md（全局记忆，如存在）
//   2. 从 Git 根到 CWD 路径上各级目录中的 AGENTS.md（根→叶）
//
// 拼接格式：
//   # AGENTS.md instructions for {cwd}
//
//   <INSTRUCTIONS>
//
//   ## {path}
//   {content}
//
//   ## {path}
//   {content}
//   </INSTRUCTIONS>
//
// 总大小超过 64KB 时截断后续文件，在尾部追加截断标记。
func (l *Loader) Load() (text string, warnings []string, err error)
```

---

## 核心算法

### 1. 项目根发现

```
输入: cwd
输出: projectRoot (绝对路径) 或 nil

1. 将 cwd 规范化为绝对路径
2. 从 cwd 向上遍历父目录:
   a. 检查当前目录中是否存在 ".git"（文件或目录）
   b. 存在 → projectRoot = 当前目录，break
   c. 不存在 → 继续父目录
   d. 到达文件系统根 → break（projectRoot = nil）
3. 返回 projectRoot
```

### 2. 单目录 AGENTS.md 发现

```
输入: dir string
输出: path string（找到的文件路径，空 = 未找到）

1. path = filepath.Join(dir, "AGENTS.md")
2. os.Stat → 存在且为常规文件 → 返回 path，否则返回 ""
```

### 3. 完整 Load 流程

```
1. parts = []           // 每项 {path, content}
2. warnings = []
3. remainingBytes = 64KB

4. ── 全局记忆 ──
   globalPath = filepath.Join(homeDir, ".waveloom", "AGENTS.md")
   如果 globalPath 存在且为常规文件:
     content, warn = readFile(globalPath, remainingBytes)
     if content != "":
       parts.append({path: "~/.waveloom/AGENTS.md", content})
       remainingBytes -= len(content)
     warnings.extend(warn)

5. ── 层级发现 ──
   projectRoot = findProjectRoot(cwd)

   if projectRoot != nil:
     dirs = [projectRoot, ..., cwd]  // 从根到叶
   else:
     dirs = [cwd]

6. ── 加载各层 ──
   对 dirs 中每个 dir:
     if remainingBytes <= 0: break
     filePath = discoverAgentsMd(dir)
     if filePath != "":
       content, warn = readFile(filePath, remainingBytes)
       if content != "":
         parts.append({path: filePath, content})
         remainingBytes -= len(content)
       warnings.extend(warn)

7. ── 拼接 ──
   blocks = []
   对 parts 中每个 {path, content}:
     blocks.append("## " + path + "\n" + content)

   body = join(blocks, "\n\n")

   text = "# AGENTS.md instructions for " + cwd + "\n\n<INSTRUCTIONS>\n\n" + body + "\n</INSTRUCTIONS>"

8. 如果 remainingBytes == 0 且还有未读取的目录:
     text += "\n\n[AGENTS.md 内容被截断：已达到大小上限]"
     warnings.append("AGENTS.md content truncated: max bytes limit reached")

9. 返回 text, warnings, nil
```

---

## 注入格式示例

当 CWD 为 `/Users/menfre/project`，Git 根为 `/Users/menfre/project`，存在全局记忆时：

```
# AGENTS.md instructions for /Users/menfre/project

<INSTRUCTIONS>

## ~/.waveloom/AGENTS.md
... 全局记忆内容 ...

## /Users/menfre/project/AGENTS.md
... 项目根约定 ...

</INSTRUCTIONS>
```

当 CWD 为子目录 `/Users/menfre/project/cmd/waveloom` 时：

```
# AGENTS.md instructions for /Users/menfre/project/cmd/waveloom

<INSTRUCTIONS>

## ~/.waveloom/AGENTS.md
... 全局记忆 ...

## /Users/menfre/project/AGENTS.md
... 项目根约定 ...

## /Users/menfre/project/cmd/waveloom/AGENTS.md
... 子模块约定 ...

</INSTRUCTIONS>
```

### 为什么增加每块路径标注（Codex 没有）

Codex 将所有文件以 `\n\n` 连接后统一包裹在一个 `<INSTRUCTIONS>` 块中，不标注各文件来源：

```
# AGENTS.md instructions for /project

<INSTRUCTIONS>
[文件1内容]

[文件2内容]
</INSTRUCTIONS>
```

模型无法区分哪段来自哪个目录。如果 `cmd/waveloom/AGENTS.md` 说“必须使用 table-driven tests”，而 `pkg/llm/AGENTS.md` 说“测试只需覆盖 happy path”——模型不知道每条约定适用于什么范围。

Waveloom 增加 `## {path}` 前缀，成本极低（每个路径一行），收益明显：路径本身就是作用域标签。

---

## 集成点

### `cmd/waveloom/main.go`

```go
// 8. 加载 AGENTS.md 持久记忆（对标 Codex agents_md.rs）
var agentsMdText string
if homeDir, err := os.UserHomeDir(); err == nil {
    loader := memory.NewLoader(cwd, homeDir)
    text, warnings, err := loader.Load()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: 加载 AGENTS.md 失败: %v\n", err)
    }
    for _, w := range warnings {
        fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
    }
    agentsMdText = text
}

// 9. 创建 Context Manager
systemPrompt := cfg.SystemPrompt
if systemPrompt == "" {
    systemPrompt = buildSystemPrompt(cwd)
}
ctxMgr := ctxpkg.New(systemPrompt)

// 10. 将 AGENTS.md 作为 user 消息注入（对标 Codex UserInstructions fragment）
if agentsMdText != "" {
    ctxMgr.InjectUserInstructions(agentsMdText)
}
```

### `pkg/context/context.go` 修改

新增 `InjectUserInstructions` 方法——在 system prompt 之后、用户实际输入之前插入一条 user 消息：

```go
// InjectUserInstructions 注入 AGENTS.md 内容作为第一条 user 消息。
// 对标 Codex 的 UserInstructions contextual user fragment。
// 仅在 messages 仅含 system prompt 时调用（会话初始化阶段）。
func (cm *ContextManager) InjectUserInstructions(text string) {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    // 在 system prompt (messages[0]) 之后插入
    if len(cm.messages) > 0 && cm.messages[0].Role == llm.RoleSystem {
        cm.messages = append(cm.messages[:1], append(
            []llm.Message{{Role: llm.RoleUser, Content: text}},
            cm.messages[1:]...,
        )...)
    }
}
```

### Context 消息结构

```
messages[0]: system  = hardcoded prompt + workspace info（前缀锚点）
messages[1]: user    = # AGENTS.md instructions for /project
                       <INSTRUCTIONS>
                       ## ~/.waveloom/AGENTS.md
                       ...
                       ## /project/AGENTS.md
                       ...
                       ## /project/cmd/waveloom/AGENTS.md
                       ...
                       </INSTRUCTIONS>
messages[2]: user    = 用户实际输入: "帮我重构 tui.go"
messages[3]: assistant = ...
...
```

### `doReset` (Ctrl+L) 修改

重置时**重新加载 AGENTS.md**，然后重新注入：

```go
func (m *model) doReset() {
    // ... 现有清理逻辑 ...

    // 重新加载 AGENTS.md
    if homeDir, err := os.UserHomeDir(); err == nil {
        loader := memory.NewLoader(m.cwd, homeDir)
        text, _, _ := loader.Load()
        if text != "" {
            m.cm.InjectUserInstructions(text)
        }
    }
    // ...
}
```

### 注入位置可视化

```
┌─ messages[0] (system) ──────────────────────────────┐
│ You are Waveloom v0.1.0...                          │
│ ## Personality                                      │
│ ...                                                 │
│ ## Workspace                                        │
│ Current working directory: /Users/menfre/project    │
└─────────────────────────────────────────────────────┘

┌─ messages[1] (user) ────────────────────────────────┐
│ # AGENTS.md instructions for /Users/menfre/project  │
│                                                     │
│ <INSTRUCTIONS>                                      │
│                                                     │
│ ## ~/.waveloom/AGENTS.md                            │
│ ... 全局记忆 ...                                     │
│                                                     │
│ ## /Users/menfre/project/AGENTS.md                  │
│ ... 项目根约定 ...                                   │
│                                                     │
│ ## /Users/menfre/project/cmd/waveloom/AGENTS.md     │
│ ... 子模块约定 ...                                   │
│ </INSTRUCTIONS>                                     │
└─────────────────────────────────────────────────────┘

┌─ messages[2] (user) ────────────────────────────────┐
│ 帮我重构 tui.go                                      │
└─────────────────────────────────────────────────────┘
```

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/memory/memory.go` | Loader 类型、发现逻辑、加载逻辑 |
| 新增 | `pkg/memory/memory_test.go` | 单元测试 |
| 新增 | `specs/agents-md-memory.md` | 本规格书 |
| 修改 | `pkg/context/context.go` | 新增 `InjectUserInstructions` 方法 |
| 修改 | `cmd/waveloom/main.go` | 会话启动时加载并注入 AGENTS.md |
| 修改 | `cmd/waveloom/tui.go` | `doReset` 中重新加载 AGENTS.md |

---

## 测试计划

### 单元测试 (`pkg/memory/memory_test.go`)

1. **TestFindProjectRoot_WithGit** — 临时目录包含 `.git/` → 找到 project root
2. **TestFindProjectRoot_NoGit** — 无 `.git` → 返回 nil
3. **TestFindProjectRoot_Subdirectory** — CWD 在项目子目录 → 向上找到含 `.git` 的目录
4. **TestDiscoverAgentsMd_Found** — 目录中存在 `AGENTS.md` → 返回该路径
5. **TestDiscoverAgentsMd_NotFound** — 不存在 `AGENTS.md` → 返回空
6. **TestLoad_GlobalMemory** — `~/.waveloom/AGENTS.md` 存在 → 出现在拼接结果首部
7. **TestLoad_Hierarchical** — Git 根 + 子目录各有 AGENTS.md → 按 root→leaf 序拼接，各有 `## {path}` 标注
8. **TestLoad_GlobalPlusHierarchical** — 全局 + 层级 → 全局在最前
9. **TestLoad_NoFiles** — 无任何 AGENTS.md → 返回空 text，无 warning
10. **TestLoad_Format** — 验证输出包含 `# AGENTS.md instructions for`、`<INSTRUCTIONS>` / `</INSTRUCTIONS>`、`## {path}` 标注
11. **TestLoad_MaxBytesTruncation** — 超出 64KB → 截断 + warning
12. **TestLoad_InvalidUtf8** — 文件含非法 UTF-8 → 替换 + warning
13. **TestLoad_ReadError** — 文件无权限读取 → warning，跳过该文件继续

### ContextManager 测试

14. **TestInjectUserInstructions_InsertsAfterSystem** — 插入在 system 消息之后、现有消息之前
15. **TestInjectUserInstructions_NoSystem** — messages 无 system 时正确处理

### 集成测试

16. **TestIntegration_AgentsMdInjectedAsUserMessage** — 端到端：验证 system prompt 不含 AGENTS.md，第一条 user 消息包含

---

## 不变量

1. **system prompt 不含 AGENTS.md**：hardcoded prompt + workspace info 保持原样，对标 Codex
2. **AGENTS.md 作为独立 user 消息**：位于 messages[1]，对标 Codex UserInstructions fragment
3. **无文件不报错**：没有任何 AGENTS.md 是正常状态
4. **错误不阻塞**：读取错误只产生 warning
5. **大小有上限**：拼接后总内容 ≤ 64KB
6. **会话期间不变**：对标 Codex，一次加载不复读。Ctrl+L 重置时才重载
7. **顺序确定**：全局 → 根 → 子目录，每块标注来源路径
8. **幂等性**：多次 Load 调用返回相同结果
