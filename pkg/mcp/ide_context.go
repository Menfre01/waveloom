package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// IDEContextProvider — IDE MCP Server 能力注入
// ---------------------------------------------------------------------------

// IDEInfo 描述已连接的 IDE MCP Server。
type IDEInfo struct {
	ServerName string // MCP 配置中的名称
	IDEType    string // "idea" | "vscode"
	Title      string // ServerInfo.Title
	Client     *Client
}

// IDEContextProvider 收集所有 IDE MCP Server 的信息并生成 system prompt 片段。
// 静态能力引导注入 system prompt（一次性），动态上下文注入 user 消息（每轮，带 15 分钟缓存）。
type IDEContextProvider struct {
	mu    sync.RWMutex
	ides  map[string]*IDEInfo
	cache map[string]contextCacheEntry // key: serverName + "|" + cwd
}

type contextCacheEntry struct {
	result    string
	timestamp time.Time
}

// contextCacheTTL 动态上下文缓存时间，与环境探测一致。
const contextCacheTTL = 15 * time.Minute

// Register 记录一个已连接的 IDE MCP Server。
// 由 Manager.connectServer 成功后调用。
func (p *IDEContextProvider) Register(name, title string, client *Client) {
	ideType := detectIDEType(name, title)
	if ideType == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ides == nil {
		p.ides = make(map[string]*IDEInfo)
	}
	// 清除该 server 的缓存，确保连接重建后立即刷新
	for k := range p.cache {
		if strings.HasPrefix(k, name+"|") {
			delete(p.cache, k)
		}
	}
	p.ides[name] = &IDEInfo{
		ServerName: name,
		IDEType:    ideType,
		Title:      title,
		Client:     client,
	}
}

// Unregister 移除一个已断开的 IDE MCP Server。
func (p *IDEContextProvider) Unregister(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ides, name)
	for k := range p.cache {
		if strings.HasPrefix(k, name+"|") {
			delete(p.cache, k)
		}
	}
}

// FormatCapabilityGuide 返回应注入 system prompt 的静态 IDE 能力引导文本。
// 无 IDE 连接时返回空字符串。注入时机为启动时一次性注入，不破坏前缀缓存。
func (p *IDEContextProvider) FormatCapabilityGuide() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ides) == 0 {
		return ""
	}

	var parts []string
	for _, info := range p.ides {
		prefix := "mcp__" + sanitizeName(info.ServerName) + "__"
		parts = append(parts, formatCapabilityGuide(info, prefix))
	}
	return "\n\n## IDE Integration\n\n" + strings.Join(parts, "\n\n")
}

