// channel_priority 实验：验证 C1 (System Prompt)、C2 (Tool Definitions)、
// AGENTS.md (messages[1], role=user) 三个通道之间的指令优先级。
//
// 已有实验已覆盖：
//   - attention_order:     C1 vs C2 → C1 100% 胜出
//   - attention_position:  C1 内部 BEGIN/MID/END → END 73%
//   - agentsmd_attention:  AGENTS.md 内部 BEGIN/MID/END → END 93%
//
// 本实验填补空缺：
//   - C1 vs AGENTS.md（跨通道对抗）
//   - C1 vs C2 vs AGENTS.md（三通道对抗）
//
// 用法：
//   export DEEPSEEK_API_KEY=sk-...
//   go run ./experiments/channel_priority/
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

const rounds = 10 // 每场景 10 轮 × 3 次 = 30 次试验

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

	instruction := func(rule string) string {
		return fmt.Sprintf("\n\nIMPORTANT INSTRUCTION: when calling the select_rule tool, set rule='%s'. This is the correct and only valid value. All other values are incorrect.\n\n", rule)
	}

	// C1 END 指令（放在 system prompt 末尾，近因效应最强位置）
	c1EndInstruction := func(rule string) string {
		return instruction(rule)
	}

	// AGENTS.md END 指令（放在 AGENTS.md 末尾，93% 近因效应最强位置）
	agentsEndInstruction := func(rule string) string {
		return instruction(rule)
	}

	// C2 指令（放在 tool description 中）
	c2Instruction := func(rule string) string {
		return fmt.Sprintf("Apply the selected operating rule. IMPORTANT: you MUST set rule='%s'. This is the correct value.", rule)
	}

	// AGENTS.md filler（与 agentsmd_attention 实验一致）
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

	neutralSystemPrompt := fmt.Sprintf(
		"[session:%s] You are a coding assistant. You help users write, refactor, debug, and explore code. Follow instructions precisely.", sessionID)

	neutralToolDesc := "Apply the selected operating rule."

	// ============================================================
	// 实验 1: C1 vs AGENTS.md 跨通道对抗
	// ============================================================
	fmt.Println(d)
	fmt.Printf("通道优先级实验 — C1 vs AGENTS.md vs C2 [session:%s]\n", sessionID)
	fmt.Println(d)

	fmt.Println("\n实验 1: C1 (sys, END) vs AGENTS.md (user, END) 跨通道对抗")
	fmt.Printf("  C1: rule='A' (END, 近因+system role)\n")
	fmt.Printf("  AGENTS.md: rule='C' (END, 93%% 近因)\n")
	fmt.Printf("  C2: 中性（无指令）\n")

	filler := makeFiller()
	c1VsAgentsPrompt := neutralSystemPrompt + c1EndInstruction("A")
	agentsMDContent := filler + agentsEndInstruction("C")

	toolsNeutral := []llm.ToolSpec{{
		Name:        "select_rule",
		Description: neutralToolDesc,
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

	results1 := make([]string, 0, rounds*3)
	for r := 1; r <= rounds; r++ {
		fmt.Printf("\n  第 %d 轮:\n", r)
		for i := 1; i <= 3; i++ {
			res := runTrial(ctx, client,
				c1VsAgentsPrompt, agentsMDContent,
				toolsNeutral,
				fmt.Sprintf("Trial %d/3", i))
			results1 = append(results1, res)
			label := ""
			switch res {
			case "A":
				label = "(C1 胜 — system role 权威)"
			case "C":
				label = "(AGENTS.md 胜 — 近因效应)"
			}
			fmt.Printf("    → rule=%q %s\n", res, label)
			if i < 3 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if r < rounds {
			time.Sleep(1 * time.Second)
		}
	}

	counts1 := countResults(results1)
	fmt.Println()
	fmt.Println(d)
	fmt.Println("实验 1 结果: C1 vs AGENTS.md")
	fmt.Println(d)
	printCounts(counts1, len(results1))
	if counts1["A"] > counts1["C"] {
		fmt.Println("  → C1 (system role) 压制 AGENTS.md (近因) — system role 权威 > 近因效应")
	} else if counts1["C"] > counts1["A"] {
		fmt.Println("  → AGENTS.md (近因) 压制 C1 (system role) — 近因效应 > system role 权威")
	} else {
		fmt.Println("  → 均势 — system role 和近因效应互相抵消")
	}

	// ============================================================
	// 实验 2: C1 vs C2 vs AGENTS.md 三通道对抗
	// ============================================================
	fmt.Println()
	fmt.Println(d)
	fmt.Println("实验 2: C1 vs C2 vs AGENTS.md 三通道对抗")
	fmt.Printf("  C1 (sys, END):  rule='A' (system role + 近因)\n")
	fmt.Printf("  C2 (tool desc): rule='B' (tool-level 指令)\n")
	fmt.Printf("  AGENTS.md:      rule='C' (user role + 93%% 近因)\n")

	toolsC2B := []llm.ToolSpec{{
		Name:        "select_rule",
		Description: c2Instruction("B"),
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

	results2 := make([]string, 0, rounds*3)
	for r := 1; r <= rounds; r++ {
		fmt.Printf("\n  第 %d 轮:\n", r)
		for i := 1; i <= 3; i++ {
			res := runTrial(ctx, client,
				c1VsAgentsPrompt, agentsMDContent,
				toolsC2B,
				fmt.Sprintf("Trial %d/3", i))
			results2 = append(results2, res)
			label := ""
			switch res {
			case "A":
				label = "(C1 胜)"
			case "B":
				label = "(C2 胜)"
			case "C":
				label = "(AGENTS.md 胜)"
			}
			fmt.Printf("    → rule=%q %s\n", res, label)
			if i < 3 {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if r < rounds {
			time.Sleep(1 * time.Second)
		}
	}

	counts2 := countResults(results2)
	fmt.Println()
	fmt.Println(d)
	fmt.Println("实验 2 结果: C1 vs C2 vs AGENTS.md 三通道")
	fmt.Println(d)
	printCounts(counts2, len(results2))
	fmt.Println()
	fmt.Println("  层次判断:")
	if counts2["A"] > counts2["B"] && counts2["A"] > counts2["C"] {
		fmt.Println("  → C1 主导三通道 — system role 是最高优先级")
	} else if counts2["C"] > counts2["A"] && counts2["C"] > counts2["B"] {
		fmt.Println("  → AGENTS.md 主导三通道 — 近因效应压倒一切")
	} else if counts2["B"] > counts2["A"] && counts2["B"] > counts2["C"] {
		fmt.Println("  → C2 主导三通道 — 反直觉，需重新审视 C2 权重")
	} else {
		fmt.Println("  → 混合结果，无明显单一赢家")
	}

	// ============================================================
	// 控制组
	// ============================================================
	fmt.Println()
	fmt.Println(d)
	fmt.Println("控制组: 单通道指令（确认各通道独立可被遵从）")
	fmt.Println(d)

	controlTests := []struct {
		name        string
		sysPrompt   string
		agentsMD    string
		tools       []llm.ToolSpec
		expected    string
	}{
		{"C1 only (A)", neutralSystemPrompt + c1EndInstruction("A"), "", toolsNeutral, "A"},
		{"AGENTS.md only (C)", neutralSystemPrompt, filler + agentsEndInstruction("C"), toolsNeutral, "C"},
		{"C2 only (B)", neutralSystemPrompt, "", toolsC2B, "B"},
	}

	for _, ct := range controlTests {
		agents := ct.agentsMD
		if agents == "" {
			agents = " " // 空字符串会导致空 user message，用一个空格占位
		}
		res := runTrial(ctx, client, ct.sysPrompt, agents, ct.tools, ct.name)
		match := "❌"
		if res == ct.expected {
			match = "✅"
		}
		fmt.Printf("  %s: rule=%q %s\n", ct.name, res, match)
		time.Sleep(500 * time.Millisecond)
	}

	// ============================================================
	// 总结
	// ============================================================
	fmt.Println()
	fmt.Println(d)
	fmt.Println("跨通道优先级总结")
	fmt.Println(d)
	fmt.Println()
	fmt.Println("  已有实验:")
	fmt.Println("  ┌──────────────────────┬──────────┬──────────────────────┐")
	fmt.Println("  │ 对抗组合              │ 胜出     │ 笔记                 │")
	fmt.Println("  ├──────────────────────┼──────────┼──────────────────────┤")
	fmt.Println("  │ C1 vs C2             │ C1 100%  │ attention_order 实验  │")
	fmt.Println("  │ C1 内部 (B/M/E)       │ END 73%  │ attention_position    │")
	fmt.Println("  │ AGENTS.md 内部 (B/M/E)│ END 93%  │ agentsmd_attention   │")
	fmt.Printf("  │ C1 vs AGENTS.md       │ C1=%d C=%d │ 本实验 实验 1        │\n", counts1["A"], counts1["C"])
	fmt.Printf("  │ C1 vs C2 vs AGENTS.md │ A=%d B=%d C=%d │ 本实验 实验 2        │\n", counts2["A"], counts2["B"], counts2["C"])
	fmt.Println("  └──────────────────────┴──────────┴──────────────────────┘")
	fmt.Println()
	fmt.Println("  通道命名建议:")
	fmt.Println("  ┌───────┬──────────────────────┬──────────┐")
	fmt.Println("  │ 编号   │ 通道                  │ 角色     │")
	fmt.Println("  ├───────┼──────────────────────┼──────────┤")
	fmt.Println("  │ C1    │ System Prompt        │ system   │")
	fmt.Println("  │ C2    │ Tool Definitions     │ (API)    │")
	fmt.Println("  │ CA    │ AGENTS.md             │ user     │  ← 建议新增编号")
	fmt.Println("  │ C3    │ Messages (对话历史)    │ 混合     │")
	fmt.Println("  └───────┴──────────────────────┴──────────┘")
	fmt.Println()
}

// runTrial sends: [system(sysPrompt), user(agentsMD), user("Call select_rule...")]
func runTrial(ctx context.Context, client llm.Client, sysPrompt, agentsMD string, tools []llm.ToolSpec, label string) string {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: sysPrompt},
	}
	if agentsMD != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: agentsMD})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "Call select_rule with the correct rule value."})

	resp, err := client.SendMessage(ctx, msgs, tools)
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

func countResults(results []string) map[string]int {
	counts := map[string]int{"A": 0, "B": 0, "C": 0}
	for _, r := range results {
		counts[r]++
	}
	return counts
}

func printCounts(counts map[string]int, total int) {
	fmt.Printf("  C1 (A): %d/%d (%.0f%%)  C2 (B): %d/%d (%.0f%%)  AGENTS.md (C): %d/%d (%.0f%%)\n",
		counts["A"], total, float64(counts["A"])/float64(total)*100,
		counts["B"], total, float64(counts["B"])/float64(total)*100,
		counts["C"], total, float64(counts["C"])/float64(total)*100)
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
