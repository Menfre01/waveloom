// attention_position 实验：验证 System Prompt 中不同位置的指令遵从度差异。
//
// 设计原理:
//   在同一份 ~6000 token 的 System Prompt 中，BEGIN、MID、END 三个位置
//   各埋入一条对 select_rule 工具的调用指令，但给出不同的 rule 值（A/B/C）。
//   LLM 只能选择一个值 — 选择结果揭示哪个位置获得了最高注意力。
//
//   还包含控制组：单指令（无竞争）验证每个位置都能被独立遵从。
//
// 用法：
//   export DEEPSEEK_API_KEY=sk-...
//   go run ./experiments/attention_position/
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
)

const rounds = 10 // 轮数：每轮 3 次竞争试验

func main() {
	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "未找到 API Key")
		os.Exit(1)
	}
	client, err := llm.NewClient(llm.ClientConfig{
		Provider: llm.ProviderDeepSeek,
		APIKey:   apiKey,
		Model:    "deepseek-v4-flash",
		Timeout:  60 * time.Second,
	})
	fatalIf(err)
	ctx := context.Background()

	sessionID := randomHex(4)
	d := strings.Repeat("=", 62)

	tools := []llm.ToolSpec{{
		Name:        "select_rule",
		Description: "Apply the selected operating rule.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rule": map[string]any{
					"type":        "string",
					"description": "Rule to apply, one of: A, B, C",
					"enum":        []string{"A", "B", "C"},
				},
			},
			"required": []string{"rule"},
		},
	}}

	rolePrefix := fmt.Sprintf("[session:%s] You are a coding assistant. Follow instructions precisely.", sessionID)

	instruction := func(rule string) string {
		return fmt.Sprintf("\n\nIMPORTANT INSTRUCTION: when calling the select_rule tool, set rule='%s'. This is the correct and only valid value. All other values are incorrect.\n\n", rule)
	}

	// ── filler 块（~1500 token）──
	makeFiller := func() string {
		return `

## Coding Standards

- Follow Go community conventions (Effective Go, standard library project layout)
- No over-engineering. Clear names, avoid abbreviations.
- Unified error handling. No stack traces to client.
- Query APIs: prefer go doc.
- Cross-platform compatibility: Windows / Linux / Darwin.
  - File system operations: prefer filepath.WalkDir, os.ReadDir.
  - Path construction: use filepath.Join, filepath.Separator.
  - No hardcoded / or \ in paths.
  - Confirm third-party package cross-platform support before use.

## Agent Operation Checklist

| Phase | Action |
|-------|--------|
| Before edit | find/grep → read_file → confirm line numbers and content |
| After edit | Build verification → make build → make test (if pkg/ involved) |
| Before refactor | grep → read_file → assess impact scope |

## Tool Usage Principles

- Independent read-only operations: parallel (read_file)
- Write operations: serial
- Local edits: prefer edit_file. New files: write_file.
- edit_file iron rule: old_string must exactly match current file content.
  Reliable sources: read_file within 2 turns with no intervening edits.
  Unreliable: memory, old reads, reads after edits.
- When uncertain: re-read the file. One extra read beats a no_match loop.

## Development Workflow

### Wave Development
- Tasks split by component cohesion, low inter-component coupling.
- Each task is an independent Wave: component dev → test → review → assembly.
- Before Wave: produce spec (file list, component boundaries/dependencies/invariants, integration points).
- Cold agent executes tests and reviews after completion.
- Main agent coordinates Wave boundaries, advances only after critical dependencies complete.
- No file edited by more than one Wave task at a time.

### TDD
- Red → Green → Refactor.
- Test coverage ≥ 97% (excluding OS/filesystem unmockable paths).

### Bug Fix Regression Protection
- Every fix MUST include regression protection.
- Testable: write TestRegression_<short_description>, assert root cause.
- Untestable: add // REGRESSION: <root cause>. Cannot test: <reason> above fix point.
- ≥ 3 regressions in same code area → fragile module, refactor priority.

## Build & Test

- Do NOT use go build / go install directly. Use Makefile.
- Build: make build | Install: make install | Run: make run | Clean: make clean
- Test single file/package: go test ./pkg/<name>/ -run TestXxx
- Test across packages: make test
- Integration test: make test-integration

## Documentation Standards

- Architecture/flow/data model: ASCII first, then convert to Mermaid.

## Commit Strategy

- DO NOT auto-commit. Wait for explicit user instruction ("commit", "提交").
- Conventional Commits v1.0.0: <type>(<scope>): <subject>
- type: feat / fix / refactor / test / docs / chore
- scope: package name. Multiple scopes separated by /.
- subject: Chinese imperative, ≤72 chars, no period.
- Example: feat(loop): Run() add VerboseWriter support

## Release Process

Pre-release checks (all must pass):
  make build && make test && make lint

Release notes organized by user-perceivable changes:
  - New Features — new capabilities, modules, commands
  - Fixes — bug fixes
  - Refactoring — major module restructuring
  - Performance — performance improvements
  (docs / chore / test types excluded)

Release by GitHub Actions (tag push v* → .github/workflows/release.yml).

Manual steps before release:
  1. Summarize changelog from last tag to HEAD
  2. Verify dates in CHANGELOG.md and CHANGELOG.en.md match today
  3. Review Windows compatibility
  4. Review README sync
  5. Review bilingual doc sync
  6. Commit docs changes (type: docs)
  7. Tag and push: git tag vX.Y.Z && git push origin dev && git push origin vX.Y.Z

## Error Handling Strategy

- Recoverable errors (retry once): command_failed, command_not_found, timeout, file_not_found, invalid_args, no_match, no_results, not_dir, binary_file, multiple_matches.
- Fatal errors (do not retry): permission_denied, security_violation, disk_full, unknown_tool.
- not_dir: error message includes directory listing. Pick from listing or use suggestion, retry immediately.
- file_not_found: use suggested path, or bash to locate correct file.
- binary_file: verify filename, use bash to check directory contents.
- no_match: error includes hint with closest matching lines. Use read_file to verify exact content, then retry.
- multiple_matches: error shows each match with context. Pick one, include surrounding lines in old_string.

## Backoff & Loop Protection

- Loop tracks consecutive turns where ALL tool calls fail with same (tool, error_kind) pair.
- Changing tool OR error kind resets the counter.
- Any successful tool call resets the counter entirely.
- At 3 consecutive same errors → system warning.
- At 5 → stronger warning.
- At 8 → loop termination.
- Change approach BEFORE the warning:
  - Try different tool for same goal.
  - Try same tool with different arguments.
  - If neither works → ask user for guidance.

## Parallel Execution Guidelines

- ALWAYS parallelize independent agent calls.
- Trigger patterns for parallel dispatch:
  - Multiple independent topics → one agent per topic.
  - Codebase exploration across multiple packages → one Explore agent each.
  - Research decomposable into independent questions → parallel forks.
  - Post-implementation: verification + code review → launch together.
- Anti-patterns:
  - Call agent A, wait, then call agent B when independent.
  - Use single agent to sequentially explore N packages.

## Agent Tool Reference

### Available agent types
- (omit) / fork: Research, implementation, analysis. Inherits context.
- Explore: Code search, file discovery, read-only. Cold (fast model).
- evaluate: Code review, security audit, second opinion. Cold.
- verification: Post-implementation testing. Cold.
- advisor: Deep analysis, trade-off evaluation. Inherits context.

### When to fork (omit subagent_type)
- Fork when intermediate output isn't worth keeping in context.
- Fork is DEFAULT and cheapest — prefer over cold agents.
- Launch parallel forks for decomposable tasks.
- Implementation: fork work requiring more than a couple of edits.

### When to use cold agent (with subagent_type)
- Need independent perspective (code review, audit).
- Cold agents start with fresh context, cannot reuse parent's prompt cache.
- More expensive than forks. Use only when independence justifies cost.

### Writing the prompt
- description: 3-5 word task label, not a full sentence.
- Cold agents: brief like a smart colleague, explain what you're trying to accomplish.
- Fork prompts: directive. Fork inherits context. Be specific about scope.
- Include file paths, line numbers, precise changes.

### Output cost
- Output tokens: 240× cost of cached input, 2× cost of uncached input.
- Constrain subagent output. Prefer concise, structured responses.
- Subagent final output is permanent in context. Exclude irrelevant detail.

## Plan Mode Reference

- Use ONLY for complex features/refactoring (3+ files, architectural decisions).
- Do NOT use for: code review, bug analysis, performance investigation, explaining code, answering questions.
- Skip for: single-file fixes, trivial bugs, precise step-by-step user instructions.

## Todo List Usage

- Complex multi-step tasks (3+ distinct steps) → use todo_write.
- Mark in_progress BEFORE starting work.
- Exactly ONE in_progress at a time.
- Mark complete IMMEDIATELY after finishing.
- When in doubt, use the tool.

## Shell Command Guidelines

- Keep commands to SINGLE LINE. Chain with &&.
- Prefer dedicated tools over shell: read_file (not cat), write_file (not echo), edit_file (not sed).
- Do NOT prefix commands with # comments.
- Multiple independent commands: launch as parallel shell calls.
- Commands run in workspace directory by default.
- For verification scripts: prefer python, write to temp file, clean up after.

## Workspace Rules

- Workspace directory is default base for all operations, not a boundary.
- Shell commands run in isolated subprocesses.
- "cd" inside shell has NO effect on subsequent commands.
- Use working_dir parameter to change execution directory per command.
`
	}

	// ── 构建含三指令竞争的 System Prompt ──
	f1 := makeFiller()
	f2 := makeFiller()
	competingPrompt := strings.Join([]string{
		rolePrefix,
		instruction("A"), // BEGIN
		f1,
		instruction("B"), // MID
		f2,
		instruction("C"), // END
	}, "\n")

	estTokens := len(competingPrompt) / 4

	fmt.Println(d)
	fmt.Printf("注意力位置实验 — C1 内部竞争 [session:%s]\n", sessionID)
	fmt.Printf("C1 大小: ~%d tokens, 3 条互相矛盾的指令\n", estTokens)
	fmt.Println(d)

	// ── 实验 1: 三指令竞争（rounds 轮 × 3 次）──
	fmt.Printf("\n实验 1: 三指令竞争 — %d 轮 × 3 次 = %d 次试验 — BEGIN=A, MID=B, END=C\n", rounds, rounds*3)
	totalResults := make([]string, 0, rounds*3)
	for r := 1; r <= rounds; r++ {
		fmt.Printf("\n  第 %d 轮:\n", r)
		for i := 1; i <= 3; i++ {
			res := runTrial(ctx, client, tools,
				competingPrompt,
				fmt.Sprintf("Trial %d/3", i))
			totalResults = append(totalResults, res)
			fmt.Printf("    → rule=%q %s\n", res, posLabel(res))
			if i < 3 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if r < rounds {
			time.Sleep(1 * time.Second)
		}
	}

	fmt.Println()
	fmt.Println(d)
	fmt.Println("竞争结果汇总:")
	fmt.Println(d)
	counts := map[string]int{"A": 0, "B": 0, "C": 0}
	for _, r := range totalResults {
		counts[r]++
	}
	total := len(totalResults)
	fmt.Printf("\n  BEGIN (A): %d/%d (%.0f%%)  MID (B): %d/%d (%.0f%%)  END (C): %d/%d (%.0f%%)\n",
		counts["A"], total, float64(counts["A"])/float64(total)*100,
		counts["B"], total, float64(counts["B"])/float64(total)*100,
		counts["C"], total, float64(counts["C"])/float64(total)*100)

	// ── 实验 2: 控制组 — 单指令（无竞争）各 1 次 ──
	fmt.Println()
	fmt.Println(d)
	fmt.Println("实验 2: 控制组 — 单指令（确认每个位置都能被独立遵从）")
	fmt.Println(d)

	for _, pos := range []struct{ name, value string }{
		{"BEGIN only", "A"},
		{"MID only", "B"},
		{"END only", "C"},
	} {
		var sp string
		f := makeFiller()
		switch pos.name {
		case "BEGIN only":
			sp = strings.Join([]string{rolePrefix, instruction(pos.value), f}, "\n")
		case "MID only":
			mid := len(f) / 2
			nl := strings.Index(f[mid:], "\n")
			if nl < 0 {
				nl = 0
			}
			split := mid + nl
			sp = strings.Join([]string{rolePrefix, f[:split], instruction(pos.value), f[split:]}, "\n")
		case "END only":
			sp = strings.Join([]string{rolePrefix, f, instruction(pos.value)}, "\n")
		}
		res := runTrial(ctx, client, tools, sp, pos.name)
		match := "❌"
		if res == pos.value {
			match = "✅"
		}
		fmt.Printf("  %s: rule=%q %s\n", pos.name, res, match)
		time.Sleep(500 * time.Millisecond)
	}

	// ── 结论 ──
	fmt.Println()
	fmt.Println(d)
	fmt.Println("结论")
	fmt.Println(d)
	fmt.Println()
	fmt.Printf("  竞争实验（%d 次）：\n", total)
	for i, r := range totalResults {
		if i > 0 && i%30 == 0 {
			fmt.Println()
		}
		fmt.Printf(" %s", r)
	}
	fmt.Println()
	fmt.Printf("\n  BEGIN 胜: %d/%d (%.0f%%)  MID 胜: %d/%d (%.0f%%)  END 胜: %d/%d (%.0f%%)\n",
		counts["A"], total, float64(counts["A"])/float64(total)*100,
		counts["B"], total, float64(counts["B"])/float64(total)*100,
		counts["C"], total, float64(counts["C"])/float64(total)*100)
	if counts["A"] > counts["B"] && counts["A"] > counts["C"] {
		fmt.Println("  → 首因效应 (primacy): BEGIN 位置注意力最强")
	} else if counts["C"] > counts["A"] && counts["C"] > counts["B"] {
		fmt.Println("  → 近因效应 (recency): END 位置注意力最强")
	} else if counts["B"] > counts["A"] && counts["B"] > counts["C"] {
		fmt.Println("  → MID 位置注意力最强（反直觉）")
	} else {
		fmt.Println("  → 无明显胜出者，或需更多样本")
	}
	fmt.Println()
}

