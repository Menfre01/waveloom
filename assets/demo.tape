# Waveloom Demo
# 展示 Thought 面板 → 工具调用 → 流式输出 → 手动/对话式 Plan Mode 的完整交互流程

Output assets/demo.gif

Set FontSize 15
Set Width 800
Set Height 700
Set Theme "Atom"
Set Padding 12
Set Framerate 12
Set PlaybackSpeed 1

# 启动交互模式
Type "./bin/waveloom"
Enter

# 等待 TUI 启动
Sleep 3s

# ── 第一问：触发 reasoning，展示 thought 面板 ──
Type "用一句话解释 pkg/compaction/compactor.go 中四级水位线的工作机制"
Enter

# 等待 thought + 回答流式输出
Sleep 25s

# ── 第二问：触发 shell 工具调用 ──
Type "用 shell 执行 head -80 pkg/llm/client.go，展示 NewClient 实现"
Enter

# 等待 permission 弹框出现，按 Enter 确认
Sleep 3s
Enter

# 等待工具调用 + 流式输出
Sleep 30s

# ── 手动进入 Plan Mode（Shift+Tab）──
Shift+Tab
Sleep 1s

# 在 Plan 模式输入框中随便输入点什么（可选），演示 plan placeholder
# 然后 Shift+Tab 退出 Plan 模式
Shift+Tab
Sleep 1s

# ── 第三问：对话触发 Plan Mode 先规划后执行 ──
Type "帮我规划并为 README.md 底部添加一个「Star History」章节，引用 star-history.com 的图表链接"
Enter

# 等待 LLM 进入 Plan Mode（弹出确认 Overlay），确认进入
Sleep 5s
Enter

# 等待 LLM 写入 plan 并提交审批，弹出审批 Overlay，批准
Sleep 15s
Enter

# 等待 LLM 在 Normal 模式执行 plan 内容（edit_file README.md）
Sleep 25s

# ── 第四问：收尾 ──
Type "一句话总结 Plan Mode 给开发流程带来的最大变化"
Enter

Sleep 15s

# 退出
Ctrl+C
Sleep 1s
