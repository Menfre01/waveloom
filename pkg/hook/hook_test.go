package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)


// compactStr returns JSON compact form of raw message.
func compactStr(raw json.RawMessage) string {
	var buf bytes.Buffer
	_ = json.Compact(&buf, raw)
	return buf.String()
}
func TestMatch(t *testing.T) {
	tests := []struct {
		name     string
		matcher  string
		toolName string
		want     bool
	}{
		{"empty matches all", "", "Bash", true},
		{"exact match", "Bash", "Bash", true},
		{"exact mismatch", "Bash", "Read", false},
		{"prefix wildcard match", "Read*", "ReadFile", true},
		{"prefix wildcard match exact prefix", "Read*", "Read", true},
		{"prefix wildcard mismatch", "Read*", "Write", false},
		{"pipe exact match first", "Bash|Write", "Bash", true},
		{"pipe exact match second", "Bash|Write", "Write", true},
		{"pipe wildcard match", "Read*|Write*", "ReadFile", true},
		{"pipe mismatch", "Bash|Write", "Read", false},
		{"pipe with spaces", "Bash | Write", "Bash", true},
		{"empty pipe segment", "|Bash", "Bash", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Match(tt.matcher, tt.toolName)
			if got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.matcher, tt.toolName, got, tt.want)
			}
		})
	}
}

func TestLoadFromSettings(t *testing.T) {
	t.Run("valid hooks", func(t *testing.T) {
		raw := []byte(`{
			"hooks": {
				"PreToolUse": [
					{
						"matcher": "Bash",
						"hooks": [
							{
								"type": "command",
								"command": "/path/to/rtk.sh",
								"timeout": 5000
							}
						]
					}
				],
				"PostToolUse": [
					{
						"matcher": "",
						"hooks": [
							{
								"command": "logger.sh"
							}
						]
					}
				]
			}
		}`)

		configs, err := LoadFromSettings(raw)
		if err != nil {
			t.Fatalf("LoadFromSettings error: %v", err)
		}

		if len(configs) != 2 {
			t.Fatalf("expected 2 event types, got %d", len(configs))
		}

		preHooks, ok := configs[EventPreToolUse]
		if !ok {
			t.Fatal("missing PreToolUse configs")
		}
		if len(preHooks) != 1 {
			t.Fatalf("expected 1 PreToolUse config, got %d", len(preHooks))
		}
		if preHooks[0].Matcher != "Bash" {
			t.Errorf("expected matcher 'Bash', got %q", preHooks[0].Matcher)
		}
		if preHooks[0].Hooks[0].Command != "/path/to/rtk.sh" {
			t.Errorf("expected command '/path/to/rtk.sh', got %q", preHooks[0].Hooks[0].Command)
		}
		if preHooks[0].Hooks[0].Timeout != 5000 {
			t.Errorf("expected timeout 5000, got %d", preHooks[0].Hooks[0].Timeout)
		}

		postHooks, ok := configs[EventPostToolUse]
		if !ok {
			t.Fatal("missing PostToolUse configs")
		}
		if postHooks[0].Hooks[0].Command != "logger.sh" {
			t.Errorf("expected command 'logger.sh', got %q", postHooks[0].Hooks[0].Command)
		}
	})

	t.Run("no hooks", func(t *testing.T) {
		raw := []byte(`{}`)
		configs, err := LoadFromSettings(raw)
		if err != nil {
			t.Fatalf("LoadFromSettings error: %v", err)
		}
		if len(configs) != 0 {
			t.Fatalf("expected 0 configs, got %d", len(configs))
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		raw := []byte(`{invalid`)
		_, err := LoadFromSettings(raw)
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

func TestMergeConfigs(t *testing.T) {
	userConfigs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{Matcher: "Bash", Hooks: []HookItem{{Command: "user-hook.sh"}}},
		},
	}
	projectConfigs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{Matcher: "Bash", Hooks: []HookItem{{Command: "project-hook.sh"}}},
		},
		EventPostToolUse: {
			{Matcher: "", Hooks: []HookItem{{Command: "log.sh"}}},
		},
	}

	merged := MergeConfigs(userConfigs, projectConfigs)

	// project overrides user for PreToolUse
	pre := merged[EventPreToolUse]
	if len(pre) != 1 {
		t.Fatalf("expected 1 PreToolUse config, got %d", len(pre))
	}
	if pre[0].Hooks[0].Command != "project-hook.sh" {
		t.Errorf("expected project-hook.sh, got %q", pre[0].Hooks[0].Command)
	}

	// PostToolUse from project only
	post := merged[EventPostToolUse]
	if len(post) != 1 {
		t.Fatalf("expected 1 PostToolUse config, got %d", len(post))
	}
	if post[0].Hooks[0].Command != "log.sh" {
		t.Errorf("expected log.sh, got %q", post[0].Hooks[0].Command)
	}
}