// QueryDynamicContext 返回应注入 user 消息的动态 IDE 上下文文本。
// 结果缓存 15 分钟，避免每轮重复 MCP 探测。
func (p *IDEContextProvider) QueryDynamicContext(ctx context.Context, cwd string) string {
	p.mu.RLock()
	ides := make([]*IDEInfo, 0, len(p.ides))
	for _, info := range p.ides {
		ides = append(ides, info)
	}
	p.mu.RUnlock()

	if len(ides) == 0 {
		return ""
	}

	var parts []string
	for _, info := range ides {
		cacheKey := info.ServerName + "|" + cwd

		p.mu.RLock()
		if entry, ok := p.cache[cacheKey]; ok && time.Since(entry.timestamp) < contextCacheTTL {
			p.mu.RUnlock()
			if entry.result != "" {
				parts = append(parts, entry.result)
			}
			continue
		}
		p.mu.RUnlock()

		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		result := queryOpenFiles(queryCtx, info, cwd)
		cancel()

		p.mu.Lock()
		if p.cache == nil {
			p.cache = make(map[string]contextCacheEntry)
		}
		p.cache[cacheKey] = contextCacheEntry{result: result, timestamp: time.Now()}
		p.mu.Unlock()

		if result != "" {
			parts = append(parts, result)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "## IDE Context\n\n" + strings.Join(parts, "\n\n")
}

// queryOpenFiles 查询单个 IDE Server 的当前打开文件列表。
// cwd 作为 projectPath 传入，确保多项目场景下查询正确项目。
func queryOpenFiles(ctx context.Context, info *IDEInfo, cwd string) string {
	if info.Client == nil {
		return ""
	}
	switch info.IDEType {
	case "idea":
		return queryIDEAOpenFiles(ctx, info, cwd)
	case "vscode":
		return queryVSCodeOpenFiles(ctx, info, cwd)
	}
	return ""
}

// ideaOpenFileTools 列出 IDEA MCP Server 上用于获取打开文件的候选工具名。
// 按优先级排列：命中第一个即返回。
var ideaOpenFileTools = []string{
	"get_all_open_file_paths",
	"get_open_files",
	"list_open_files",
}

// queryIDEAOpenFiles 调用 IDEA MCP Server 获取当前打开文件列表。
// cwd 作为 projectPath 传入，确保多项目场景下查询正确项目。
// 先通过 get_project_modules 校验 CWD 项目是否在 IDE 中打开，
// 避免跨项目上下文泄漏。校验失败返回空。
func queryIDEAOpenFiles(ctx context.Context, info *IDEInfo, cwd string) string {
	if !isProjectOpen(ctx, info.Client, cwd) {
		return ""
	}
	for _, toolName := range ideaOpenFileTools {
		if !hasTool(info.Client, toolName) {
			continue
		}
		result, err := info.Client.CallTool(ctx, toolName, map[string]any{
			"projectPath": cwd,
		})
		if err != nil || result.IsError {
			continue
		}
		text := extractTextContent(result)
		if text == "" {
			continue
		}
		return fmt.Sprintf("### %s — Open Files\n\n%s", info.ServerName, text)
	}
	return ""
}

// isProjectOpen 校验 CWD 项目是否在 IDE 中打开。
// 通过调用 get_project_modules 轻量验证，失败时返回 false。
// 若 IDE 不支持 get_project_modules 则降级允许继续。
func isProjectOpen(ctx context.Context, client *Client, cwd string) bool {
	if !hasTool(client, "get_project_modules") {
		return true
	}
	result, err := client.CallTool(ctx, "get_project_modules", map[string]any{
		"projectPath": cwd,
	})
	if err != nil || result.IsError {
		return false
	}
	return extractTextContent(result) != ""
}

// queryVSCodeOpenFiles 通过 VS Code MCP Server 获取上下文信息。
// VS Code MCP Server 无法列出所有打开文件（get_document_symbols 仅返回符号
// 不带文件路径，get_diagnostics 仅在无 active editor 时查全部）。
// 替代方案：通过 get_document_symbols 检测活跃编辑器 + workspace 校验。
func queryVSCodeOpenFiles(ctx context.Context, info *IDEInfo, cwd string) string {
	if !isVSCodeWorkspaceOpen(ctx, info.Client, cwd) {
		return ""
	}
	if hasTool(info.Client, "get_document_symbols") {
		result, err := info.Client.CallTool(ctx, "get_document_symbols", nil)
		if err == nil && !result.IsError && extractTextContent(result) != "" {
			return fmt.Sprintf("### %s\n\nActive editor with symbols detected.", info.ServerName)
		}
	}
	return ""
}

// isVSCodeWorkspaceOpen 校验 CWD 是否在 VS Code 的某个 workspace root 下。
// 通过 get_workspace_folders 获取所有 workspace root 并做前缀匹配。
func isVSCodeWorkspaceOpen(ctx context.Context, client *Client, cwd string) bool {
	if !hasTool(client, "get_workspace_folders") {
		return true
	}
	result, err := client.CallTool(ctx, "get_workspace_folders", nil)
	if err != nil || result.IsError {
		return false
	}
	text := extractTextContent(result)
	if text == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && (strings.HasPrefix(cwd, trimmed) || strings.HasPrefix(trimmed, cwd)) {
			return true
		}
	}
	return false
}

// hasTool 检查 Client 的已缓存工具列表中是否存在指定工具名。
func hasTool(client *Client, name string) bool {
	for _, td := range client.Tools() {
		if td.Name == name {
			return true
		}
	}
	return false
}

// extractTextContent 从 CallToolResult 中提取所有文本内容块。
func extractTextContent(result *CallToolResult) string {
	var parts []string
	for _, block := range result.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ---------------------------------------------------------------------------
// 内部函数
// ---------------------------------------------------------------------------

// detectIDEType 根据 server 名称或标题判断 IDE 类型。
func detectIDEType(name, title string) string {
	lower := strings.ToLower(name + " " + title)
	switch {
	case containsAny(lower, "idea", "intellij", "jetbrains"):
		return "idea"
	case containsAny(lower, "vscode", "vs code", "visual studio code"):
		return "vscode"
	}
	return ""
}

// containsAny 检查 s 中是否包含任意一个关键字。
func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// formatCapabilityGuide 根据 IDE 类型返回静态能力引导文本。
func formatCapabilityGuide(info *IDEInfo, prefix string) string {
	switch info.IDEType {
	case "idea":
		return fmt.Sprintf(`### %s — IntelliJ IDEA
You are connected to IntelliJ IDEA via MCP. The following IDE tools are available as alternatives to shell commands:

| Task | IDE tool | Shell alternative |
|------|----------|-------------------|
| File search | %s | find / grep |
| Text search | %s | grep |
| Symbol lookup | %s | read(outline=true) / grep |
| Symbol search | %s | grep |
| Build | %s | go build / make |
| Check errors | %s | go vet / cargo check |
| Rename | %s | sed / edit |
| Run | %s | shell commands |

Prefer IDE tools for semantic exploration (symbol lookup, references, refactoring) where the IDE's pre-built index avoids full disk scans. IDE tools do NOT replace ` + "`read`" + ` — you must still ` + "`read`" + ` a file before ` + "`edit`" + `/` + "`write`" + `. Prefer shell tools for batch processing, pipelines, and one-off searches in known locations.`,
			info.ServerName,
			prefix+"find_files_by_name_keyword",
			prefix+"search_text",
			prefix+"get_symbol_info",
			prefix+"search_symbol",
			prefix+"build_project",
			prefix+"get_file_problems",
			prefix+"rename_refactoring",
			prefix+"execute_run_configuration")

	case "vscode":
		return fmt.Sprintf(`### %s — VS Code
You are connected to VS Code via MCP. The following IDE tools are available as alternatives to shell commands:

| Task | IDE tool | Shell alternative |
|------|----------|-------------------|
| Find symbols | %s | grep |
| List files | %s | find / ls |
| Document symbols | %s | read(outline=true) / grep |
| Find references | %s | grep |
| Check errors | %s | go build / go vet |
| Rename | %s | sed / edit |
| Debug | %s / %s | print / run |
| Terminal output | %s | bash |

Prefer IDE tools for semantic exploration (symbol lookup, references, refactoring) where the IDE's pre-built index avoids full disk scans. IDE tools do NOT replace ` + "`read`" + ` — you must still ` + "`read`" + ` a file before ` + "`edit`" + `/` + "`write`" + `. Prefer shell tools for batch processing, pipelines, and one-off searches in known locations. Verify with %s that the CWD project is open before using these tools.`,
			info.ServerName,
			prefix+"get_workspace_symbols",
			prefix+"list_files",
			prefix+"get_document_symbols",
			prefix+"find_references",
			prefix+"get_diagnostics",
			prefix+"rename_symbol",
			prefix+"start_debugging",
			prefix+"add_breakpoint",
			prefix+"execute_in_terminal",
			prefix+"get_workspace_folders")
	}
	return ""
}
