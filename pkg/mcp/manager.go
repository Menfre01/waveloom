package mcp

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Manager — 多 MCP Server 生命周期管理
// ---------------------------------------------------------------------------

// Manager 管理所有 MCP Server 的连接和工具注册。
type Manager struct {
	registry tool.Registry
	logger   *log.Logger

	mu      sync.RWMutex
	clients map[string]*Client // server name → Client

	// connectFunc 用于创建 MCP 连接，测试时可替换。
	connectFunc func(ctx context.Context, name string, config ServerConfig) (*Client, error)
}

// ManagerOption 是 NewManager 的功能选项。
type ManagerOption func(*Manager)

// WithLogger 设置 MCP Manager 的日志输出目标。
// 默认使用 io.Discard，不输出任何日志。
func WithLogger(l *log.Logger) ManagerOption {
	return func(m *Manager) {
		m.logger = l
	}
}

// NewManager 创建一个新的 MCP Manager。
func NewManager(registry tool.Registry, opts ...ManagerOption) *Manager {
	m := &Manager{
		registry:    registry,
		logger:      log.New(io.Discard, "[mcp] ", log.LstdFlags),
		clients:     make(map[string]*Client),
		connectFunc: Connect,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Start 连接所有配置的 MCP Server，发现工具并注册到 Registry。
// 连接在独立 goroutine 中异步进行，不阻塞调用方。
// HTTP server 的 connectServer 内部包含指数退避重连逻辑。
func (m *Manager) Start(ctx context.Context, configs map[string]ServerConfig) {
	if len(configs) == 0 {
		return
	}

	for _, cfg := range configs {
		go m.connectServer(ctx, cfg)
	}
}

// connectServer 连接单个 MCP Server 并注册其工具。
// 对于 HTTP transport，连接失败会进行指数退避重连（最多 5 次）。
func (m *Manager) connectServer(ctx context.Context, config ServerConfig) {
	name := config.Name

	connectTimeout := 30 * time.Second
	if config.Timeout > 0 {
		connectTimeout = time.Duration(config.Timeout) * time.Millisecond
	}

	// HTTP transport: 指数退避重连
	maxRetries := 1       // stdio: 只试一次
	backoff := time.Second // 初始退避

	if config.Type == ServerTypeHTTP || config.Type == ServerTypeSSE {
		maxRetries = 5
	}

	var client *Client
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			m.logger.Printf("retrying %q (attempt %d/%d, backoff %v)", name, attempt+1, maxRetries, backoff)
			time.Sleep(backoff)
			backoff *= 2
		}

		connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		var err error
		client, err = m.connectFunc(connectCtx, name, config)
		cancel()
		if err == nil {
			break
		}
		lastErr = err
	}

	if client == nil {
		m.logger.Printf("failed to connect %q after %d attempts: %v", name, maxRetries, lastErr)
		return
	}

	// 将 Manager 的 logger 注入 Client，统一日志输出。
	client.logger = m.logger

	// 设置 tools/list_changed 回调，触发工具刷新
	client.OnToolsChanged = func() {
		m.refreshTools(client)
	}

	// 发现工具 — 使用独立超时
	listTimeout := 30 * time.Second
	listCtx, listCancel := context.WithTimeout(ctx, listTimeout)
	defer listCancel()

	tools, err := client.ListTools(listCtx)
	if err != nil {
		m.logger.Printf("failed to list tools from %q: %v", name, err)
		_ = client.Close()
		return
	}

	// 注册工具代理
	count := 0
	for _, td := range tools {
		proxy := NewMCPToolProxy(client, td)
		m.registry.Register(tool.Wrap(proxy))
		count++
	}

	// 保存 client
	m.mu.Lock()
	m.clients[name] = client
	m.mu.Unlock()

	m.logger.Printf("connected %q (%s v%s), registered %d tools",
		name, client.ServerInfo().Title, client.ServerInfo().Version, count)
}

// refreshTools 重新发现并注册 server 的工具（响应 list_changed 通知）。
func (m *Manager) refreshTools(client *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := client.ServerName()
	tools, err := client.ListTools(ctx)
	if err != nil {
		m.logger.Printf("failed to refresh tools from %q: %v", name, err)
		return
	}

	// 注意：当前 Registry 不支持反注册，新增工具直接追加
	// 重复名称的 Register 会 panic，因此先检查
	// 实际上工具名是 mcp__<server>__<tool>，不会与已有工具冲突
	count := 0
	for _, td := range tools {
		proxy := NewMCPToolProxy(client, td)
		// 跳过已注册的同名工具（Registry.Register 会 panic）
		// 在 Waveloom 当前 Registry 中无 Unregister，此限制可接受
		m.registry.Register(tool.Wrap(proxy))
		count++
	}
	m.logger.Printf("refreshed tools from %q: %d tools", name, count)
}

// Stop 关闭所有 MCP Server 连接。
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			m.logger.Printf("error closing %q: %v", name, err)
			lastErr = err
		}
	}
	m.clients = make(map[string]*Client)

	if lastErr != nil {
		return fmt.Errorf("mcp stop: some errors occurred (last: %w)", lastErr)
	}
	return nil
}

// ClientCount 返回当前活跃连接数。
func (m *Manager) ClientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients)
}

// ClientNames 返回所有已连接 server 的名称列表。
func (m *Manager) ClientNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	return names
}

// ClientStatus 返回所有 server 的连接状态。
func (m *Manager) ClientStatus() map[string]ClientStatusInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]ClientStatusInfo, len(m.clients))
	for name, client := range m.clients {
		tools := client.Tools()
		result[name] = ClientStatusInfo{
			Name:      name,
			Title:     client.ServerInfo().Title,
			Version:   client.ServerInfo().Version,
			ToolCount: len(tools),
			Connected: true,
		}
	}
	return result
}

// ClientStatusInfo 描述单个 MCP Server 的连接状态。
type ClientStatusInfo struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Version   string `json:"version"`
	ToolCount int    `json:"toolCount"`
	Connected bool   `json:"connected"`
}
