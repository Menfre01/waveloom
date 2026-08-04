package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/mcp"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// handler — 协议方法处理器
// ---------------------------------------------------------------------------

// handleInitialize 处理 initialize 请求,返回 Agent 能力声明。
// 无状态,所有连接共享。
func (s *Server) handleInitialize(req JSONRPCRequest) {
	var params InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.respondError(req, ErrInvalidParams, "invalid initialize params: "+err.Error())
			return
		}
	}

	result := InitializeResult{
		ProtocolVersion: 1,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: PromptCapabilities{
				Image:           false,
				Audio:           false,
				EmbeddedContext: true,
			},
			McpCapabilities: &McpCapabilities{
				// stdio 为 All Agents MUST(隐式,无声明字段);http/sse 由
				// mcp.Manager 支持——session/new 的 mcpServers 已接线
				// (mcp.go parseMcpServers:stdio/http/sse 三种变体)。
				HTTP: true,
				SSE:  true,
			},
			SessionCapabilities: &SessionCapabilities{
				Resume: &struct{}{},
				Close:  &struct{}{},
				List:   &struct{}{},
				Delete: &struct{}{},
			},
			Auth: AgentAuthCapabilities{},
		},
		AgentInfo: &ImplementationInfo{
			Name:    "waveloom",
			Title:   "Waveloom",
			Version: s.buildVersion,
		},
		// Terminal 认证:客户端以 base 启动配置追加 args 启动交互式
		// 配置向导(`waveloom acp setup` = runSetup,退出码 0 表示成功),
		// 随后重连并重新 initialize。注册表 CI 要求 authMethods 非空且
		// 含 type "agent"/"terminal" 之一(auth-check)。
		AuthMethods: []AuthMethod{{
			ID:          "terminal-setup",
			Name:        "Log in from the terminal",
			Description: "Run the interactive setup wizard (waveloom acp setup) to configure the API key and provider",
			Type:        "terminal",
			Args:        []string{"setup"},
			// Zed 兼容扩展(meta 路径):stable Zed 的 terminal auth 标准路径
			// 被 acp-beta feature flag 门控(普通用户默认关),点击登录按钮
			// 时只解析 _meta.terminal-auth {label, command, args, env} 构造
			// SpawnInTerminal——缺此字段则按钮点击静默无效果。提供后 stable
			// Zed 点击 "Log in from the terminal" 即在终端拉起
			// `waveloom setup`(公开一等公民命令,与 acp setup 同一实现,
			// 写入 ~/.waveloom/settings.json),退出码 0 → Zed 重连并重新
			// initialize。meta 路径是独立 spawn,不追加 agent_servers 的
			// base args,故 args 不带 "acp" 前缀。
			// 参考 zed-industries/zed:agent_servers/src/acp.rs
			// (meta_terminal_auth_task + terminal_auth_task)。
			Meta: map[string]any{
				"terminal-auth": map[string]any{
					"label":   "waveloom acp setup",
					"command": "waveloom",
					"args":    []string{"setup"},
					"env":     map[string]any{},
				},
			},
		}},
	}

	s.respond(req, result)
	s.initialized = true
}

