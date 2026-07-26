package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	diagMu      sync.RWMutex
	diagnostics map[DocumentURI][]Diagnostic // URI → 最新诊断列表

	// didClose LRU: 追踪已打开的文件，超过上限时发送 didClose
	openDocs    []DocumentURI // 有序列表，最近使用的在尾部
	openDocsCap int           // 上限（默认 20）

	// Monotonic document version counter (per LSP spec)
	docVersion atomic.Uint32

	// Per-URI channels for synchronous diagnostic wait after SyncFile.
	diagWaiters   map[DocumentURI]chan struct{}
	diagWaitersMu sync.Mutex
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

// WithIdleTimeout 设置空闲回收超时 (默认 5 分钟)。
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
	probeMap    map[string]bool   // binary → installed（环境探测结果）
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
		probeMap:    make(map[string]bool),
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

// SetProbeMap 注入环境探测结果，决定哪些 LSP server 可用。
func (m *Manager) SetProbeMap(probes map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probeMap = probes
}

// GetOrCreate 根据文件扩展名获取或创建对应的 ServerInstance。
// probeMap[binary]==false 时返回 nil, nil（静默跳过）。
func (m *Manager) GetOrCreate(filePath string) (*ServerInstance, error) {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return nil, nil
	}

	cfg := LookupServer(filePath, m.userServers)
	if cfg == nil {
		return nil, nil
	}

	// 检查探针结果：未安装则静默跳过
	m.mu.RLock()
	installed := m.probeMap[cfg.Command]
	m.mu.RUnlock()
	if !installed {
		// 用户显式配置的 server 跳过探针检查
		if _, isUser := m.userServers[ext]; !isUser {
			return nil, nil
		}
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
		// Hold write lock across state re-check + delete to prevent
		// another goroutine from replacing the instance in between.
		if s == stateCrashed {
			m.mu.Lock()
			// Re-check state under write lock
			inst.stateMu.RLock()
			stillCrashed := inst.state == stateCrashed
			inst.stateMu.RUnlock()
			if stillCrashed {
				delete(m.instances, ext)
			}
			m.mu.Unlock()
		}
	}

	// 创建新 instance
	m.mu.Lock()
	// 双重检查: any non-crashed instance means another goroutine
	// is creating or already created it — wait for or reuse it.
	if inst, exists = m.instances[ext]; exists {
		inst.stateMu.RLock()
		s := inst.state
		inst.stateMu.RUnlock()
		switch s {
		case stateReady:
			inst.lastUsed = time.Now()
			m.mu.Unlock()
			return inst, nil
		case stateStarting:
			// Another goroutine is creating it — wait briefly
			m.mu.Unlock()
			for i := 0; i < 50; i++ {
				time.Sleep(100 * time.Millisecond)
				inst.stateMu.RLock()
				s2 := inst.state
				inst.stateMu.RUnlock()
				if s2 == stateReady {
					inst.lastUsed = time.Now()
					return inst, nil
				}
			}
			return nil, fmt.Errorf("lsp: %s server start timed out", inst.cfg.Command)
		}
		// stateCrashed or stateClosed: fall through to create/replace
	}

	rootURI := string(PathToURI(findProjectRoot(filePath)))
	stderrPath := lspLogPath(ext)

	inst = &ServerInstance{
		ext:         ext,
		cfg:         *cfg,
		rootURI:     rootURI,
		state:       stateStarting,
		diagnostics: make(map[DocumentURI][]Diagnostic),
		openDocs:    make([]DocumentURI, 0),
		docVersion: atomic.Uint32{},
		diagWaiters: make(map[DocumentURI]chan struct{}),
		openDocsCap: 20,
		lastUsed:    time.Now(),
	}
	m.instances[ext] = inst
	m.mu.Unlock()

	inst.docVersion.Store(1)

	if err := m.startInstance(inst, stderrPath); err != nil {
		inst.stateMu.Lock()
		inst.state = stateCrashed
		inst.stateMu.Unlock()
		return nil, err
	}

	return inst, nil
}

