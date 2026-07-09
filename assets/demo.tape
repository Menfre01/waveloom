# Waveloom Demo
# 展示: Logo+HUD → 并行 Subagent Explore → Todo 面板 → 权限弹框 → Plan Mode
# 核心卖点: 6 色渐变 Logo、多 Subagent 并行执行、Todo 实时面板、▌Plan 状态标记

Output assets/demo.gif

Set FontSize 14
Set Width 820
Set Height 700
Set Theme "Atom"
Set Padding 12
Set Framerate 24
Set PlaybackSpeed 1

# ── 启动 ──
Type "waveloom"
Enter

# 等待 Logo 渲染 + Footer HUD 就绪
Sleep 2.5s

# ── 第一幕：并行 Subagent Explore + Todo 面板 ──
# 这个 prompt 设计用来同时触发: todo_write（任务拆分）+ 并行 agent Explore（探索多个包）
Type "用 3 个 Explore agent 并行探索 pkg/llm、pkg/tool、pkg/subagent，每个 agent 负责一个包，各自总结职责、核心接口和依赖关系"
Enter

# 等待 agent 创建 todo + 启动并行 subagent（需要足够时间让 todo 面板和 subagent spinner 出现）
Sleep 8s

# 等待 subagent 执行完成 + 结果汇总 + 流式输出（给足时间展示 subagent 完整执行过程）
Sleep 45s

# ── 第二幕：shell 工具调用 + 权限确认弹框 ──
Type "用 shell 执行 head -80 pkg/llm/client.go，展示 NewClient 实现"
Enter

# 等待 permission 弹框出现，Enter 确认
Sleep 2.5s
Enter

# 等待工具输出 + 流式响应
Sleep 25s

# ── 第三幕：Plan Mode 手动切换，展示 ▌Plan 状态标记 ──
Shift+Tab
Sleep 0.8s

Sleep 1.5s

Shift+Tab
Sleep 0.8s

# ── 第四幕：收尾 ──
Type "总结 Waveloom 的 subagent 和 todo 系统各自解决了什么问题"
Enter

Sleep 12s

Ctrl+C
Sleep 0.8s
