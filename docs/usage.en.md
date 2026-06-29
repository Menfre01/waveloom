<p align="center">
  <a href="./usage.md">简体中文</a>
  &nbsp;·&nbsp;
  <strong>English</strong>
</p>

---

# Usage

## Interactive Mode

```sh
waveloom
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
waveloom "explain the design of pkg/llm/client.go"
waveloom --model deepseek-v4-pro "write unit tests for UserService"
echo "review the code under pkg/llm/" | waveloom
```

## Session Management

```sh
waveloom ls                     # List recent sessions
waveloom --continue             # Resume the most recent session
waveloom --resume <session-id>  # Resume a specific session
```

## @ File References

Type `@` in the input to open a fuzzy file picker (prefix > substring matching). `Tab` enters subdirectories. Selected file contents are automatically injected into the message context.

```
help me optimize the error handling in @pkg/auth/login.go
```

### AGENTS.md Auto-loading

On startup, Waveloom discovers and loads `AGENTS.md` (search path: `~/.waveloom/AGENTS.md` → project root where `.git` lives → CWD), concatenating them from outer to inner as the first user message. The agent automatically follows project conventions, coding standards, and workflows defined therein.

### @ Expansion Inside AGENTS.md

`AGENTS.md` files also support `@` reference syntax, useful for splitting large convention docs into multiple files:

```
# AGENTS.md
@docs/coding-style.md
@docs/release-process.md
```

Waveloom expands `@` references within loaded AGENTS.md files. Multiple refs are expanded in order, with deduplication by path.
