// Package environment 在 Waveloom 启动时探测宿主编译/运行时工具链可用性，
// 将结果注入 System Prompt，让 LLM 在尝试调用工具前就了解系统环境，
// 避免因命令缺失导致的反复试错探测循环。
package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// 类型
// ---------------------------------------------------------------------------

// ProbeResult 表示单个探针命令的执行结果。
type ProbeResult struct {
	Command string // 原始命令字符串，如 "go version"
	Binary  string // 命令的第一个词（仅用于展示），如 "go"
	Output  string // 成功时：stdout 首行（已 trim）
	Found   bool   // 命令是否存在于 PATH 且能在超时内执行成功
	Error   string // 失败原因（command_not_found / timeout / non-zero exit）
}

// ---------------------------------------------------------------------------
// 默认探针列表
// ---------------------------------------------------------------------------

// DefaultProbes 返回跨平台的默认探针命令列表。
// 所有命令均设计为"输出版本号后立即退出"，超时短（2s），
// 确保启动延迟控制在可接受范围内。
func DefaultProbes() []string {
	return []string{
		// Go 生态
		"go version",
		// Python 生态
		"python3 --version",
		"python --version",
		"pip3 --version",
		// Node.js 生态
		"node --version",
		"npm --version",
		// Rust 生态
		"rustc --version",
		"cargo --version",
		// C/C++
		"gcc --version",
		"g++ --version",
		"cmake --version",
		// JVM
		"java -version",
		"mvn --version",
		// Ruby
		"ruby --version",
		// PHP
		"php --version",
		// .NET
		"dotnet --version",
		// 版本控制 / 构建
		"git version",
		"make --version",
		// 容器
		"docker --version",
		// 搜索增强（rg 比 grep 更快，LLM 在探测到可用时优先使用）
		"rg --version",
	}
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

// ProbeTimeout 是单个探针命令的最大等待时间。
const ProbeTimeout = 2 * time.Second

// RunProbes 并行执行给定的探针命令列表，返回按 binary 名排序的结果切片。
// 每个命令超时 ProbeTimeout 秒，失败结果也返回（Found=false）。
//
// 为避免阻塞主线程过久，所有命令并行执行，最坏情况延迟 = ProbeTimeout。
func RunProbes(ctx context.Context, commands []string) []ProbeResult {
	results := make([]ProbeResult, len(commands))
	var wg sync.WaitGroup

	for i, cmd := range commands {
		wg.Add(1)
		go func(idx int, command string) {
			defer wg.Done()
			results[idx] = runOne(ctx, command)
		}(i, cmd)
	}

	wg.Wait()

	// 按 binary 名排序：Found 的排在前面，同状态按字母序
	sort.Slice(results, func(i, j int) bool {
		if results[i].Found != results[j].Found {
			return results[i].Found
		}
		return results[i].Binary < results[j].Binary
	})

	return results
}

// runOne 执行单个探针命令，返回结果。
func runOne(ctx context.Context, command string) ProbeResult {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ProbeResult{Command: command, Binary: command, Found: false, Error: "empty command"}
	}

	binary := parts[0]
	res := ProbeResult{Command: command, Binary: binary}

	// 先检查命令是否在 PATH 中
	if _, err := exec.LookPath(binary); err != nil {
		res.Found = false
		res.Error = "not found"
		return res
	}

	// 带超时执行
	cmdCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, binary, parts[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// java -version 输出到 stderr 是正常行为
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			res.Found = false
			res.Error = "timeout"
		} else {
			// 非零退出码但仍有输出的情况（如 java -version → exit 0 但输出在 stderr 已处理）
			if output != "" {
				// 有输出即视为成功
				res.Found = true
				res.Output = firstLine(output)
				return res
			}
			res.Found = false
			res.Error = fmt.Sprintf("exit %s", err.Error())
		}
		return res
	}

	res.Found = true
	res.Output = firstLine(output)
	return res
}

// firstLine 取字符串的首行（去除 \r）。
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimRight(s[:idx], "\r")
	}
	return s
}

// ---------------------------------------------------------------------------
// 格式化 — 生成 System Prompt ## Environment 节
// ---------------------------------------------------------------------------

// FormatEnvironmentSection 将探测结果格式化为 System Prompt 的 ## Environment 节。
//
// toolOverrides 来自 settings.json 的 environment.tools 配置，key=命令名 value=完整路径。
// 已配置路径的工具将在 "Configured tools" 下展示，即使探针未在 PATH 中检测到。
func FormatEnvironmentSection(results []ProbeResult, osName, shellPath string, toolOverrides map[string]string) string {
	if len(results) == 0 && len(toolOverrides) == 0 {
		return ""
	}

	var (
		found    []ProbeResult
		notFound []ProbeResult
	)

	// 已配置路径的工具从探针结果中移除，单独归入 configured 展示
	configured := make(map[string]string)
	for k, v := range toolOverrides {
		configured[k] = v
	}

	for _, r := range results {
		if _, hasOverride := configured[r.Binary]; hasOverride {
			continue
		}
		if r.Found {
			found = append(found, r)
		} else {
			notFound = append(notFound, r)
		}
	}

	var b bytes.Buffer
	b.WriteString("\n\n## Environment\n\n")

	if osName != "" {
		fmt.Fprintf(&b, "- OS: %s\n", osName)
	}
	if shellPath != "" {
		fmt.Fprintf(&b, "- Shell: %s\n", shellPath)
	}

	b.WriteString("\nThe following tools were detected at startup. Do NOT attempt to run tools\n")
	b.WriteString("listed under \"Not found\" — use the higher-level built-in tools (read_file,\n")
	b.WriteString("grep, search_file, ls, etc.) or ask the user to provide the tool path.\n")
	b.WriteString("If a required tool is missing, suggest the OS-appropriate install command:\n")
	b.WriteString("  macOS:  brew install <tool>\n")
	b.WriteString("  Ubuntu: sudo apt install <tool>\n")
	b.WriteString("  Windows: winget install <tool>\n")

	if len(configured) > 0 {
		b.WriteString("\nConfigured tools (use the full path when invoking):\n")
		names := make([]string, 0, len(configured))
		for k := range configured {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "  %-10s %s\n", name, configured[name])
		}
	}

	if len(found) > 0 {
		b.WriteString("\nAvailable tools:\n")
		for _, r := range found {
			fmt.Fprintf(&b, "  %-10s %s\n", r.Binary, r.Output)
		}
	}

	if len(notFound) > 0 {
		names := make([]string, len(notFound))
		for i, r := range notFound {
			names[i] = r.Binary
		}
		fmt.Fprintf(&b, "\nNot found: %s\n", strings.Join(names, ", "))
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// settings.json 加载
// ---------------------------------------------------------------------------

// envSettings 是 settings.json 中 environment 块的结构。
type envSettings struct {
	Environment *envConfig `json:"environment"`
}

type envConfig struct {
	Tools map[string]string `json:"tools"`
}

// LoadToolOverrides 从 settings.json 文件加载用户配置的工具路径覆盖。
// 返回 map[命令名]完整路径。文件不存在或缺少 environment.tools 时返回空 map。
func LoadToolOverrides(settingsPath string) map[string]string {
	if settingsPath == "" {
		return nil
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil
	}
	var s envSettings
	if err := json.Unmarshal(data, &s); err != nil || s.Environment == nil {
		return nil
	}
	return s.Environment.Tools
}