// handleSessionNew 处理 session/new 请求,创建新的 ACP session。
func (s *Server) handleSessionNew(req JSONRPCRequest) {
	var params SessionNewParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.respondError(req, ErrInvalidParams, "invalid session/new params: "+err.Error())
			return
		}
	}

	sessionID := session.NewSessionID()

	// per-session 分层注册表:本地 MCP 工具 + 父级内置工具(child 随 session
	// 丢弃即天然反注册,不污染其他 session)。
	childRegistry := tool.NewChildRegistry(s.toolRegistry)

	// 创建独立的 ContextManager(配置持久化路径后 CompleteRun 自动落盘;
	// 压缩配置与入口解析的窗口容量同源——settings/flag → compactionConfig)
	cm := session.NewWithCompaction(s.systemPrompt, s.compactionConfig, s.summarizer)
	if s.sessionDir != "" {
		cm.SetSessionPath(filepath.Join(s.sessionDir, sessionID+".json"))
	}

	// TodoState:session 级任务清单。Loop 依赖它注入 todo 摘要(每轮 LLM
	// 调用前)与完成前提醒;未配置时 todo_create/todo_update 返回
	// "not available"(execute.go executeTodoMutate)。
	todoState := todo.NewTodoState()

	// Guard:优先使用入口注入的共享实例(ACP 已 EnableAutoAllow → 二元决策,
	// 仅 DENY/ALLOW,不产生 ASK;ACP v1 无权限确认协议)。未注入时 fallback
	// 裸 Guard 保持 fail-closed(ASK → 无 responder → deny)。
	guard := s.guard
	if guard == nil {
		guard = permission.NewGuard(
			permission.WithBypassMode(false),
		)
	}

	// 创建 Loop(共享 tool.Registry 和 llm.Client)
	loop := agentloop.New(s.llmClient, childRegistry, agentloop.Config{
		SystemPrompt: s.systemPrompt,
		Guard:        guard,
		MaxTurns:     s.maxTurns,
		SandboxMgr:   s.sandboxMgr,
		TodoState:    todoState,
		Compactor:    cm.Compactor(), // 上下文压缩在 Loop 内每轮执行(与 TUI 一致)
	})

	state := &SessionState{
		ID:    sessionID,
		CM:    cm,
		Loop:  loop,
		Guard: guard,
		CWD:   s.cwd,
		Registry: childRegistry,
	}

	// MCP:session/new 的 mcpServers → per-session Manager(v1 All Agents MUST)
	if len(params.McpServers) > 0 {
		configs, err := parseMcpServers(params.McpServers)
		if err != nil {
			s.respondError(req, ErrInvalidParams, "invalid mcpServers: "+err.Error())
			return
		}
		mcpMgr := mcp.NewManager(childRegistry)
		if s.mcpConnect != nil {
			mcpMgr.SetConnectFunc(s.mcpConnect)
		}
		mcpMgr.Start(context.Background(), configs)
		state.MCPManager = mcpMgr
		slog.Info("acp: mcp servers started", "sessionId", sessionID, "servers", len(configs))
	}

	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()

	// 发送 available_commands_update(客户端命令面板;命令列表由入口注入)
	if s.commandRunner != nil {
		if cmds := s.commandRunner.AvailableCommands(); len(cmds) > 0 {
			ad := newAdapter(sessionID, func(msg any) error { return s.transport.Send(msg) })
			ad.sendAvailableCommands(cmds)
		}
	}

	slog.Info("acp: session created", "sessionId", sessionID)

	s.respond(req, SessionNewResult{
		SessionID: sessionID,
	})
}

// handleSessionPrompt 处理 session/prompt 请求,执行 LLM 交互。
func (s *Server) handleSessionPrompt(ctx context.Context, req JSONRPCRequest) {
	var params SessionPromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req, ErrInvalidParams, "invalid session/prompt params: "+err.Error())
		return
	}

	// 验证 session 存在
	s.mu.RLock()
	state, ok := s.sessions[params.SessionID]
	s.mu.RUnlock()
	if !ok {
		s.respondError(req, ErrSessionNotFound, fmt.Sprintf("session %q not found", params.SessionID))
		return
	}

	// 同一 session 串行保护:重复 prompt 返回 -32001 Session busy
	if !state.promptMu.TryLock() {
		s.respondError(req, ErrSessionBusy, fmt.Sprintf("session %q is busy", params.SessionID))
		return
	}

	// 同步置"已接受"标志(cancelMu 保护):覆盖 go executePrompt 调度前的
	// cancel 竞态窗口——此窗口内 cancelSession 凭 promptQueued 置
	// cancelPending,不再被当空闲期忽略(五审 High-6)。
	state.cancelMu.Lock()
	state.promptQueued = true
	state.cancelMu.Unlock()

	// 注册 requestId → session($/cancel_request 按 id 取消)
	s.registerRequest(req.ID, params.SessionID)

	// 启动独立的 goroutine 执行 prompt
	s.wg.Add(1)
	go s.executePrompt(ctx, req.ID, state, params)
}

