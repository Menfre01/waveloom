# Waveloom Demo (English)
# Showcase: Logo+HUD → Parallel Subagent Explore → Todo Panel → Permission Dialog → Plan Mode
# Key highlights: 6-color gradient Logo, parallel Subagent execution, real-time Todo panel, ▌Plan indicator

Output assets/demo.en.gif

Set FontSize 14
Set Width 820
Set Height 700
Set Theme "Atom"
Set Padding 12
Set Framerate 24
Set PlaybackSpeed 1

# ── Startup ──
Type "waveloom --locale en-US"
Enter

# Wait for Logo render + Footer HUD ready
Sleep 2.5s

# ── Act 1: Parallel Subagent Explore + Todo Panel ──
Type "Use 3 Explore agents in parallel to explore pkg/llm, pkg/tool, and pkg/subagent — one agent per package. Summarize each package's responsibilities, core interfaces, and dependencies."
Enter

# Wait for agent to create todos + spawn parallel subagents
Sleep 8s

# Wait for subagent execution + result aggregation + streaming output
Sleep 45s

# ── Act 2: Shell tool call + permission confirmation overlay ──
Type "Use shell to run head -80 pkg/llm/client.go, show me the NewClient implementation"
Enter

# Wait for permission dialog, press Enter to confirm
Sleep 2.5s
Enter

# Wait for tool output + streaming response
Sleep 25s

# ── Act 3: Plan Mode toggle, show ▌Plan indicator ──
Shift+Tab
Sleep 0.8s

Sleep 1.5s

Shift+Tab
Sleep 0.8s

# ── Act 4: Wrap up ──
Type "Summarize what problems the subagent system and todo system each solve in Waveloom"
Enter

Sleep 12s

Ctrl+C
Sleep 0.8s
