# Waveloom Demo
# 展示 Thought 面板 → 工具调用 → 流式输出 的完整交互流程
# 交互模型：Tab / Shift+Tab 聚焦可交互段落，Enter 展开/折叠

Output assets/demo.gif

Set FontSize 15
Set Width 800
Set Height 700
Set Theme "Atom"
Set Padding 12
Set Framerate 12
Set PlaybackSpeed 1

# 启动交互模式
Type "wvl"
Enter

# 等待 TUI 启动
Sleep 3s

# ── 第一问：触发 reasoning，展示 thought 面板 ──
Type "用一句话解释 pkg/compaction/compactor.go 中四级水位线的工作机制"
Enter

# 等待 thought + 回答流式输出
Sleep 25s

# Tab 聚焦 thought → Enter 展开 thought 面板，展示 reasoning 内容
Tab
Enter
Sleep 2s

# Enter 折叠 thought
Enter
Sleep 1s

# Escape 退出聚焦模式，回到输入框
Escape
Sleep 500ms

# ── 第二问：触发 shell 工具调用 ──
Type "用 shell 执行 head -80 pkg/llm/client.go，展示 NewClient 实现"
Enter

# 等待 permission 弹框出现，按 Enter 确认
Sleep 3s
Enter

# 等待工具调用 + 流式输出
Sleep 30s

# Tab 逐段移至 shell 输出 → Enter 展开工具输出面板
# （具体 Tab 次数取决于中段可交互段落数，按需调整）
Tab
Tab
Enter
Sleep 2s

# Enter 折叠工具输出
Enter
Sleep 1s

# Escape 退出聚焦模式
Escape
Sleep 500ms

# ── 第三问：收尾 ──
Type "一句话总结 waveloom 相比其他 coding agent 最大的不同点"
Enter

Sleep 15s

# 退出
Ctrl+C
Sleep 1s