func TestNewRunner(t *testing.T) {
	runner := NewRunner(nil, "session-1", "/tmp/transcript.json")
	if runner == nil {
		t.Fatal("NewRunner returned nil")
	}
	if runner.sessionID != "session-1" { //nolint:SA5011 // t.Fatal above guards nil
		t.Errorf("expected sessionID 'session-1', got %q", runner.sessionID)
	}
}

func TestHookOutputParsing(t *testing.T) {
	t.Run("full output", func(t *testing.T) {
		raw := []byte(`{
			"hookSpecificOutput": {
				"hookEventName": "PreToolUse",
				"permissionDecision": "allow",
				"permissionDecisionReason": "RTK rewrite",
				"updatedInput": {
					"command": "rtk git diff"
				}
			}
		}`)

		var output HookOutput
		if err := json.Unmarshal(raw, &output); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		ho := output.HookSpecificOutput
		if ho.HookEventName != "PreToolUse" {
			t.Errorf("expected PreToolUse, got %q", ho.HookEventName)
		}
		if ho.PermissionDecision != "allow" {
			t.Errorf("expected allow, got %q", ho.PermissionDecision)
		}
		if ho.PermissionDecisionReason != "RTK rewrite" {
			t.Errorf("expected 'RTK rewrite', got %q", ho.PermissionDecisionReason)
		}
		// json.RawMessage 保留原始空白符，用 Compact 标准化比较
		if compactStr(ho.UpdatedInput) != `{"command":"rtk git diff"}` {
			t.Errorf("unexpected UpdatedInput: %s", string(ho.UpdatedInput))
		}
	})

	t.Run("deny output", func(t *testing.T) {
		raw := []byte(`{
			"hookSpecificOutput": {
				"hookEventName": "PreToolUse",
				"permissionDecision": "deny",
				"permissionDecisionReason": "blocked .env read"
			}
		}`)

		var output HookOutput
		if err := json.Unmarshal(raw, &output); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		ho := output.HookSpecificOutput
		if ho.PermissionDecision != "deny" {
			t.Errorf("expected deny, got %q", ho.PermissionDecision)
		}
	})

	t.Run("post tool use output", func(t *testing.T) {
		raw := []byte(`{
			"hookSpecificOutput": {
				"hookEventName": "PostToolUse",
				"updatedResult": "optimized output"
			}
		}`)

		var output HookOutput
		if err := json.Unmarshal(raw, &output); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if output.HookSpecificOutput.UpdatedResult != "optimized output" {
			t.Errorf("expected 'optimized output', got %q", output.HookSpecificOutput.UpdatedResult)
		}
	})
}

func TestEventContextSerialization(t *testing.T) {
	input := json.RawMessage(`{"command":"git diff"}`)
	eventCtx := &EventContext{
		SessionID:     "abc123",
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput:     input,
	}

	data, err := json.Marshal(eventCtx)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded EventContext
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.SessionID != "abc123" {
		t.Errorf("expected sessionID 'abc123', got %q", decoded.SessionID)
	}
	if decoded.HookEventName != "PreToolUse" {
		t.Errorf("expected PreToolUse, got %q", decoded.HookEventName)
	}
	if decoded.ToolName != "Bash" {
		t.Errorf("expected Bash, got %q", decoded.ToolName)
	}
	if string(decoded.ToolInput) != `{"command":"git diff"}` {
		t.Errorf("unexpected tool input: %s", string(decoded.ToolInput))
	}
}

func TestRunPreToolUse_RewriteCommand(t *testing.T) {
	// 模拟 RTK rewrite：将 "git diff" 改写为 "rtk git diff"
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookItem{{
					Command: `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"rtk git diff"}}}'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"git diff"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	if result.ModifiedInput == nil {
		t.Fatal("expected ModifiedInput, got nil")
	}
	if compactStr(result.ModifiedInput) != `{"command":"rtk git diff"}` {
		t.Errorf("expected 'rtk git diff', got %s", string(result.ModifiedInput))
	}
}

func TestRunPreToolUse_Deny(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookItem{{
					Command: `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"blocked"}}'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	if !result.Denied {
		t.Fatal("expected Denied=true")
	}
	if result.DenyReason != "blocked" {
		t.Errorf("expected deny reason 'blocked', got %q", result.DenyReason)
	}
}

