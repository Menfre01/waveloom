package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Server 状态
// ---------------------------------------------------------------------------

type serverState int

const (
	stateNew      serverState = iota // 尚未创建
	stateStarting                    // 正在启动 + initialize 握手
	stateReady                       // 就绪，可处理请求
	stateCrashed                     // 进程退出（可自动恢复）
	stateClosed                      // 已关闭，不再恢复
)

func (s serverState) String() string {
	switch s {
	case stateNew:
		return "new"
	case stateStarting:
		return "starting"
	case stateReady:
		return "ready"
	case stateCrashed:
		return "crashed"
	case stateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Server 实例
// ---------------------------------------------------------------------------

// ServerInstance 管理单个 Language Server 进程及其 Client。
type ServerInstance struct {
	ext      string
	cfg      ServerConfig
	rootURI  string
	client   *Client
	state    serverState
	stateMu  sync.RWMutex
	lastUsed time.Time
	mu       sync.Mutex // 保护 Client 操作串行化

	// 诊断缓存
	diagMu   sync.RWMutex
	diagnostics map[DocumentURI][]Diagnostic // URI → 最新诊断列表
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// ManagerOption 配置 Manager 行为。
type ManagerOption func(*Manager)

// WithUserServers 设置用户自定义的 LSP Server 配置。
func WithUserServers(servers map[string]ServerConfig) ManagerOption {
	return func(m *Manager) {
		m.userServers = servers
	}
}

// WithIdleTimeout 设置空闲回收超时（默认 5 分钟）。
func WithIdleTimeout(d time.Duration) ManagerOption {
	return func(m *Manager) {
		m.idleTimeout = d
	}
}

// WithLogger 设置日志输出。
func WithLogger(logger *log.Logger) ManagerOption {
	return func(m *Manager) {
		m.logger = logger
	}
}

// Manager 管理所有 Language Server 进程的生命周期。
type Manager struct {
	mu          sync.RWMutex
	instances   map[string]*ServerInstance // ext → instance
	userServers map[string]ServerConfig
	idleTimeout time.Duration
	logger      *log.Logger

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager 创建 Server 管理器。
func NewManager(opts ...ManagerOption) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		instances:   make(map[string]*ServerInstance),
		userServers: make(map[string]ServerConfig),
		idleTimeout: 5 * time.Minute,
		logger:      log.New(io.Discard, "[lsp] ", log.LstdFlags),
		ctx:         ctx,
		cancel:      cancel,
	}
	for _, opt := range opts {
		opt(m)
	}
	// 启动空闲回收协程
	go m.reapLoop()
	return m
}

// GetOrCreate 根据文件路径获取或创建对应的 ServerInstance。
// 返回的 instance 保证处于 Ready 状态，否则返回 error。
func (m *Manager) GetOrCreate(filePath string) (*ServerInstance, error) {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return nil, fmt.Errorf("lsp: no file extension for %s", filePath)
	}

	cfg := LookupServer(filePath, m.userServers)
	if cfg == nil {
		return nil, fmt.Errorf("lsp: no LSP server configured for %s", ext)
	}

	m.mu.RLock()
	inst, exists := m.instances[ext]
	m.mu.RUnlock()

	if exists {
		inst.stateMu.RLock()
		s := inst.state
		inst.stateMu.RUnlock()

		if s == stateReady {
			inst.lastUsed = time.Now()
			return inst, nil
		}
		if s == stateCrashed {
			// 删除旧 instance，下面重建
			m.mu.Lock()
			delete(m.instances, ext)
			m.mu.Unlock()
		}
	}

	// 创建新 instance
	m.mu.Lock()
	// 双重检查
	if inst, exists = m.instances[ext]; exists && inst.state == stateReady {
		inst.lastUsed = time.Now()
		m.mu.Unlock()
		return inst, nil
	}

	inst = &ServerInstance{
		ext:         ext,
		cfg:         *cfg,
		rootURI:     string(PathToURI(filepath.Dir(filePath))),
		state:       stateStarting,
		diagnostics: make(map[DocumentURI][]Diagnostic),
		lastUsed:    time.Now(),
	}
	m.instances[ext] = inst
	m.mu.Unlock()

	if err := m.startInstance(inst); err != nil {
		inst.stateMu.Lock()
		inst.state = stateCrashed
		inst.stateMu.Unlock()
		return nil, fmt.Errorf("lsp: start %s: %w", cfg.Command, err)
	}

	return inst, nil
}

// Diagnostics 返回指定文件的缓存诊断结果。
func (m *Manager) Diagnostics(uri DocumentURI) []Diagnostic {
	ext := filepath.Ext(string(uri))
	m.mu.RLock()
	inst := m.instances[ext]
	m.mu.RUnlock()

	if inst == nil {
		return nil
	}

	inst.diagMu.RLock()
	defer inst.diagMu.RUnlock()

	diags := inst.diagnostics[uri]
	result := make([]Diagnostic, len(diags))
	copy(result, diags)
	return result
}

// Close 关闭所有 Server 进程并停止回收协程。
func (m *Manager) Close() {
	m.cancel()

	m.mu.Lock()
	instances := m.instances
	m.instances = make(map[string]*ServerInstance)
	m.mu.Unlock()

	for _, inst := range instances {
		inst.stateMu.Lock()
		if inst.state == stateReady {
			inst.state = stateClosed
			inst.client.Close()
		}
		inst.stateMu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

// startInstance 启动 LSP Server 并完成初始化。
func (m *Manager) startInstance(inst *ServerInstance) error {
	// 检查命令是否存在
	if _, err := exec.LookPath(inst.cfg.Command); err != nil {
		return fmt.Errorf("%s not found in PATH", inst.cfg.Command)
	}

	client, err := NewClient(inst.cfg.Command, inst.cfg.Args, inst.rootURI)
	if err != nil {
		return err
	}

	// 注册诊断通知处理器
	client.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return
		}
		inst.diagMu.Lock()
		inst.diagnostics[params.URI] = params.Diagnostics
		inst.diagMu.Unlock()
	})

	inst.mu.Lock()
	inst.client = client
	inst.state = stateReady
	inst.mu.Unlock()

	m.logger.Printf("server %s ready for %s", inst.cfg.Command, inst.ext)
	return nil
}