// SyncFile 通知 LSP server 文件内容已变更并等待诊断结果。
// 首次调用→ didOpen + wait；后续 → didChange (全量同步) + wait。
// 超过 openDocsCap 时 evict 最早的文件 → didClose。
// Waits up to syncDiagTimeout for publishDiagnostics notification.
func (m *Manager) SyncFile(inst *ServerInstance, filePath string) error {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	inst.stateMu.RLock()
	ready := inst.state == stateReady
	inst.stateMu.RUnlock()
	if !ready || inst.client == nil {
		return nil
	}

	uri := PathToURI(filePath)

	// 读取文件内容
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// Register a waiter for publishDiagnostics before sending the notification
	diagCh := make(chan struct{})
	inst.diagWaitersMu.Lock()
	inst.diagWaiters[uri] = diagCh
	inst.diagWaitersMu.Unlock()

	// 检查是否已打开
	isOpen := false
	for i, doc := range inst.openDocs {
		if doc == uri {
			// 移到尾部（最近使用）
			inst.openDocs = append(inst.openDocs[:i], inst.openDocs[i+1:]...)
			inst.openDocs = append(inst.openDocs, uri)
			isOpen = true
			break
		}
	}

	var notifyErr error
	if isOpen {
		// didChange (全量同步)
		version := int(inst.docVersion.Add(1))
		notifyErr = inst.client.Notify("textDocument/didChange", DidChangeTextDocumentParams{
			TextDocument: VersionedTextDocumentIdentifier{
				URI:     uri,
				Version: version,
			},
			ContentChanges: []TextDocumentContentChangeEvent{
				{Text: string(content)},
			},
		})
	} else {
		// didOpen
		langID := languageID(filePath)
		notifyErr = inst.client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
			TextDocument: TextDocumentItem{
				URI:        uri,
				LanguageID: langID,
				Version:    1,
				Text:       string(content),
			},
		})
		if notifyErr == nil {
			// LRU eviction
			inst.openDocs = append(inst.openDocs, uri)
			for len(inst.openDocs) > inst.openDocsCap {
				evict := inst.openDocs[0]
				inst.openDocs = inst.openDocs[1:]
				_ = inst.client.Notify("textDocument/didClose", DidCloseTextDocumentParams{
					TextDocument: TextDocumentIdentifier{URI: evict},
				})
			}
		}
	}

	if notifyErr != nil {
		// Clean up waiter on error
		inst.diagWaitersMu.Lock()
		delete(inst.diagWaiters, uri)
		inst.diagWaitersMu.Unlock()
		return notifyErr
	}

	// Wait for publishDiagnostics with timeout
	timer := time.NewTimer(syncDiagTimeout)
	select {
	case <-diagCh:
		timer.Stop()
	case <-timer.C:
		inst.diagWaitersMu.Lock()
		delete(inst.diagWaiters, uri)
		inst.diagWaitersMu.Unlock()
	}

	return nil
}

// Diagnostics 返回指定文件的缓存诊断结果 (可能为空，表示尚未收到推送)。
func (m *Manager) Diagnostics(uri DocumentURI) []Diagnostic {
	ext := filepath.Ext(URIToPath(uri))

	m.mu.RLock()
	inst := m.instances[ext]
	m.mu.RUnlock()

	if inst == nil {
		return nil
	}

	inst.diagMu.RLock()
	defer inst.diagMu.RUnlock()

	diags, ok := inst.diagnostics[uri]
	if !ok {
		return nil
	}
	// 返回副本
	copied := make([]Diagnostic, len(diags))
	copy(copied, diags)
	return copied
}

