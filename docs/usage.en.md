<p align="center">
  <a href="./usage.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>English</strong>
</p>

---

# Usage

## Interactive Mode

```sh
wvl
```

Once in the TUI, type like a chat and press Enter to send. The agent autonomously invokes tools to read files, search code, edit, and run tests.

<p align="center">
  <img src="../assets/tui.png" alt="Waveloom screenshot" width="720"/>
</p>

The prefix character at the beginning of each line tells you **who is speaking**:

| Prefix | Role | Meaning |
|--------|------|---------|
| `›` | You | Your message, in blue |
| `·` / spinner | Assistant | AI reply, in green, Markdown rendered |
| `·` / spinner | Thought | AI's reasoning, in gray, collapsed to one line when done (`Tab` to focus + `Enter` to expand) |
| `•` / spinner | Tool | AI's actions (read, write, run), green = success / red = failure |

**Keyboard shortcuts**:

| Key | Action |
|-----|--------|
| `Enter` | Send message; type `exit` and Enter to quit |
| `Esc` | Interrupt running agent |
| `Esc+Esc` | Clear the input |
| `↑` `↓` / `PgUp` `PgDn` | Scroll conversation history |
| `Ctrl+E` / `End` | Jump to bottom |
| `Tab` | Focus next interactive paragraph (thought / tool output) |
| `Shift+Tab` | Focus previous interactive paragraph |
| `Enter` | Expand/collapse the currently focused paragraph |
| `Ctrl+G` | Toggle theme (dark / light / auto) |
| `Ctrl+C` | Quit |
| `Shift + mouse drag` | Select text in terminal |

The **footer status bar** shows: current model, context usage (progress bar), cache hit rate, loop count, balance.

## One-shot

```sh
wvl "explain the design of pkg/llm/client.go"
wvl --model deepseek-v4-pro "write unit tests for UserService"
echo "review the code under pkg/llm/" | wvl
```

## Session Management

```sh
wvl ls                     # List recent sessions
wvl --continue             # Resume the most recent session
wvl --resume <session-id>  # Resume a specific session
```

## @ File References

Type `@` in the input to open a fuzzy file picker (prefix > substring matching). `Tab` enters subdirectories. Selected file contents are automatically injected into the message context.

```
help me optimize the error handling in @pkg/auth/login.go
```
