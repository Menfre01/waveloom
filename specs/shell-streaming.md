# Shell 流式输出 规格书

> 状态：待实现 | 优先级：中 | 依赖：# Bug Fix — Esc 中断进程组

## 问题

当前 shell 工具的输出是**批量返回**的——命令执行完全结束后，`Execute()` 一次性返回全部 stdout/stderr。对于长时间运行的命令（如 `go build`、`npm install`），用户长时间看不到任何反馈，不知道命令在正常运行还是卡死，也无法提前判断是否需要中断。

## 目标

Shell 工具支持**逐行流式输出**，每一行产生时立即推送到 TUI 对应段落的输出区，用户可实时看到命令的执行进度。

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 流式接口 | `StreamingTool` 可选接口，不强制所有工具实现 | 只有 shell 需要流式，read_file/grep 等瞬时完成无需 |
| 推送粒度 | 逐行（`\n` 分割） | 对 TUI 渲染友好，避免逐字符刷新导致性能问题 |
| 进度事件 | 新增 `ToolCallProgress` 事件类型 | 与 `ToolCallStart` / `ToolCallResult` 形成完整生命周期 |
| 并发安全 | 进度推送走 channel，由 TUI 串行消费 | 避免 goroutine 直接操作 Bubble Tea Model |
| 缓冲区 | `bufio.Scanner` + 自定义 split，单行超 100KB 截断 | 防止单行 JSON 撑爆内存 |
| 退出处理 | Context 取消 → 杀进程组 → 最后 flush 一次剩余输出 | 即使被 Esc 中断，已产生的输出也能看到 |

## 组件边界

### 新增接口

```go
// StreamingTool 是可选接口。实现此接口的工具会在 Execute 之外获得流式执行能力。
type StreamingTool interface {
    // ExecuteStreaming 执行工具，通过 onProgress 回调实时推送输出。
    // 返回值同 Execute：最终结果 + error。
    ExecuteStreaming(ctx context.Context, params json.RawMessage, onProgress func(line string)) (*ToolResult, error)
}
```

### Shell 实现

`Shell.ExecuteStreaming` — 逐行读取 stdout/stderr，每行调用 `onProgress(line)`，命令结束后返回完整的 `ToolResult`。

### Agent Loop 变更

`executeToolCalls` 在串行工具执行前检测 `StreamingTool` 接口：

```
if st, ok := t.(tool.StreamingTool); ok {
    result, err := st.ExecuteStreaming(execCtx, rawArgs, func(line string) {
        sendEvent(ctx, ch, ToolCallProgress{
            Turn:         state.TurnCount,
            ToolCallID:   tc.ID,
            ToolCallName: tc.Name,
            Line:         line,
        })
    })
}
```

### TUI 变更

`handleTurnEvent` 新增 `ToolCallProgress` 分支：找到对应 `toolParagraph`，追加 `line` 到输出缓冲区，触发重渲染。

### 事件类型

```go
// ToolCallProgress 工具执行过程中的流式输出行。
type ToolCallProgress struct {
    Turn         int
    ToolCallID   string
    ToolCallName string
    Line         string // 单行输出（不含 \n）
}
```

### 不纳入本组件

- 不改变并发工具的执行模型（shell 本身就是串行工具）
- 不改变 ToolResult 格式（最终结果仍走 ToolCallResult）
- 不涉及权限检查变更

## 接口契约

| 接口 | 签名 | 说明 |
|------|------|------|
| `StreamingTool` | `ExecuteStreaming(ctx, params, onProgress) (*ToolResult, error)` | 流式执行，每行回调 |
| `ToolCallProgress` | `struct { Turn, ToolCallID, ToolCallName, Line }` | 进度事件 |

## 数据流

```
Shell.ExecuteStreaming
    │ 逐行读取 stdout/stderr
    ├─→ onProgress("go: downloading github.com/...")
    │       ↓
    │   sendEvent(ch, ToolCallProgress{...})
    │       ↓
    │   handleTurnEvent → 追加到段落输出 → TUI 刷新
    │
    ├─→ onProgress("go: extracting github.com/...")
    │       ↓
    │   ...
    │
    └─→ 命令结束 → return ToolResult
            ↓
        sendEvent(ch, ToolCallResult{...})
```

## 风险

| 风险 | 缓解 |
|------|------|
| 高频刷新导致 TUI 卡顿 | `time.Ticker` 限流，最多 60fps；或 100ms 合并批量推送 |
| 超大输出撑爆内存 | 段落输出上限 100KB（已有 `maxToolResultBytes`），超限截断 |
| 竞态：goroutine 写 channel 时 TUI 已退出 | `sendEvent` 已有 `select { case ch <- ...; case <-ctx.Done(): }` 保护 |
| 流式输出时按 Esc，最终结果异常 | `ExecuteStreaming` 内部处理 context 取消，返回截断结果 + `ErrorClassRecoverable` |

## 文件清单

| 文件 | 动作 | 说明 |
|------|------|------|
| `pkg/tool/tool.go` | 修改 | 新增 `StreamingTool` 接口 |
| `pkg/tool/shell.go` | 修改 | 实现 `ExecuteStreaming` |
| `pkg/agentloop/types.go` | 修改 | 新增 `ToolCallProgress` 事件 |
| `pkg/agentloop/execute.go` | 修改 | 检测 `StreamingTool`，推送进度 |
| `cmd/waveloom/tui.go` | 修改 | 处理 `ToolCallProgress` 事件 |
| `cmd/waveloom/tui_renderer.go` | 修改 | 渲染时显示流式输出 |

## 测试要点

| 场景 | 预期 |
|------|------|
| `sleep 2 && echo done` 流式执行 | 无输出 2 秒后 → Progress: "done" → Result: "Command succeeded" |
| `for i in 1 2 3; do echo $i; sleep 0.1; done` | 依次 Progress: "1", "2", "3" → Result |
| 流式执行中按 Esc | Context 取消 → 杀死进程组 → flush 剩余输出 → Result: "Command interrupted" |
| 输出单行 >100KB | 截断为 100KB，追加 `... [truncated]` |
| 串行工具：shell + grep 依次执行 | shell 流式输出 → ToolCallResult → grep 开始，不要交叉 |
