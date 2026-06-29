# Contributing Guide

Thank you for considering contributing to Waveloom! Please read the following before getting started.

## Code of Conduct

- Be kind and professional
- Respect differing viewpoints and experiences
- Give and receive feedback constructively

## How to Contribute

### Report a Bug

1. Search [Issues](https://github.com/Menfre01/waveloom/issues) to confirm it hasn't been reported
2. Submit using the **Bug Report** template
3. Provide reproduction steps, expected behavior, actual behavior, and environment info (OS, terminal, waveloom version)

### Request a Feature

1. Search Issues for similar requests
2. Submit using the **Feature Request** template
3. Describe the use case and desired solution

### Submit Code

1. **Fork** this repository
2. **Create a branch**: prefix with `feat/`, `fix/`, or `refactor/`
3. **Write code**:
   - Follow Go community conventions (Effective Go)
   - Write tests first, then implement (TDD cycle)
   - Target 97%+ test coverage
4. **Build & test**: `make build && make test`
5. **Commit**: follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
   ```
   <type>(<scope>): <subject>
   ```
   Examples: `feat(tool): add web_fetch tool` / `fix(context): fix premature 60% watermark trigger`
6. **Open a Pull Request**:
   - Describe what changed and why
   - Link related Issues (e.g. `Closes #42`)
   - Ensure CI passes

## Development Process

Waveloom uses a **component-based Wave development process**, see [AGENTS.md](../AGENTS.md):

- Each task focuses on a single component — high cohesion, low coupling
- Each Wave produces a task spec → tests → review
- Avoid multiple people modifying the same file within a single Wave

## Project Structure

```
waveloom/
├── cmd/waveloom/          # Entry point + TUI
├── pkg/
│   ├── agentloop/         # Think-Act-Observe loop
│   ├── context/            # Context accumulation + four-tier compaction
│   ├── llm/               # LLM API wrapper
│   ├── memory/            # AGENTS.md hierarchical loading
│   ├── permission/        # Permission gatekeeper
│   ├── reference/         # @ file reference expansion
│   └── tool/              # Built-in tools
├── specs/                 # Component design specs
├── docs/                  # Documentation
└── Makefile
```

## Contact

- [GitHub Issues](https://github.com/Menfre01/waveloom/issues) — Bugs and feature requests
- [GitHub Discussions](https://github.com/Menfre01/waveloom/discussions) — General discussion and Q&A