func TestRunPreToolUse_NoMatch(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookItem{{
					Command: `echo 'should not run'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Read", "test-id", json.RawMessage(`{"path":"file.txt"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	// matcher 不匹配，hook 未生效，参数应保持不变
	if result.ModifiedInput != nil {
		t.Errorf("expected no ModifiedInput for non-matching tool, got %s", string(result.ModifiedInput))
	}
}

func TestRunPreToolUse_NoHooks(t *testing.T) {
	runner := NewRunner(nil, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"git diff"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	if result.Denied {
		t.Fatal("expected no deny with nil hooks")
	}
}

func TestRunPreToolUse_HookError_PassThrough(t *testing.T) {
	// 返回 exit code 1 = 透传
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookItem{{
					Command: `exit 1`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	// exit 1 → 透传，不修改参数也不拒绝
	if result.ModifiedInput != nil {
		t.Errorf("expected no ModifiedInput on hook error, got %s", string(result.ModifiedInput))
	}
	if result.Denied {
		t.Fatal("expected no deny on hook error")
	}
}

func TestRunPostToolUse_RewriteResult(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventPostToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookItem{{
					Command: `echo '{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedResult":"optimized output"}}'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPostToolUse(ctx, "Bash", "test-id", nil, "original output", 0)
	if err != nil {
		t.Fatalf("RunPostToolUse error: %v", err)
	}
	if result.ModifiedResult != "optimized output" {
		t.Errorf("expected 'optimized output', got %q", result.ModifiedResult)
	}
}

func TestRunPreToolUse_InvalidJSON_PassThrough(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `echo 'not json'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	// 无效 JSON → 透传
	if result.ModifiedInput != nil {
		t.Errorf("expected no ModifiedInput on invalid JSON, got %s", string(result.ModifiedInput))
	}
}

func TestRunPreToolUse_EmptyOutput_PassThrough(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `true`, // 无 stdout 输出
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	// 无输出 → 透传
	if result.ModifiedInput != nil {
		t.Errorf("expected no ModifiedInput on empty output, got %s", string(result.ModifiedInput))
	}
}

func TestRunStop(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventStop: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `echo '{"hookSpecificOutput":{"hookEventName":"Stop"}}'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	// RunStop 不返回值，应不 panic
	runner.RunStop(ctx, "task completed")
}

func TestRunNotification_Async(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventNotification: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `true`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	// RunNotification 异步执行，不应阻塞
	runner.RunNotification("TaskCompleted", "done")
}

func TestValidate(t *testing.T) {
	t.Run("valid command", func(t *testing.T) {
		configs := map[EventType][]HookConfig{
			EventPreToolUse: {
				{Matcher: "", Hooks: []HookItem{{Command: "true"}}},
			},
		}
		runner := NewRunner(configs, "", "")
		warnings := runner.Validate()
		if len(warnings) > 0 {
			t.Errorf("expected no warnings for 'true', got %v", warnings)
		}
	})

	t.Run("nonexistent command", func(t *testing.T) {
		configs := map[EventType][]HookConfig{
			EventPreToolUse: {
				{Matcher: "", Hooks: []HookItem{{Command: "nonexistent-command-xyz"}}},
			},
		}
		runner := NewRunner(configs, "", "")
		warnings := runner.Validate()
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		configs := map[EventType][]HookConfig{
			EventPreToolUse: {
				{Matcher: "", Hooks: []HookItem{{Command: ""}}},
			},
		}
		runner := NewRunner(configs, "", "")
		warnings := runner.Validate()
		if len(warnings) != 1 {
			t.Errorf("expected 1 warning for empty command, got %d", len(warnings))
		}
	})

	t.Run("no hooks", func(t *testing.T) {
		runner := NewRunner(nil, "", "")
		warnings := runner.Validate()
		if len(warnings) != 0 {
			t.Errorf("expected no warnings for empty hooks, got %v", warnings)
		}
	})
}

func TestIsShellCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		// 单词路径 → 直执行
		{"simple path", "/path/to/rtk-rewrite.sh", false},
		{"relative path", "./hook.sh", false},
		{"home path", "~/bin/hook.sh", true}, // ~ 是 shell 特殊字符
		{"single executable", "true", false},
		// 多词命令 → bash -c
		{"multi word", "echo hello", true},
		{"exit with code", "exit 1", true},
		{"RTK inline", `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":{"command":"rtk git diff"}}}'`, true},
		// shell 特殊字符 → bash -c
		{"pipe", "cat file | grep pattern", true},
		{"redirect", "echo foo > /tmp/out", true},
		{"variable", "echo $HOME", true},
		{"backtick", "echo `date`", true},
		{"semicolon", "cmd1; cmd2", true},
		{"single quote", "echo 'hello'", true},
		{"double quote", `echo "hello"`, true},
		{"background", "sleep 10 &", true},
		{"subshell", "(echo hello)", true},
		{"brace", "{ echo hello; }", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isShellCommand(tt.command)
			if got != tt.want {
				t.Errorf("isShellCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestRunPreToolUse_LegacyBlock(t *testing.T) {
	// 模拟旧格式 hook：decision: "block"
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `echo '{"decision":"block","reason":"legacy block"}'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	if !result.Denied {
		t.Fatal("expected Denied=true for legacy block")
	}
	if result.DenyReason != "legacy block" {
		t.Errorf("expected deny reason 'legacy block', got %q", result.DenyReason)
	}
}

func TestValidate_UnsupportedType(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Type:    "prompt",
					Command: "some-prompt-command",
				}},
			},
		},
	}

	runner := NewRunner(configs, "", "")
	warnings := runner.Validate()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for unsupported type, got %d: %v", len(warnings), warnings)
	}
}

func TestSetSessionInfo(t *testing.T) {
	runner := NewRunner(nil, "", "")
	runner.SetSessionInfo("session-123", "/tmp/transcript.jsonl")

	if runner.sessionID != "session-123" {
		t.Errorf("expected sessionID 'session-123', got %q", runner.sessionID)
	}
	if runner.transcriptPath != "/tmp/transcript.jsonl" {
		t.Errorf("expected transcriptPath '/tmp/transcript.jsonl', got %q", runner.transcriptPath)
	}
}

func TestRunStop_Blocked(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventStop: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `echo '{"decision":"block","reason":"not done yet"}'`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	blocked := runner.RunStop(ctx, "task done")
	if !blocked {
		t.Fatal("expected blocked=true when Stop hook returns block")
	}
}

func TestRunStop_MatcherSkip(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventStop: {
			{
				Matcher: "NonExistentEvent",
				Hooks: []HookItem{{
					Command: `exit 1`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	blocked := runner.RunStop(context.Background(), "done")
	if blocked {
		t.Fatal("expected blocked=false when matcher doesn't match")
	}
}

func TestRunStop_Allow(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventStop: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `true`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	blocked := runner.RunStop(context.Background(), "done")
	if blocked {
		t.Fatal("expected blocked=false when Stop hook has no output")
	}
}

func TestRunNotification_MatcherFilter(t *testing.T) {
	configs := map[EventType][]HookConfig{
		EventNotification: {
			{
				Matcher: "TaskCompleted",
				Hooks: []HookItem{{
					Command: `true`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	// matcher 不匹配，不应执行（异步，不报错）
	runner.RunNotification("OtherEvent", "message")
}

func TestFlushWarnings_AccumulateOnHookFailure(t *testing.T) {
	// hook 以 exit code 3 失败 → addRuntimeWarn 被触发
	configs := map[EventType][]HookConfig{
		EventPreToolUse: {
			{
				Matcher: "",
				Hooks: []HookItem{{
					Command: `exit 3`,
				}},
			},
		},
	}

	runner := NewRunner(configs, "test", "")
	ctx := context.Background()
	result, err := runner.RunPreToolUse(ctx, "Bash", "test-id", json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("RunPreToolUse error: %v", err)
	}
	if result.Denied {
		t.Fatal("expected pass-through on hook failure")
	}

	// FlushWarnings 应返回 exit code 3 产生的警告
	warnings := runner.FlushWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestFlushWarnings_Empty(t *testing.T) {
	runner := NewRunner(nil, "test", "")
	warnings := runner.FlushWarnings()
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}
