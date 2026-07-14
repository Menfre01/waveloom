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
			Rule{Behavior: RuleAsk, ToolName: "write", Pattern: ""},
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
			Rule{Behavior: RuleAsk, ToolName: "write", Pattern: "src/**"},
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
		// REGRESSION: edit_file / write_file 兼容映射
		{"edit_file", "edit_file", "edit"},
		{"Edit_File", "Edit_File", "edit"},
		{"write_file", "write_file", "write"},
		{"Write_File", "Write_File", "write"},
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

// TestNormalizeToolName_Compat 验证存量配置兼容映射。
func TestNormalizeToolName_Compat(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"bash", "bash"},
		{"Bash", "bash"},
		{"Shell", "bash"},
		{"shell", "bash"},
		{"edit_file", "edit"},
		{"EDIT_FILE", "edit"},
		{"Edit_File", "edit"},
		{"write_file", "write"},
		{"WRITE_FILE", "write"},
		{"Write_File", "write"},
		// 非特殊名称原样返回
		{"read_file", "read_file"},
		{"web_fetch", "web_fetch"},
	}
	for _, tt := range tests {
		got := normalizeToolName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeToolName(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
