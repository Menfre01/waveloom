# Waveloom Demo (English)
# Thought → Tool call → Streaming → Manual/Conversational Plan Mode

Output assets/demo.en.gif

Set FontSize 15
Set Width 800
Set Height 700
Set Theme "Atom"
Set Padding 12
Set Framerate 12
Set PlaybackSpeed 1

# Start in English locale
Type "./bin/waveloom --locale en-US"
Enter

# Wait for TUI to start
Sleep 3s

# ── Q1: Trigger reasoning, show thought panel ──
Type "Explain in one sentence how the four-tier watermark compaction works in pkg/compaction/compactor.go"
Enter

# Wait for thought + streaming response
Sleep 25s

# ── Q2: Trigger shell tool call ──
Type "Use shell to run head -80 pkg/llm/client.go, show me the NewClient implementation"
Enter

# Wait for permission dialog, press Enter to confirm
Sleep 3s
Enter

# Wait for tool call + streaming output
Sleep 30s

# ── Manually enter Plan Mode (Shift+Tab) ──
Shift+Tab
Sleep 1s

# Shift+Tab to exit Plan Mode
Shift+Tab
Sleep 1s

# ── Q3: Conversational Plan Mode trigger ──
Type "Plan and add a 'Star History' section to the bottom of README.md, referencing a star-history.com chart link"
Enter

# Wait for LLM to enter Plan Mode (confirmation overlay), confirm
Sleep 5s
Enter

# Wait for LLM to write plan and submit for approval, approve
Sleep 15s
Enter

# Wait for LLM to execute plan in Normal mode (edit_file README.md)
Sleep 25s

# ── Q4: Wrap up ──
Type "In one sentence, what's the biggest change Plan Mode brings to the development workflow"
Enter

Sleep 15s

# Quit
Ctrl+C
Sleep 1s
