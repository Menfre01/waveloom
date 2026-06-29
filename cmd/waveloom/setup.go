package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// runSetup 首次设置向导。引导用户完成必要配置，写入 ~/.waveloom/settings.json。
func runSetup() {
	fmt.Println()
	fmt.Println("  ╭─────────────────────────────────────────────╮")
	fmt.Println("  │         Waveloom · 首次设置                  │")
	fmt.Println("  ╰─────────────────────────────────────────────╯")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	// 1. 确定配置文件路径（全局 ~/.waveloom/settings.json）
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("  Error: 无法获取用户主目录")
		os.Exit(1)
	}
	configPath := filepath.Join(homeDir, ".waveloom", "settings.json")

	// 提示已有配置将被覆盖
	if settings, err := llm.LoadSettingsIfExists(configPath); err == nil && settings != nil && settings.APIKey != "" {
		fmt.Printf("  检测到已有配置: %s\n", configPath)
		fmt.Println("  继续操作将覆盖当前的 api_key。")
		fmt.Println()
	}

	// 2. 选择 Provider
	fmt.Println("  Step 1/3 — 选择 Provider")
	fmt.Println()
	fmt.Println("  [1] DeepSeek（推荐）")
	fmt.Println("  [2] OpenAI")
	fmt.Println()
	fmt.Print("  请输入数字 (1-2) [默认: 1]: ")

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	provider := "deepseek"
	defaultModel := "deepseek-v4-pro"
	defaultBaseURL := "https://api.deepseek.com"

	if input == "2" {
		provider = "openai"
		defaultModel = "gpt-4o"
		defaultBaseURL = "https://api.openai.com/v1"
	}

	fmt.Println()

	// 3. 输入 API Key
	fmt.Println("  Step 2/3 — API Key")
	fmt.Println()
	if provider == "deepseek" {
		fmt.Println("  前往 https://platform.deepseek.com/api_keys 创建 Key")
	} else {
		fmt.Println("  前往 https://platform.openai.com/api-keys 创建 Key")
	}
	fmt.Println()
	fmt.Print("  请输入 API Key: ")

	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	if apiKey == "" {
		fmt.Println()
		fmt.Println("  ⚠️  API Key 不能为空。你可以之后设置 LLM_API_KEY 环境变量再运行 waveloom setup。")
		os.Exit(1)
	}

	fmt.Println()

	// 4. 模型名称
	fmt.Printf("  Step 3/3 — 模型名称\n\n")
	if provider == "deepseek" {
		fmt.Println("  可用: deepseek-v4-pro（推荐，增强推理）")
		fmt.Println("        deepseek-v4-flash（快速推理）")
	} else {
		fmt.Println("  可用: gpt-4o（推荐）")
		fmt.Println("        gpt-4o-mini（快速）")
	}
	fmt.Println()
	fmt.Printf("  输入模型名 [默认: %s]: ", defaultModel)

	model, _ := reader.ReadString('\n')
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultModel
	}

	fmt.Println()

	// 5. 构建配置并写入
	settings := &llm.LLMSettings{
		APIKey:   apiKey,
		Provider: provider,
		Model:    model,
		BaseURL:  defaultBaseURL,
		Timeout:  "600s",
	}

	if provider == "deepseek" {
		settings.ExtraParams = map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": "max",
		}
	}

	if err := llm.WriteSettingsFile(configPath, settings); err != nil {
		fmt.Printf("  Error: 无法写入配置文件: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("  ╭─────────────────────────────────────────────╮")
	fmt.Println("  │         设置完成！                           │")
	fmt.Println("  ├─────────────────────────────────────────────┤")
	fmt.Printf("  │  配置已保存到 ~/.waveloom/settings.json      │\n")
	fmt.Printf("  │  Provider:  %-32s │\n", provider)
	fmt.Printf("  │  Model:     %-32s │\n", model)
	fmt.Println("  ╰─────────────────────────────────────────────╯")
	fmt.Println()
	fmt.Println("  现在可以运行 waveloom 进入交互模式了。")
}

// needsSetup 检查是否缺少 API Key 配置。
// 检查顺序：项目配置 → 全局配置 → 环境变量。
func needsSetup() bool {
	projectPath := filepath.Join(".waveloom", "settings.json")
	if s, _ := llm.LoadSettingsIfExists(projectPath); s != nil && s.APIKey != "" {
		return false
	}

	homeDir, _ := os.UserHomeDir()
	globalPath := filepath.Join(homeDir, ".waveloom", "settings.json")
	if s, _ := llm.LoadSettingsIfExists(globalPath); s != nil && s.APIKey != "" {
		return false
	}

	if os.Getenv("LLM_API_KEY") != "" {
		return false
	}

	return true
}
