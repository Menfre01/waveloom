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
			Rule{Behavior: RuleDeny, ToolName: "shell", Pattern: ""},
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
			Rule{Behavior: RuleAllow, ToolName: "shell", Pattern: "git *"},
		},
		{
			"shell go pattern",
			"shell(go *)", RuleAllow,
			Rule{Behavior: RuleAllow, ToolName: "shell", Pattern: "go *"},
		},
		{
			"write_file path pattern",
			"write_file(src/**)", RuleAsk,
			Rule{Behavior: RuleAsk, ToolName: "write_file", Pattern: "src/**"},
		},
		{
			"shell rm deny",
			"shell(rm -rf *)", RuleDeny,
			Rule{Behavior: RuleDeny, ToolName: "shell", Pattern: "rm -rf *"},
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
		{"Bash", "Bash", "shell"},
		{"bash", "bash", "shell"},
		{"BASH", "BASH", "shell"},
		{"Bash(git status)", "Bash(git status)", "shell"},
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

func TestParseRule_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"empty parentheses", "shell()"},
		{"missing closing paren", "shell(git *"},
		{"only open paren", "shell("},
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
			want:  Rule{Behavior: RuleAllow, ToolName: "shell", Pattern: "git status"},
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
			Rule{ToolName: "shell", Pattern: "git *"},
			"shell(git *)",
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
