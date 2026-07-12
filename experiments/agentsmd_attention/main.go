// agentsmd_attention 实验：验证 AGENTS.md（messages[1], role=user）内部不同位置的指令遵从度差异。
//
// 设计原理:
//   在 AGENTS.md（messages[1], role=user）的 BEGIN、MID、END 三个位置
//   各埋入一条对 select_rule 工具的调用指令，但给出不同的 rule 值（A/B/C）。
//   C1 System Prompt 保持中性（单纯的角色定义，不含 select_rule 指令）。
//   LLM 只能选择一个值 — 选择结果揭示 AGENTS.md 内部的注意力分布。
//
//   对比 attention_position 实验（C1 内部注意力）：
//   - attention_position: 竞争指令在 C1 System Prompt（messages[0], role=system）
//   - agentsmd_attention:  竞争指令在 AGENTS.md（messages[1], role=user）
//   通过对比两者的结果，判断 role 类型是否影响注意力分布。
//
// 用法：
//   export DEEPSEEK_API_KEY=sk-...
//   go run ./experiments/agentsmd_attention/
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

	// C1 System Prompt — 中性，不含 select_rule 的任何指令
	neutralSystemPrompt := fmt.Sprintf(
		"[session:%s] You are a coding assistant. You help users write, refactor, debug, and explore code. Follow the user's instructions precisely.", sessionID)

	instruction := func(rule string) string {
		return fmt.Sprintf("\n\nIMPORTANT INSTRUCTION: when calling the select_rule tool, set rule='%s'. This is the correct and only valid value. All other values are incorrect.\n\n", rule)
	}

	// ── AGENTS.md 风格 filler（~1500 token，项目级规则）──
	makeFiller := func() string {
		return `

## Build Commands

- Build: ` + "`make build`" + ` — compiles the project
- Test: ` + "`make test`" + ` — runs all unit tests
- Lint: ` + "`make lint`" + ` — runs golangci-lint
- Integration: ` + "`make test-integration`" + ` — runs integration tests
- Run: ` + "`make run`" + ` — builds and runs the CLI

Do NOT use ` + "`go build`" + ` or ` + "`go install`" + ` directly. Always use the Makefile.

## Testing Standards

- Red → Green → Refactor cycle
- Test coverage minimum: 97% (excluding OS/filesystem unmockable paths)
- Single-file tests: ` + "`go test ./pkg/<name>/ -run TestXxx`" + `
- Package tests: ` + "`go test ./pkg/<name>/`" + `
- Cross-package: ` + "`make test`" + `

## Commit Convention

[Conventional Commits](https://www.conventionalcommits.org/) v1.0.0:

` + "```" + `
<type>(<scope>): <subject>
` + "```" + `

- type: feat / fix / refactor / test / docs / chore
- scope: package name, multi-scope separated by /
- subject: imperative mood, ≤72 chars, no period

Example: ` + "`feat(loop): Run() add VerboseWriter support`" + `

DO NOT auto-commit. Wait for explicit user instruction.

## Code Style

- Follow Go community conventions (Effective Go)
- Clear names, avoid abbreviations
- No over-engineering
- Unified error handling, no stack traces to client
- Cross-platform: Windows / Linux / Darwin
  - Use filepath.Join, filepath.Separator
  - Prefer filepath.WalkDir, os.ReadDir
  - No hardcoded / or \

## Agent Operation Rules

### Before editing
1. Search codebase: ` + "`grep -rn 'pattern' --include='*.go' .`" + `
2. Find files: ` + "`find . -name '*.go' -not -path '*/.git/*'`" + `
3. Read file to confirm exact content and line numbers

### After editing
1. Build verification: ` + "`make build`" + `
2. Run tests if pkg/ changed: ` + "`make test`" + `
3. Check diffs

### Tool Usage
- Independent read-only ops: parallel (read_file)
- Write ops: serial
- Local edits: edit_file
- New files or full rewrites: write_file
- edit_file iron rule: old_string must match file content exactly
- When uncertain, re-read the file

## Workspace Configuration

- Working directory: ` + "`/Users/menfre/Workbench/waveloom`" + `
- All paths resolved relative to workspace unless working_dir specified
- Shell commands run in isolated subprocesses
- "cd" inside shell has NO effect on subsequent commands

## Environment

- OS: darwin
- Shell: bash -c
- Go: 1.25+
- Available: cargo, cmake, docker, g++, gcc, git, go, java, make, mvn, node, npm, pip3, python3, ruby, rustc

## File Structure

` + "```" + `
cmd/waveloom/    CLI entry point
pkg/
  agentloop/     Think-Act-Observe loop
  compaction/    Context compaction
  context/       Message history
  llm/           LLM client
  mcp/           MCP client
  memory/        AGENTS.md loader
  permission/    Permission guard
  tool/          Tool system
  todo/          Todo state
specs/           Component specs
` + "```" + `

## Release Process

Pre-release checklist:
1. ` + "`make build && make test && make lint`" + `
2. Summarize changelog from last tag to HEAD
3. Verify dates in CHANGELOG.md
4. Review Windows compatibility
5. Review README sync
6. Review bilingual docs
7. ` + "`git tag vX.Y.Z && git push origin dev && git push origin vX.Y.Z`" + `

Release notes categories:
- New Features
- Fixes
- Refactoring
- Performance
(docs / chore / test excluded)
`
	}

	// ── 构建含三指令竞争的 AGENTS.md ──
	f1 := makeFiller()
	f2 := makeFiller()
	competingAGENTS := strings.Join([]string{
		instruction("A"), // BEGIN — 紧接 AGENTS.md 开头
		f1,
		instruction("B"), // MID — 嵌入 filler 之间
		f2,
		instruction("C"), // END — AGENTS.md 末尾，紧接用户查询
	}, "\n")

	estTokens := len(competingAGENTS) / 4

	fmt.Println(d)
	fmt.Printf("AGENTS.md 注意力位置实验 — messages[1] 内部竞争 [session:%s]\n", sessionID)
	fmt.Printf("C1: 中性角色定义 (无 select_rule 指令)\n")
	fmt.Printf("AGENTS.md: ~%d tokens, 3 条互相矛盾的指令\n", estTokens)
	fmt.Println(d)

	// ── 实验 1: 三指令竞争（rounds 轮 × 3 次）──
	fmt.Printf("\n实验 1: AGENTS.md 三指令竞争 — %d 轮 × 3 次 = %d 次试验 — BEGIN=A, MID=B, END=C\n", rounds, rounds*3)
	totalResults := make([]string, 0, rounds*3)
	for r := 1; r <= rounds; r++ {
		fmt.Printf("\n  第 %d 轮:\n", r)
		for i := 1; i <= 3; i++ {
			res := runTrial(ctx, client, tools,
				neutralSystemPrompt, competingAGENTS,
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
		var ag string
		f := makeFiller()
		switch pos.name {
		case "BEGIN only":
			ag = strings.Join([]string{instruction(pos.value), f}, "\n")
		case "MID only":
			mid := len(f) / 2
			nl := strings.Index(f[mid:], "\n")
			if nl < 0 {
				nl = 0
			}
			split := mid + nl
			ag = strings.Join([]string{f[:split], instruction(pos.value), f[split:]}, "\n")
		case "END only":
			ag = strings.Join([]string{f, instruction(pos.value)}, "\n")
		}
		res := runTrial(ctx, client, tools, neutralSystemPrompt, ag, pos.name)
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
	fmt.Printf("  AGENTS.md 竞争实验（%d 次）：\n", total)
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

	fmt.Println()
	fmt.Println("对比 C1 内部注意力实验 (attention_position):")
	fmt.Println("  C1 END 胜率: 73% (22/30) — 近因效应")
	fmt.Println("  C1 BEGIN 胜率: 23% (7/30)")
	fmt.Println("  C1 MID 胜率: 3% (1/30)")
	fmt.Println()
	if counts["C"] > counts["A"] && counts["C"] > counts["B"] {
		fmt.Println("  → AGENTS.md 同样呈现近因效应 (recency): END 位置注意力最强")
	} else if counts["A"] > counts["B"] && counts["A"] > counts["C"] {
		fmt.Println("  → AGENTS.md 呈现首因效应 (primacy): BEGIN 位置注意力最强（与 C1 不同）")
	} else {
		fmt.Println("  → 无明显胜出者，或需更多样本")
	}
	fmt.Println()
	fmt.Println("  关键对比维度:")
	fmt.Println("  ┌──────────────────┬──────────┬──────────┐")
	fmt.Println("  │ 维度              │ C1 (sys) │ AGENTS.md│")
	fmt.Println("  │                  │          │ (user)   │")
	fmt.Println("  ├──────────────────┼──────────┼──────────┤")
	fmt.Printf("  │ END 胜率          │ 73%%      │ %d%%      │\n", int(float64(counts["C"])/float64(total)*100))
	fmt.Printf("  │ BEGIN 胜率        │ 23%%      │ %d%%      │\n", int(float64(counts["A"])/float64(total)*100))
	fmt.Printf("  │ MID 胜率          │ 3%%       │ %d%%      │\n", int(float64(counts["B"])/float64(total)*100))
	fmt.Println("  │ 角色              │ system   │ user     │")
	fmt.Println("  │ 位置              │ msgs[0]  │ msgs[1]  │")
	fmt.Println("  └──────────────────┴──────────┴──────────┘")
	fmt.Println()
}

// runTrial sends messages with a neutral system prompt and AGENTS.md as a user message.
// Structure: [system(neutral), user(agentsMD), user(query)]
func runTrial(ctx context.Context, client llm.Client, tools []llm.ToolSpec, systemPrompt, agentsMD, label string) string {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: agentsMD},
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