func runTrial(ctx context.Context, client llm.Client, tools []llm.ToolSpec, sysPrompt, label string) string {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: sysPrompt},
		{Role: llm.RoleUser, Content: "Call select_rule with the correct rule value."},
	}
	resp, err := client.SendMessage(ctx, messages, tools)
	if err != nil {
		fmt.Printf("  [%s] ERROR: %s\n", label, err)
		return "ERROR"
	}
	if len(resp.ToolCalls) == 0 {
		fmt.Printf("  [%s] NO CALL: %s\n", label, truncate(resp.Content, 50))
		return "NONE"
	}
	var args struct {
		Rule string `json:"rule"`
	}
	if err := json.Unmarshal([]byte(resp.ToolCalls[0].Arguments), &args); err != nil {
		fmt.Printf("  [%s] PARSE: %s\n", label, resp.ToolCalls[0].Arguments)
		return "PARSE"
	}
	return args.Rule
}

func posLabel(rule string) string {
	switch rule {
	case "A":
		return "(BEGIN)"
	case "B":
		return "(MID)"
	case "C":
		return "(END)"
	default:
		return ""
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func resolveAPIKey() string {
	if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
		return k
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	settings, err := llm.LoadSettingsIfExists(homeDir + "/.waveloom/settings.json")
	if err != nil || settings == nil {
		return ""
	}
	return settings.APIKey
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "  失败: %v\n", err)
		os.Exit(1)
	}
}