// handleSessionCancel 处理 session/cancel 请求,取消指定 session 的活跃 prompt。
// 协议语义为通知(无响应);cancel 可能在 prompt 启动前到达,置 cancelPending
// 由 executePrompt 消费,避免竞态丢失。
func (s *Server) handleSessionCancel(req JSONRPCRequest) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req, ErrInvalidParams, "invalid session/cancel params")
		return
	}

	s.mu.RLock()
	state, ok := s.sessions[params.SessionID]
	s.mu.RUnlock()
	if !ok {
		s.respondError(req, ErrSessionNotFound, "session not found")
		return
	}

	cancelSession(state)

	// session/cancel 是通知,无响应
}

// handleCancelRequest 处理 $/cancel_request 通知(LSP 风格按 requestId 取消)。
// 官方语义:发送方取消自己发出的请求;未知 requestId 静默忽略(notification 无响应)。
func (s *Server) handleCancelRequest(req JSONRPCRequest) {
	var params struct {
		RequestID any `json:"requestId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return // 参数非法:忽略(通知无响应)
	}
	sid, ok := s.sessionForRequest(params.RequestID)
	if !ok {
		return // 未知请求(已结束或不存在):忽略
	}
	s.mu.RLock()
	state, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return
	}
	cancelSession(state)
}

// cancelSession 取消 session 的活跃 prompt(与 handleSessionCancel 共用):
// cancelFn 直取消 / 启动窗口置 cancelPending / 空闲期忽略。
func cancelSession(state *SessionState) {
	state.cancelMu.Lock()
	if state.cancelFn != nil {
		state.cancelFn()
	} else if state.promptStarted || state.promptQueued {
		// REGRESSION: cancel 与 prompt 启动竞态——prompt goroutine 已启动但
		// cancelFn 尚未设置的窗口内到达的 cancel 曾被静默丢弃;promptQueued
		// 进一步覆盖"goroutine 尚未调度"的窗口(handleSessionPrompt 同步置位,
		// 五审 High-6)。置位后由 executePrompt 在设置 cancelFn 时消费。
		// 空闲期(无活跃 prompt)的 cancel 忽略,避免误取消下一个 prompt。
		state.cancelPending = true
	}
	state.cancelMu.Unlock()
}

// handleSessionClose 处理 session/close 请求:取消活跃 prompt 并从注册表移除。
func (s *Server) handleSessionClose(req JSONRPCRequest) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req, ErrInvalidParams, "invalid session/close params")
		return
	}
	if !validSessionID(params.SessionID) {
		s.respondError(req, ErrInvalidParams, "invalid sessionId")
		return
	}

	state, ok := s.removeSession(params.SessionID)
	if !ok {
		s.respondError(req, ErrSessionNotFound, fmt.Sprintf("session %q not found", params.SessionID))
		return
	}

	// 取消活跃 prompt(goroutine 自然收尾,wg 跟踪)。
	// cancelSession 覆盖 promptQueued 启动窗口(二审 M1)。
	cancelSession(state)
	// 关闭 per-session MCP 连接
	if state.MCPManager != nil {
		if err := state.MCPManager.Stop(); err != nil {
			slog.Warn("acp: mcp stop", "sessionId", params.SessionID, "err", err)
		}
	}

	slog.Info("acp: session closed", "sessionId", params.SessionID)
	s.respond(req, struct{}{})
}

// handleSessionList 处理 session/list 请求:返回进程内 + 磁盘持久化 session。
func (s *Server) handleSessionList(req JSONRPCRequest) {
	seen := make(map[string]bool)
	var items []SessionListItem

	s.mu.RLock()
	for id := range s.sessions {
		seen[id] = true
		items = append(items, SessionListItem{SessionID: id, Cwd: s.cwd})
	}
	s.mu.RUnlock()

	// 磁盘 session(跨进程持久化;session 文件在 CompleteRun 时落盘)
	if s.sessionDir != "" {
		entries, err := os.ReadDir(s.sessionDir)
		if err == nil {
			for _, e := range entries {
				name := e.Name()
				if !strings.HasSuffix(name, ".json") {
					continue
				}
				id := strings.TrimSuffix(name, ".json")
				if !seen[id] {
					seen[id] = true
					items = append(items, SessionListItem{SessionID: id, Cwd: s.cwd})
				}
			}
		} else if !os.IsNotExist(err) {
			slog.Warn("acp: session list dir", "err", err)
		}
	}

	s.respond(req, SessionListResult{Sessions: items})
}

// handleSessionLoad 处理 session/load 请求:从磁盘恢复 session 并回放消息历史。
func (s *Server) handleSessionLoad(req JSONRPCRequest) {
	var params SessionLoadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req, ErrInvalidParams, "invalid session/load params: "+err.Error())
		return
	}
	if !validSessionID(params.SessionID) {
		s.respondError(req, ErrInvalidParams, "invalid sessionId")
		return
	}
	// 已活跃的 session 不可重复加载(旧 prompt goroutine 会覆写磁盘状态)
	s.mu.RLock()
	_, active := s.sessions[params.SessionID]
	s.mu.RUnlock()
	if active {
		s.respondError(req, ErrSessionBusy, fmt.Sprintf("session %q is already active", params.SessionID))
		return
	}

	state, err := s.loadSessionFromDisk(params.SessionID)
	if err != nil {
		s.respondError(req, ErrSessionNotFound, err.Error())
		return
	}
	s.registerSession(state)

	// 回放消息历史为 session/update 通知(客户端恢复 UI)
	s.replayHistory(state)

	s.respond(req, SessionNewResult{SessionID: params.SessionID})
}

// handleSessionResume 处理 session/resume 请求:从磁盘恢复 session,不回放历史。
func (s *Server) handleSessionResume(req JSONRPCRequest) {
	var params SessionLoadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req, ErrInvalidParams, "invalid session/resume params: "+err.Error())
		return
	}
	if !validSessionID(params.SessionID) {
		s.respondError(req, ErrInvalidParams, "invalid sessionId")
		return
	}
	s.mu.RLock()
	_, active := s.sessions[params.SessionID]
	s.mu.RUnlock()
	if active {
		s.respondError(req, ErrSessionBusy, fmt.Sprintf("session %q is already active", params.SessionID))
		return
	}

	state, err := s.loadSessionFromDisk(params.SessionID)
	if err != nil {
		s.respondError(req, ErrSessionNotFound, err.Error())
		return
	}
	s.registerSession(state)

	s.respond(req, SessionNewResult{SessionID: params.SessionID})
}

// handleSessionDelete 处理 session/delete 请求:移除注册表并删除磁盘文件。
func (s *Server) handleSessionDelete(req JSONRPCRequest) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.respondError(req, ErrInvalidParams, "invalid session/delete params")
		return
	}
	if !validSessionID(params.SessionID) {
		s.respondError(req, ErrInvalidParams, "invalid sessionId")
		return
	}

	// 进程内:取消活跃 prompt + 移除注册表(磁盘 session 可能不在 map 中)
	if state, ok := s.removeSession(params.SessionID); ok {
		// cancelSession 覆盖 promptQueued 启动窗口(二审 M1)。
		cancelSession(state)
		if state.MCPManager != nil {
			if err := state.MCPManager.Stop(); err != nil {
				slog.Warn("acp: mcp stop", "sessionId", params.SessionID, "err", err)
			}
		}
	}

	// 磁盘:删除 session 文件
	if s.sessionDir != "" {
		path := filepath.Join(s.sessionDir, params.SessionID+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("acp: session delete file", "err", err)
		}
	}

	s.respond(req, struct{}{})
}

// executePrompt 在独立 goroutine 中执行 session/prompt。
func (s *Server) executePrompt(ctx context.Context, id any, state *SessionState, params SessionPromptParams) {
	defer state.promptMu.Unlock()
	defer s.wg.Done()
	defer s.unregisterRequest(id) // prompt 完成,移除 $/cancel_request 映射

	// 创建可取消的 context
	promptCtx, cancel := context.WithCancel(ctx)
	state.cancelMu.Lock()
	state.promptStarted = true
	state.promptQueued = false // 启动窗口结束:已接受状态由 cancelFn 承载
	state.cancelFn = cancel
	// 消费 cancel 竞态标志:prompt 启动前到达的 cancel 立即生效
	cancelPending := state.cancelPending
	state.cancelPending = false
	state.cancelMu.Unlock()
	if cancelPending {
		cancel()
	}
	defer func() {
		state.cancelMu.Lock()
		state.cancelFn = nil
		state.promptStarted = false
		state.cancelMu.Unlock()
	}()

	// 提取用户文本(含 embeddedContext:resource 内嵌块 + resource_link 文件)
	userText := extractPromptText(params.Prompt, s.cwd)
	if userText == "" {
		if id != nil {
			s.sendErrorResponse(id, ErrInvalidParams, "prompt is empty")
		}
		return
	}

	// 适配器提前创建(命令拦截与 Loop 路径共用)
	ad := newAdapterWithContextLimit(state.ID, func(msg any) error {
		return s.transport.Send(msg)
	}, s.contextLimit)

	// 回显用户消息(Zed 会话显示 agent 实际处理的 prompt,含 resource 展开)
	ad.sendUserMessage(userText)

	// 首次 prompt 设置 session 标题(Threads 侧栏展示)
	if !state.titleSet {
		state.titleSet = true
		ad.sendSessionInfo(truncateTitle(userText))
	}

	// 斜杠命令拦截(/cmd 开头且匹配注册命令):
	//   - skill 命令 → 注入指令文本,继续 LLM
	//   - 其他命令(help/model/provider)→ 直接文本回复,不调用 LLM
	if s.commandRunner != nil {
		if resultText, injectedPrompt, handled := s.commandRunner.Run(ctx, userText); handled {
			if injectedPrompt != "" {
				userText = injectedPrompt
			} else {
				if resultText != "" {
					ad.sendUpdate(ContentChunk{
						SessionUpdate: "agent_message_chunk",
						MessageID:     ad.messageID,
						Content:       ContentBlock{Type: "text", Text: resultText},
					})
				}
				if id != nil {
					s.sendResponse(id, SessionPromptResult{StopReason: "end_turn"})
				}
				slog.Info("acp: slash command handled", "sessionId", state.ID)
				return
			}
		}
	}

	// 未配置 LLM(终端认证待完成):返回 AUTH_REQUIRED(-32000),客户端
	// 据此触发 authMethods 中的 terminal 登录流(`waveloom acp setup`)。
	// 斜杠命令(help 等)在上一段已处理,无需 LLM 的命令不受影响。
	if s.llmClient == nil {
		// REGRESSION: notification(id==nil)不得回响应(JSON-RPC §4.2)——此前
		// 无条件 sendErrorResponse 会对无 id 的 prompt 通知回 id:null 错误帧,
		// 规范型客户端视为协议违规。
		if id != nil {
			s.sendErrorResponse(id, ErrAuthRequired,
				"authentication required: run 'waveloom acp setup' to configure the API key and provider")
		}
		slog.Info("acp: prompt rejected, AUTH_REQUIRED", "sessionId", state.ID)
		return
	}

	// 追加 user 消息并获取完整消息历史
	messages, _ := state.CM.PrepareRun(userText)

	// 启动 Loop
	eventCh := state.Loop.Run(promptCtx, messages)

	// 适配事件 → session/update 通知
	loopDone, gotReal := ad.consumeEvents(promptCtx, eventCh)
	stopReason := ACPStopReason(string(loopDone.Reason))
	stats := ad.Stats()

	// 仅真实 LoopDone 携带完整消息历史;异常路径(通道关闭但未收到
	// LoopDone)返回合成值,若传入 CompleteRun 会用空历史整体替换会话
	// 上下文(数据损坏)。此时跳过提交:PrepareRun 追加的 user 消息保留
	// 在 CM 中,上下文不丢失。
	if gotReal {
		// 完成 run:使用 LoopDone.Messages 保留完整会话历史,
		// token 统计用 adapter 累积的真实值(原实现传全零)。
		_ = state.CM.CompleteRun(
			loopDone.Messages,
			stats.PromptTokens, 0, stats.CompletionTokens,
			stats.CacheHitTokens, stats.CacheMissTokens, stats.ReasoningTokens,
			"", 0,
			stopReason,
		)
	}

	// 发送最终响应
	result := SessionPromptResult{
		StopReason: stopReason,
	}
	// JSON-RPC §4.2:notification(无 id)MUST NOT 收到响应
	if id != nil {
		s.sendResponse(id, result)
	}

	slog.Info("acp: prompt completed", "sessionId", state.ID, "stopReason", stopReason)
}

// ---------------------------------------------------------------------------
// session 生命周期辅助
// ---------------------------------------------------------------------------

// removeSession 从注册表移除 session 并返回。
func (s *Server) removeSession(id string) (*SessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
	}
	return state, ok
}

// registerSession 注册 session 到注册表。
func (s *Server) registerSession(state *SessionState) {
	s.mu.Lock()
	s.sessions[state.ID] = state
	s.mu.Unlock()
}

// loadSessionFromDisk 从磁盘加载 session 文件并构造 SessionState。
func (s *Server) loadSessionFromDisk(sessionID string) (*SessionState, error) {
	if s.sessionDir == "" {
		return nil, fmt.Errorf("session persistence not configured")
	}
	path := filepath.Join(s.sessionDir, sessionID+".json")
	cm := session.NewWithCompaction(s.systemPrompt, s.compactionConfig, s.summarizer)
	if !cm.LoadFromFile(path) {
		return nil, fmt.Errorf("session file not found: %s", path)
	}
	// REGRESSION: LoadFromFile 用磁盘持久化的 watermark.ContextLimit 整体覆盖
	// compactor 阈值(与主 agent resume 同源问题)——恢复后重放当前窗口容量,
	// 保证压缩阈值与 usage_update.size / 入口配置一致。
	if s.contextLimit > 0 {
		if c, ok := cm.Compactor().(interface{ SetContextLimit(int) }); ok {
			c.SetContextLimit(s.contextLimit)
		}
	}

	todoState := todo.NewTodoState()
	guard := s.guard
	if guard == nil {
		guard = permission.NewGuard(permission.WithBypassMode(false))
	}
	// load/resume 协议无 mcpServers 参数:child registry 无 MCP 工具
	childRegistry := tool.NewChildRegistry(s.toolRegistry)
	loop := agentloop.New(s.llmClient, childRegistry, agentloop.Config{
		SystemPrompt: s.systemPrompt,
		Guard:        guard,
		MaxTurns:     s.maxTurns,
		SandboxMgr:   s.sandboxMgr,
		TodoState:    todoState,
		Compactor:    cm.Compactor(),
	})

	return &SessionState{
		ID:    sessionID,
		CM:    cm,
		Loop:  loop,
		Guard: guard,
		CWD:   s.cwd,
		Registry: childRegistry,
	}, nil
}

// replayHistory 将 session 消息历史回放为 session/update 通知(session/load 用)。
func (s *Server) replayHistory(state *SessionState) {
	messages := state.CM.Messages()
	for _, m := range messages {
		var updateType, text string
		switch m.Role {
		case llm.RoleUser:
			updateType = "user_message_chunk"
			text = m.Content
		case llm.RoleAssistant:
			updateType = "agent_message_chunk"
			text = m.Content
		default:
			continue
		}
		if text == "" {
			continue
		}
		ad := newAdapter(state.ID, func(msg any) error { return s.transport.Send(msg) })
		ad.sendUpdate(ContentChunk{
			SessionUpdate: updateType,
			MessageID:     llm.NewMessageID(),
			Content:       ContentBlock{Type: "text", Text: text},
		})
	}
}

// validSessionID 校验客户端提供的 sessionId 是否安全可用于文件系统路径。
// session.NewSessionID 生成 8-4-4-4-12 hex;仅允许 [A-Za-z0-9_-],
// 杜绝 `../` 路径穿越(任意读/删/覆写 sessionDir 下文件)。
func validSessionID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

// truncateTitle 从用户消息生成 session 标题(单行截断,多行取首行)。
func truncateTitle(text string) string {
	title := text
	if idx := strings.IndexAny(title, "\n\r"); idx >= 0 {
		title = title[:idx]
	}
	runes := []rune(title)
	if len(runes) > 60 {
		title = string(runes[:60]) + "…"
	}
	return title
}

// ---------------------------------------------------------------------------
// embeddedContext 提取
// ---------------------------------------------------------------------------

// extractPromptText 从 ContentBlock 切片提取用户文本(embeddedContext 支持):
// - text 块:直接拼接
// - resource 块:内嵌 text / base64 blob 解码为文本
// - resource_link 块:读取 URI 指向的本地文件(file:// 或裸路径);
//   读取失败时追加错误说明,不阻断 prompt(声明 embeddedContext:true 的承诺)
// cwd 限制 resource_link 的读取边界(仅工作区内文件,防任意文件读入 LLM 上下文)。
func extractPromptText(blocks []ContentBlock, cwd string) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "resource":
			if b.Resource == nil {
				continue
			}
			if b.Resource.Text != "" {
				parts = append(parts, b.Resource.Text)
			} else if b.Resource.Blob != "" {
				if data, err := base64.StdEncoding.DecodeString(b.Resource.Blob); err == nil {
					parts = append(parts, string(data))
				} else {
					parts = append(parts, "[resource blob: undecodable base64]")
				}
			}
		case "resource_link":
			content, err := readResourceLink(b.URI, cwd)
			if err != nil {
				parts = append(parts, fmt.Sprintf("[resource_link %s: %v]", b.URI, err))
			} else {
				parts = append(parts, content)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// readResourceLink 读取 resource_link URI 指向的本地文件内容。
// 仅允许 file:// 或绝对路径,且必须位于 cwd 工作区内(安全边界)。
func readResourceLink(uri, cwd string) (string, error) {
	path := uri
	isFileURI := strings.HasPrefix(uri, "file://")
	if isFileURI {
		rest := strings.TrimPrefix(uri, "file://")
		// Windows 盘符形式:file://C:\x 或 file://C:/x——直接作为路径,
		// 不走 host 解析(REGRESSION:strings.Cut(rest, "/") 无 "/" 时误报
		// "invalid file URI",Windows CI 必挂)。
		if len(rest) >= 2 && isDriveLetterPrefix(rest) {
			path = rest
		} else
		// 处理 file://localhost/path 形式
		if host, p, ok := strings.Cut(rest, "/"); ok && host != "" && host != "localhost" {
			return "", fmt.Errorf("unsupported file URI host %q", host)
		} else if !ok {
			return "", fmt.Errorf("invalid file URI %q", uri)
		} else {
			path = "/" + p
		}
	}
	// file:// 来源的路径按 URL 语义(/ 开头即绝对):Windows 上
	// filepath.IsAbs("/etc/passwd") 恒 false,会误报 "must be absolute"——
	// 跳过 IsAbs,由下方 workspace 边界检查(filepath.Rel)跨平台统一拦截。
	if !isFileURI && !filepath.IsAbs(path) {
		return "", fmt.Errorf("resource_link path must be absolute: %q", uri)
	}
	// 安全边界:仅工作区内文件(cwd 为空时不限制——测试/内部使用)
	if cwd != "" {
		rel, err := filepath.Rel(cwd, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("resource_link outside workspace: %q", uri)
		}
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// isDriveLetterPrefix 判断路径是否以盘符开头(C:\ 或 C:/,Windows 绝对路径)。
func isDriveLetterPrefix(s string) bool {
	return len(s) >= 2 &&
		((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) &&
		s[1] == ':'
}
