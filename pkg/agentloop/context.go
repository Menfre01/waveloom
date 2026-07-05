package agentloop

import "context"

// Context keys for injecting loop state into tool execution context.
// AgentTool reads these to construct sub-loop instances.
type (
	eventCallbackKey       struct{}
	parentMessagesKey      struct{}
	parentSystemPromptKey  struct{}
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
