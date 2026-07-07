# Contributing to Waveloom

Thank you for contributing!

## Prerequisites

- **Go 1.25+**
- **Windows users**: [Git for Windows](https://git-scm.com/downloads/win) is required (Waveloom uses Git Bash for shell command execution) plus `make` (not bundled with Git for Windows — install via `choco install make` or use `go build` / `go test` directly)
- **macOS users**: Xcode Command Line Tools (`xcode-select --install`)

## Quick Start

```sh
git clone git@github.com:Menfre01/waveloom.git
cd waveloom
make build
make test
```

## Development Flow

### Component-based Wave Development

- Tasks are split by the principle of **high cohesion within a single component, low coupling between components**
- Each task advances as an independent Wave, following "component dev → component test → component review → gradual assembly"
- Read the corresponding component spec under `specs/` before starting each Wave

### TDD (Test-Driven Development)

- Red → Green → Refactor cycle
- Write tests first, then implement
- Run `make test` after modifying `pkg/` code to ensure all tests pass

## Project Structure

```
waveloom/
├── cmd/waveloom/          # CLI entry point + TUI
├── pkg/
│   ├── agentloop/         # Think-Act-Observe loop
│   ├── compaction/        # Four-tier watermark context compaction
│   ├── context/           # Cross-turn message history
│   ├── environment/       # Build/runtime toolchain probing
│   ├── llm/               # LLM Client (DeepSeek + OpenAI)
│   ├── mcp/               # MCP client
│   ├── memory/            # AGENTS.md hierarchical loading
│   ├── pathutil/          # Path utilities
│   ├── permission/        # Permission gatekeeper
│   ├── reference/         # @ file reference expansion
│   ├── shellutil/         # Shell utilities
│   ├── skill/             # Skill system
│   ├── slashcommand/      # / command palette
│   ├── subagent/          # Sub-agent delegation
│   ├── task/              # Background task management
│   ├── todo/              # Todo state management
│   └── tool/              # Built-in tool system
├── specs/                 # Component design specs (read before modifying)
├── docs/                  # Documentation
└── Makefile
```

## Coding Standards

- Follow [Effective Go](https://go.dev/doc/effective_go) and Go community conventions
- Use clear, self-documenting names; avoid abbreviations
- Errors propagate cleanly — no raw stack traces to the client
- Read `AGENTS.md` for project-level conventions before making changes

## Common Commands

| Action | Command |
|--------|---------|
| Build | `make build` |
| Install | `make install` |
| Run | `make run` |
| Test | `make test` |
| Integration Test | `make test-integration` |
| Clean | `make clean` |

## Commit Conventions

Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) v1.0.0:

```
<type>(<scope>): <subject>
```

- `type`: `feat` / `fix` / `refactor` / `test` / `docs` / `chore`
- `scope`: component or package name
- `subject`: imperative mood, ≤72 characters

## PR Review

- Keep PRs small and focused — one PR solves one problem
- Ensure CI passes before requesting review
- Reviewers will check code style, test coverage, and documentation sync

## Reference Docs

- [`docs/system-prompt.md`](./docs/system-prompt.md) — Full System Prompt content and design principles
- [`docs/tool-descriptions.md`](./docs/tool-descriptions.md) — Schema definitions for all 12 built-in tools
- [`specs/`](./specs/) — Component design specs (read before modifying)
