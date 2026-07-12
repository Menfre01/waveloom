// tool_cache 实验：验证 DeepSeek API 中 tools 字段的计费和缓存行为。
//
// 本文件位于 experiments/ 目录，不属于主工程。make build 和 go build ./... 均不包含此目录。
// 直接通过 go run ./experiments/tool_cache/ 执行。
//
// 实验 1 — tools 是否计入 input tokens：
//   Call A: 不带 tools，记 prompt_tokens
//   Call B: 带工具定义，记 prompt_tokens
//   差值 → tools 的 token 成本
//
// 实验 2 — tools 是否参与前缀缓存：
//   Call 1: [sys][user] + tools → 建立缓存
//   Call 2: [sys][user][asst][user] + 相同 tools → 观察 cache_hit
//   若 cache_hit ≥ tools 部分的 token 数 → tools 在缓存前缀中
//
// 用法：
//   export DEEPSEEK_API_KEY=sk-...
//   go run ./experiments/tool_cache/
//
// 或将 API Key 配置在 ~/.waveloom/settings.json 的 llm.api_key 字段中。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
)

func main() {
	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "未找到 API Key。请设置环境变量 DEEPSEEK_API_KEY 或配置 ~/.waveloom/settings.json")
		os.Exit(1)
	}

	client, err := llm.NewClient(llm.ClientConfig{
		Provider: llm.ProviderDeepSeek,
		APIKey:   apiKey,
		Model:    "deepseek-v4-flash",
		Timeout:  60 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 client 失败: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// 每次运行使用唯一 system prompt，避免跨次缓存污染
	sessionID := randomHex(4)
	sysPrompt := fmt.Sprintf("You are a helpful assistant. Answer briefly. [session:%s]", sessionID)

	tools := []llm.ToolSpec{
		{
			Name:        "read_file",
			Description: "Read a file with line numbers. Supports offset and limit parameters.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "File path"},
					"offset":    map[string]any{"type": "integer", "description": "Starting line number (0-based)"},
				},
				"required": []string{"file_path"},
			},
		},
		{
			Name:        "shell",
			Description: "Execute a shell command in a subprocess.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "Shell command to execute"},
				},
				"required": []string{"command"},
			},
		},
	}

	d := "══════════════════════════════════════════════════════════════"

	// ════════════════════ 实验 1: tools 是否计入 input tokens ════════════════════
	fmt.Println(d)
	fmt.Println("实验 1: tools 是否计入 input tokens")
	fmt.Println(d)
	fmt.Printf("  session: %s\n\n", sessionID)

	fmt.Println("► Call A: 不带 tools")
	respA, err := client.SendMessage(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: sysPrompt},
		{Role: llm.RoleUser, Content: "What is 2+2?"},
	}, nil)
	fatalIf(err)
	printUsage("A", respA)

	fmt.Println("\n► Call B: 带 2 个 tools（相同 system，不同 user message）")
	respB, err := client.SendMessage(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: sysPrompt},
		{Role: llm.RoleUser, Content: "What is 3+3?"},
	}, tools)
	fatalIf(err)
	printUsage("B", respB)

	if respA.Usage != nil && respB.Usage != nil {
		delta := respB.Usage.PromptTokens - respA.Usage.PromptTokens
		fmt.Println()
		fmt.Println("  ── 差异分析 ──")
		fmt.Printf("  prompt_tokens (无 tools): %d\n", respA.Usage.PromptTokens)
		fmt.Printf("  prompt_tokens (有 tools): %d\n", respB.Usage.PromptTokens)
		fmt.Printf("  差值 (2 个 tools 成本):   %d tokens (~%d per tool)\n", delta, delta/2)
		if delta > 0 {
			fmt.Println("  ✅ 结论: tools 计入 input tokens")
		} else {
			fmt.Println("  ❓ 结论: tools 未计入 input tokens")
		}
	} else {
		fmt.Fprintln(os.Stderr, "  usage 数据缺失")
		os.Exit(1)
	}

	toolTokens := respB.Usage.PromptTokens - respA.Usage.PromptTokens
	fmt.Printf("\n  注: tools 部分约占 %d tokens\n", toolTokens)

	// ════════════════════ 实验 2: tools 是否参与前缀缓存 ════════════════════
	fmt.Println()
	fmt.Println(d)
	fmt.Println("实验 2: tools 是否参与前缀缓存")
	fmt.Println(d)

	prefix := []llm.Message{
		{Role: llm.RoleSystem, Content: sysPrompt},
		{Role: llm.RoleUser, Content: "What is the capital of France?"},
	}

	fmt.Println("\n► Call 1: 建立缓存前缀 [sys][user][2 tools]")
	resp1, err := client.SendMessage(ctx, prefix, tools)
	fatalIf(err)
	printUsage("1", resp1)

	// 验证 Call 1 未被污染：首次调用应全部 miss
	if resp1.Usage != nil && resp1.Usage.CacheHitTokens != 0 {
		fmt.Printf("  ⚠️  警告: Call 1 存在非预期的 cache_hit=%d，可能有残留缓存\n", resp1.Usage.CacheHitTokens)
	}

	// 等一秒确保缓存落盘
	time.Sleep(1 * time.Second)

	fmt.Println("\n► Call 2: 相同前缀 + 追加新消息（测试缓存命中）")
	call2 := append(prefix,
		llm.Message{Role: llm.RoleAssistant, Content: resp1.Content},
		llm.Message{Role: llm.RoleUser, Content: "What about Germany?"},
	)
	resp2, err := client.SendMessage(ctx, call2, tools)
	fatalIf(err)
	printUsage("2", resp2)

	if resp1.Usage != nil && resp2.Usage != nil {
		prefixTokens := resp1.Usage.PromptTokens
		cacheHit := resp2.Usage.CacheHitTokens

		// 关键计算:
		// Call 1 的 prompt_tokens = sys + user + tools
		// 如果 tools 参与缓存 → cache_hit 应 ≥ tools 部分（约 toolTokens）
		// user message 的 token 数 ≈ Call 1 prompt_tokens - toolTokens - sys_tokens
		// 但更简单：Call 1 的 prompt_tokens 去掉 user message 就是 sys + tools
		// Call 2 的 user message 变了但 sys+tools 相同
		// → cache_hit ≈ sys + tools 的部分
		// → 如果 cache_hit > toolTokens → tools 确定在缓存中

		fmt.Println()
		fmt.Println("  ── 缓存命中分析 ──")
		fmt.Printf("  Call 1 prompt_tokens:       %d (sys + user + tools)\n", prefixTokens)
		fmt.Printf("  Call 2 prompt_tokens:       %d\n", resp2.Usage.PromptTokens)
		fmt.Printf("  cache_hit_tokens:           %d\n", cacheHit)
		fmt.Printf("  cache_miss_tokens:          %d\n", resp2.Usage.CacheMissTokens)
		fmt.Printf("  命中率:                     %.1f%%\n",
			float64(cacheHit)/float64(resp2.Usage.PromptTokens)*100)
		fmt.Printf("  tools 部分 (来自实验 1):    %d tokens\n", toolTokens)

		if cacheHit >= toolTokens {
			fmt.Println("  ✅ 结论: tools 参与前缀缓存（hit ≥ tools token 数）")
		} else {
			fmt.Printf("  ❓ 结论: cache_hit (%d) < tools (%d)，tools 未确认缓存\n",
				cacheHit, toolTokens)
		}
	}

	fmt.Println()
	fmt.Println(d)
	fmt.Println("实验完成")
	fmt.Println(d)
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

func printUsage(label string, resp *llm.Response) {
	if resp.Usage == nil {
		fmt.Printf("  [%s] usage 为 nil\n", label)
		return
	}
	fmt.Printf("  [%s] prompt_tokens=%d  completion_tokens=%d  total=%d  cache_hit=%d  cache_miss=%d\n",
		label,
		resp.Usage.PromptTokens,
		resp.Usage.CompletionTokens,
		resp.Usage.TotalTokens,
		resp.Usage.CacheHitTokens,
		resp.Usage.CacheMissTokens,
	)
}
