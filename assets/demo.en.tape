# Waveloom Demo (English)
# Thought → Tool call → Streaming → Plan Mode
# Upgrade highlights: stable streaming (wrapLineStable), 2-line textarea, smoother overall flow

Output assets/demo.en.gif

Set FontSize 15
Set Width 800
Set Height 700
Set Theme "Atom"
Set Padding 12
Set Framerate 24
Set PlaybackSpeed 1

# Start in English locale
Type "./bin/waveloom --locale en-US"
Enter

# Wait for TUI to start
Sleep 2.5s

# ── Q1: Long input shows 2-line textarea + trigger reasoning, show thought panel ──
Type "Explain in one sentence how the four-tier watermark compaction works in pkg/compaction/compactor.go, including the trigger condition and fallback strategy for each tier"
Enter

# Wait for thought + streaming response (stable, no jitter)
Sleep 20s

# ── Q2: Trigger shell tool call ──
Type "Use shell to run head -80 pkg/llm/client.go, show me the NewClient implementation"
Enter

# Wait for permission dialog, press Enter to confirm
Sleep 2.5s
Enter

# Wait for tool call + streaming output
Sleep 25s

# ── Manually enter Plan Mode (Shift+Tab) ──
Shift+Tab
Sleep 0.8s

# Shift+Tab to exit Plan Mode
Shift+Tab
Sleep 0.8s

# ── Q3: Conversational Plan Mode trigger ──
Type "Plan and add a 'Star History' section to the bottom of README.md, referencing a star-history.com chart link"
Enter

# Wait for LLM to enter Plan Mode (confirmation overlay), confirm
Sleep 4s
Enter

# Wait for LLM to write plan and submit for approval, approve
Sleep 12s
Enter

# Wait for LLM to execute plan in Normal mode (edit_file README.md)
Sleep 20s

# ── Q4: Wrap up ──
Type "In one sentence, what's the biggest change Plan Mode brings to the development workflow"
Enter

Sleep 12s

# Quit
Ctrl+C
Sleep 0.8s
