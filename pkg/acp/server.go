package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/mcp"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Server — ACP Server 主循环
// ---------------------------------------------------------------------------

// Server 是 ACP Agent 的核心：从 stdin 读取 JSON-RPC 请求，
// 分发到对应 handler，通过 stdout 写入响应和通知。
type Server struct {
	transport *StdioTransport

	// session 注册表
	mu       sync.RWMutex
	sessions map[string]*SessionState

	// 共享资源(构造时注入)
	llmClient    llm.Client
	toolRegistry tool.Registry
	systemPrompt string
	buildVersion string
	cwd          string
	maxTurns     int
	guard        permission.Guard            // ACP 入口注入(已启用 autoAllow 二元决策);nil → 每 session fallback 裸 Guard
	sandboxMgr   *sandbox.SandboxManager     // ACP 入口注入(自动激活);nil → Shell 不包装
	sessionDir   string                      // session 持久化目录(空 → 不落盘)
	mcpConnect   mcpConnectFunc             // MCP 连接函数(测试注入;nil → mcp.Connect)
	contextLimit int                        // 上下文窗口容量 token(usage_update size;0 = 未知)
	compactionConfig compaction.CompactionConfig // 上下文压缩配置(settings + flag 解析)
	summarizer       compaction.Summarizer        // Tier 3 摘要器(可能为 nil → Tier 3 跳过)

	// 状态
	initialized bool            // initialize 是否已完成
	wg          sync.WaitGroup  // 跟踪活跃的 prompt goroutine
}

// SessionState 保存单个 ACP session 的完整状态。
type SessionState struct {
	ID    string
	CM    *session.ContextManager
	Loop  *agentloop.Loop
	Guard permission.Guard
	CWD   string
	// MCP 支持(session/new 的 mcpServers):child registry 隔离 MCP 工具,
	// MCPManager 管理连接生命周期(session close/delete 时 Stop)。
	Registry    tool.Registry
	MCPManager  *mcp.Manager

	promptMu sync.Mutex          // 同一 session 内串行
	cancelMu sync.Mutex          // 保护 cancelFn
	cancelFn context.CancelFunc  // 取消当前 prompt 的 ctx
	promptStarted bool           // prompt goroutine 已启动(区分"启动窗口"与"空闲期")
	cancelPending bool           // cancel 在 prompt 启动前到达(executePrompt 设置 cancelFn 后消费)
}

// ServerConfig 是创建 Server 的配置。
type ServerConfig struct {
	LLMClient    llm.Client
	ToolRegistry tool.Registry
	SystemPrompt string
	BuildVersion string
	CWD          string
	MaxTurns     int
	Guard        permission.Guard            // 可选:ACP 入口注入(已 EnableAutoAllow)
	SandboxMgr   *sandbox.SandboxManager     // 可选:ACP 入口注入(自动激活)
	SessionDir   string                      // session 持久化目录(空 → 不落盘)
	ContextLimit int                         // 上下文窗口容量 token(usage_update size;0 = 未知)
	CompactionConfig compaction.CompactionConfig // 上下文压缩配置
	Summarizer       compaction.Summarizer        // Tier 3 摘要器(可 nil)
}

// mcpConnectFunc 与 mcp.Manager.connectFunc 同构,供测试注入 fake 连接。
type mcpConnectFunc func(ctx context.Context, name string, config mcp.ServerConfig) (*mcp.Client, error)

// NewServer 创建 ACP Server 实例。
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		transport:    NewStdioTransport(),
		sessions:     make(map[string]*SessionState),
		llmClient:    cfg.LLMClient,
		toolRegistry: cfg.ToolRegistry,
		systemPrompt: cfg.SystemPrompt,
		buildVersion: cfg.BuildVersion,
		cwd:          cfg.CWD,
		maxTurns:     cfg.MaxTurns,
		guard:        cfg.Guard,
		sandboxMgr:   cfg.SandboxMgr,
		sessionDir:   cfg.SessionDir,
		mcpConnect:   mcp.Connect,
		contextLimit: cfg.ContextLimit,
		compactionConfig: cfg.CompactionConfig,
		summarizer:       cfg.Summarizer,
	}
}

