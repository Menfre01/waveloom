package permission

import (
	"testing"
)

func TestParseRule_ToolLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		behavior RuleBehavior
		want     Rule
	}{
		{
			"read_file allow",
			"read_file", RuleAllow,
			Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: ""},
		},
		{
			"write_file ask",
			"write_file", RuleAsk,
			Rule{Behavior: RuleAsk, ToolName: "write_file", Pattern: ""},
		},
		{
			"shell deny",
			"shell", RuleDeny,
			Rule{Behavior: RuleDeny, ToolName: "bash", Pattern: ""},
		},
		{
			"grep allow",
			"grep", RuleAllow,
			Rule{Behavior: RuleAllow, ToolName: "grep", Pattern: ""},
		},
		{
			"ls allow",
			"ls", RuleAllow,
			Rule{Behavior: RuleAllow, ToolName: "ls", Pattern: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRule(tt.input, tt.behavior)
			if err != nil {
				t.Fatalf("ParseRule(%q, %s) error: %v", tt.input, tt.behavior, err)
			}
			if got != tt.want {
				t.Errorf("ParseRule(%q, %s) = %+v, want %+v", tt.input, tt.behavior, got, tt.want)
			}
		})
	}
}

func TestParseRule_ContentLevel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		behavior RuleBehavior
		want     Rule
	}{
		{
			"Bash git pattern",
			"Bash(git *)", RuleAllow,
			Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "git *"},
		},
		{
			"shell go pattern",
			"shell(go *)", RuleAllow,
			Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "go *"},
		},
		{
			"write_file path pattern",
			"write_file(src/**)", RuleAsk,
			Rule{Behavior: RuleAsk, ToolName: "write_file", Pattern: "src/**"},
		},
		{
			"shell rm deny",
			"shell(rm -rf *)", RuleDeny,
			Rule{Behavior: RuleDeny, ToolName: "bash", Pattern: "rm -rf *"},
		},
		{
			"read_file path pattern",
			"read_file(/etc/**)", RuleDeny,
			Rule{Behavior: RuleDeny, ToolName: "read_file", Pattern: "/etc/**"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRule(tt.input, tt.behavior)
			if err != nil {
				t.Fatalf("ParseRule(%q, %s) error: %v", tt.input, tt.behavior, err)
			}
			if got != tt.want {
				t.Errorf("ParseRule(%q, %s) = %+v, want %+v", tt.input, tt.behavior, got, tt.want)
			}
		})
	}
}

func TestParseRule_BashCompatibility(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // 期望的 ToolName
	}{
		{"Bash", "Bash", "bash"},
		{"bash", "bash", "bash"},
		{"BASH", "BASH", "bash"},
		{"Bash(git status)", "Bash(git status)", "bash"},
		// REGRESSION: 存量配置 "shell" 小写
		{"shell", "shell", "bash"},
		{"Shell", "Shell", "bash"},
		{"Shell(go test)", "Shell(go test)", "bash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRule(tt.input, RuleAllow)
			if err != nil {
				t.Fatalf("ParseRule(%q) error: %v", tt.input, err)
			}
			if got.ToolName != tt.want {
				t.Errorf("ParseRule(%q).ToolName = %q, want %q", tt.input, got.ToolName, tt.want)
			}
		})
	}
}

// TestNormalizeToolName_ShellLowerCase 验证存量配置中 "shell" 归一化为 "bash"。
func TestNormalizeToolName_ShellLowerCase(t *testing.T) {
	if normalizeToolName("shell") != "bash" {
		t.Error(`normalizeToolName("shell") should be "bash"`)
	}
	if normalizeToolName("Shell") != "bash" {
		t.Error(`normalizeToolName("Shell") should be "bash"`)
	}
	if normalizeToolName("BASH") != "bash" {
		t.Error(`normalizeToolName("BASH") should be "bash"`)
	}
	// 非 Bash/Shell 名称原样返回
	if normalizeToolName("read_file") != "read_file" {
		t.Error(`normalizeToolName("read_file") should be "read_file"`)
	}
}

func TestParseRule_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"empty parentheses", "bash()"},
		{"missing closing paren", "bash(git *"},
		{"only open paren", "bash("},
		{"whitespace only", "  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRule(tt.input, RuleAllow)
			if err == nil {
				t.Errorf("ParseRule(%q) expected error, got nil", tt.input)
			}
		})
	}
}

func TestParseRule_Whitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Rule
	}{
		{
			name:  "leading/trailing spaces",
			input: "  read_file  ",
			want:  Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: ""},
		},
		{
			name:  "pattern with spaces inside",
			input: "shell(git status)",
			want:  Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "git status"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRule(tt.input, tt.want.Behavior)
			if err != nil {
				t.Fatalf("ParseRule(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseRule(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatRule(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{
			"tool-level",
			Rule{ToolName: "read_file"},
			"read_file",
		},
		{
			"content-level",
			Rule{ToolName: "bash", Pattern: "git *"},
			"bash(git *)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatRule(tt.rule); got != tt.want {
				t.Errorf("FormatRule() = %q, want %q", got, tt.want)
			}
		})
	}
}
