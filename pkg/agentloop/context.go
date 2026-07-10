package agentloop

import "context"

// Context keys for injecting loop state into tool execution context.
// AgentTool reads these to construct sub-loop instances.
type (
	eventCallbackKey       struct{}
	parentMessagesKey      struct{}
	parentSystemPromptKey  struct{}
	agentsMDKey            struct{}
	toolCallIDKey          struct{}
	messageIDKey           struct{}
)

// WithEventCallback injects a callback for subagent events into ctx.
func WithEventCallback(ctx context.Context, cb func(TurnEvent)) context.Context {
	return context.WithValue(ctx, eventCallbackKey{}, cb)
}

// EventCallbackFromContext extracts the event callback from ctx.
func EventCallbackFromContext(ctx context.Context) func(TurnEvent) {
	cb, _ := ctx.Value(eventCallbackKey{}).(func(TurnEvent))
	return cb
}

// WithParentMessages injects the parent loop's message history into ctx.
// The value is stored as interface{} to avoid importing llm package from agentloop.
// Callers must pass a []llm.Message slice; consumers must type-assert back.
func WithParentMessages(ctx context.Context, msgs interface{}) context.Context {
	return context.WithValue(ctx, parentMessagesKey{}, msgs)
}

// ParentMessagesFromContext extracts parent messages from ctx as interface{}.
// Consumers must type-assert to []llm.Message.
func ParentMessagesFromContext(ctx context.Context) interface{} {
	return ctx.Value(parentMessagesKey{})
}

// ParentSystemPromptFromContext extracts the parent system prompt from ctx.
// Key defined here; subagent package reads via this getter.
func ParentSystemPromptFromContext(ctx context.Context) string {
	sp, _ := ctx.Value(parentSystemPromptKey{}).(string)
	return sp
}

// WithParentSystemPrompt injects the parent system prompt into ctx for subagents.
func WithParentSystemPrompt(ctx context.Context, sp string) context.Context {
	return context.WithValue(ctx, parentSystemPromptKey{}, sp)
}

// WithAgentsMD injects the project AGENTS.md content into ctx for cold subagents.
func WithAgentsMD(ctx context.Context, text string) context.Context {
	return context.WithValue(ctx, agentsMDKey{}, text)
}

// AgentsMDFromContext extracts the AGENTS.md content from ctx.
func AgentsMDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(agentsMDKey{}).(string)
	return s
}

// WithToolCallID injects the current tool call ID into ctx for subagent event routing.
func WithToolCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolCallIDKey{}, id)
}

// ToolCallIDFromContext extracts the tool call ID from ctx.
func ToolCallIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(toolCallIDKey{}).(string)
	return s
}

// WithMessageID injects the current user message ID into ctx for filehistory tracking.
func WithMessageID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, messageIDKey{}, id)
}

// MessageIDFromContext extracts the user message ID from ctx.
func MessageIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(messageIDKey{}).(string)
	return s
}