// Run 启动 Server 主循环，阻塞直到 stdin 关闭或收到终止信号。
func (s *Server) Run() error {
	// 信号处理:优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听终止信号
	go func() {
		select {
		case sig := <-sigCh:
			slog.Info("acp: received signal, shutting down", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	// 主读取循环
	for {
		// 检查是否收到终止信号
		if ctx.Err() != nil {
			return s.shutdown()
		}

		raw, err := s.transport.Receive()
		if err != nil {
			if errors.Is(err, ErrLineTooLong) {
				// 超长行可恢复:回 parse error 后继续(DoS 防护,不关闭连接)
				slog.Warn("acp: message line too long, skipping")
				s.sendErrorResponse(nil, ErrParse, "message line too long")
				continue
			}
			if err == io.EOF {
				slog.Info("acp: stdin closed, shutting down")
				return s.shutdown()
			}
			slog.Error("acp: transport receive error", "err", err)
			return s.shutdown()
		}

		// 解析 JSON-RPC 请求
		var req JSONRPCRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			slog.Error("acp: failed to parse request", "err", err)
			// 发送 Parse error（没有有效 ID 时用 null）
			s.sendErrorResponse(nil, ErrParse, "parse error: "+err.Error())
			continue
		}

		if req.JSONRPC != "2.0" {
			s.respondError(req, ErrInvalidRequest, "invalid jsonrpc version")
			continue
		}

		// 分发到对应 handler
		s.dispatch(ctx, req)
	}
}

// dispatch 根据 method 将请求分发到对应 handler。
func (s *Server) dispatch(ctx context.Context, req JSONRPCRequest) {
	// initialize 前置条件:除 initialize 外所有方法需先完成握手
	if req.Method != MethodInitialize && !s.initialized {
		s.respondError(req, ErrInvalidRequest, "initialize required before any other request")
		return
	}

	switch req.Method {
	case MethodInitialize:
		s.handleInitialize(req)

	case MethodSessionNew:
		s.handleSessionNew(req)

	case MethodSessionPrompt:
		s.handleSessionPrompt(ctx, req)

	case MethodSessionCancel:
		s.handleSessionCancel(req)

	case MethodSessionClose:
		s.handleSessionClose(req)

	case MethodSessionList:
		s.handleSessionList(req)

	case MethodSessionLoad:
		s.handleSessionLoad(req)

	case MethodSessionResume:
		s.handleSessionResume(req)

	case MethodSessionDelete:
		s.handleSessionDelete(req)

	default:
		slog.Warn("acp: unknown method", "method", req.Method)
		s.respondError(req, ErrMethodNotFound, "unknown method: "+req.Method)
	}
}

// shutdown 优雅关闭服务:cancel 所有活跃 session 并等待完成。
func (s *Server) shutdown() error {
	s.mu.Lock()
	sessions := make([]*SessionState, 0, len(s.sessions))
	for _, state := range s.sessions {
		sessions = append(sessions, state)
	}
	s.mu.Unlock()

	for _, state := range sessions {
		state.cancelMu.Lock()
		if state.cancelFn != nil {
			state.cancelFn()
		}
		state.cancelMu.Unlock()
		// 关闭 per-session MCP 连接(未关闭的 session 在 shutdown 时兜底)
		if state.MCPManager != nil {
			if err := state.MCPManager.Stop(); err != nil {
				slog.Warn("acp: mcp stop", "sessionId", state.ID, "err", err)
			}
		}
	}

	// 等待所有活跃 prompt goroutine 完成
	s.wg.Wait()

	slog.Info("acp: server shut down", "sessions", len(sessions))
	return nil
}

// ---------------------------------------------------------------------------
// 响应发送辅助方法
// ---------------------------------------------------------------------------

// sendResponse 发送 JSON-RPC 成功响应。
func (s *Server) sendResponse(id any, result any) {
	resp, err := NewResponse(id, result)
	if err != nil {
		slog.Error("acp: marshal response", "err", err)
		return
	}
	if err := s.transport.Send(resp); err != nil {
		slog.Error("acp: send response", "err", err)
	}
}

// sendErrorResponse 发送 JSON-RPC 错误响应。
func (s *Server) sendErrorResponse(id any, code int, message string) {
	resp := NewErrorResponse(id, code, message)
	if err := s.transport.Send(resp); err != nil {
		slog.Error("acp: send error response", "err", err)
	}
}

// respond 发送成功响应。JSON-RPC 2.0 §4.2:notification(无 id)MUST NOT
// 收到响应——无 id 时静默跳过,处理逻辑照常执行。
func (s *Server) respond(req JSONRPCRequest, result any) {
	if req.ID == nil {
		return
	}
	s.sendResponse(req.ID, result)
}

// respondError 发送错误响应。notification 不回响应。
func (s *Server) respondError(req JSONRPCRequest, code int, message string) {
	if req.ID == nil {
		return
	}
	s.sendErrorResponse(req.ID, code, message)
}