// Shutdown 关闭所有 server 进程。
func (m *Manager) Shutdown() {
	m.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for ext, inst := range m.instances {
		inst.stateMu.Lock()
		if inst.state == stateClosed {
			inst.stateMu.Unlock()
			continue
		}
		inst.state = stateClosed
		inst.stateMu.Unlock()

		if inst.client != nil {
			_ = inst.client.Close()
			m.logger.Printf("shutdown %s server for %s", inst.cfg.Command, ext)
		}
	}
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

// startInstance 启动 LSP server 进程并注册 publishDiagnostics 通知处理器。
func (m *Manager) startInstance(inst *ServerInstance, stderrPath string) error {
	args := make([]string, len(inst.cfg.Args))
	copy(args, inst.cfg.Args)

	client, err := NewClient(inst.cfg.Command, args, inst.rootURI, stderrPath)
	if err != nil {
		return fmt.Errorf("lsp: start %s: %w", inst.cfg.Command, err)
	}

	// 注册 publishDiagnostics 通知处理器
	client.OnNotification("textDocument/publishDiagnostics", func(raw json.RawMessage) {
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(raw, &params); err != nil {
			m.logger.Printf("failed to unmarshal publishDiagnostics: %v", err)
			return
		}
		inst.diagMu.Lock()
		inst.diagnostics[params.URI] = params.Diagnostics
		inst.diagMu.Unlock()

		// Notify any synchronous waiter
		inst.diagWaitersMu.Lock()
		if ch, ok := inst.diagWaiters[params.URI]; ok {
			delete(inst.diagWaiters, params.URI)
			close(ch)
		}
		inst.diagWaitersMu.Unlock()
	})

	inst.mu.Lock()
	inst.client = client
	inst.mu.Unlock()

	inst.stateMu.Lock()
	inst.state = stateReady
	inst.stateMu.Unlock()

	m.logger.Printf("started %s server for %s", inst.cfg.Command, inst.ext)
	return nil
}

// reapLoop 定期回收空闲超过 idleTimeout 的 server 实例。
func (m *Manager) reapLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.reapIdle()
		case <-m.ctx.Done():
			return
		}
	}
}

// reapIdle 关闭空闲超过 idleTimeout 的 server 实例。
func (m *Manager) reapIdle() {
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	for ext, inst := range m.instances {
		inst.stateMu.RLock()
		ready := inst.state == stateReady
		idle := now.Sub(inst.lastUsed) > m.idleTimeout
		inst.stateMu.RUnlock()

		if ready && idle {
			inst.stateMu.Lock()
			inst.state = stateClosed
			inst.stateMu.Unlock()

			if inst.client != nil {
				_ = inst.client.Close()
				m.logger.Printf("reaped idle %s server for %s (idle %v)", inst.cfg.Command, ext, m.idleTimeout)
			}
			delete(m.instances, ext)
		}
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// syncDiagTimeout is the maximum time SyncFile waits for publishDiagnostics
// after sending didOpen/didChange. If exceeded, the caller proceeds with
// potentially stale diagnostics (eventual consistency).
const syncDiagTimeout = 2 * time.Second

// languageID 根据文件扩展名返回 LSP language ID。
func languageID(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".h":
		return "c"
	case ".hpp":
		return "cpp"
	case ".py":
		return "python"
	default:
		return ext[1:] // 去掉点前缀
	}
}

// findProjectRoot walks up from filePath to find the nearest project root
// (directory containing go.mod, Cargo.toml, package.json, etc.).
// Falls back to the file's parent directory if no marker is found.
func findProjectRoot(filePath string) string {
	markers := []string{"go.mod", "Cargo.toml", "package.json", "tsconfig.json",
		"pyproject.toml", "setup.py", "CMakeLists.txt", ".git"}
	dir := filepath.Dir(filePath)
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return filepath.Dir(filePath)
}

// lspLogPath 返回 LSP server stderr 日志文件路径。
func lspLogPath(ext string) string {
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		return ""
	}
	logsDir := filepath.Join(homeDir, ".waveloom", "logs")
	_ = os.MkdirAll(logsDir, 0o755)
	suffix := ext
	if len(ext) > 0 && ext[0] == '.' {
		suffix = ext[1:]
	}
	return filepath.Join(logsDir, fmt.Sprintf("lsp-%s.log", suffix))
}