// call 在 instance 上执行一个 LSP 请求，自动处理 didOpen。
func (m *Manager) call(inst *ServerInstance, method string, params, result any) error {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.state != stateReady {
		return fmt.Errorf("lsp: server not ready (state=%s)", inst.state)
	}

	inst.lastUsed = time.Now()
	return inst.client.Call(method, params, result)
}

// notify 在 instance 上发送一个 LSP 通知。
func (m *Manager) notify(inst *ServerInstance, method string, params any) error {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.state != stateReady {
		return fmt.Errorf("lsp: server not ready (state=%s)", inst.state)
	}

	inst.lastUsed = time.Now()
	return inst.client.Notify(method, params)
}

// SyncFile 向 LSP Server 同步文件内容（didOpen）。
func (m *Manager) SyncFile(inst *ServerInstance, filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("lsp: read file %s: %w", filePath, err)
	}

	uri := PathToURI(filePath)
	ext := filepath.Ext(filePath)
	langID := extToLanguageID(ext)

	return m.notify(inst, "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: langID,
			Version:    1,
			Text:       string(content),
		},
	})
}

// Call 在指定 instance 上执行一个 LSP 请求。
func (m *Manager) Call(inst *ServerInstance, method string, params, result any) error {
	return m.call(inst, method, params, result)
}

// reapLoop 定期扫描并回收空闲 Server。
func (m *Manager) reapLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reap()
		}
	}
}

func (m *Manager) reap() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for ext, inst := range m.instances {
		inst.stateMu.RLock()
		state := inst.state
		inst.stateMu.RUnlock()

		if state != stateReady {
			continue
		}

		if time.Since(inst.lastUsed) > m.idleTimeout {
			m.logger.Printf("reaping idle server for %s", ext)
			inst.stateMu.Lock()
			inst.state = stateClosed
			inst.stateMu.Unlock()
			inst.client.Close()
			delete(m.instances, ext)
		}
	}
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

// PathToURI 将文件路径转换为 LSP URI（file:// 格式）。
func PathToURI(path string) DocumentURI {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return DocumentURI("file://" + filepath.ToSlash(abs))
}

// extToLanguageID 将文件扩展名映射到 LSP LanguageID。
func extToLanguageID(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".mod":
		return "go.mod"
	case ".sum":
		return "go.sum"
	case ".work":
		return "go.work"
	case ".rs":
		return "rust"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py", ".pyi":
		return "python"
	default:
		return ext[1:] // 去掉点
	}
}
