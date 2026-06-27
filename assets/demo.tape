# Waveloom Demo
# 展示 Thought 面板 → 工具调用 → 流式输出 的完整交互流程

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
Sleep 20s

# 展开 thought 面板，展示 reasoning 内容
Ctrl+T
Sleep 2s

# 折叠 thought
Ctrl+T
Sleep 1s

# ── 第二问：触发工具调用（read_file）──
Type "读取 pkg/llm/client.go 文件，总结 NewClient 函数的核心逻辑"
Enter

# 等待工具调用 + 流式输出
Sleep 30s

# 展开工具输出面板
Ctrl+O
Sleep 2s

# 折叠工具输出
Ctrl+O
Sleep 1s

# ── 第三问：收尾 ──
Type "一句话总结 waveloom 相比其他 coding agent 最大的不同点"
Enter

Sleep 15s

# 退出
Ctrl+C
Sleep 1s
