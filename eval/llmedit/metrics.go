package llmedit

import (
	"encoding/json"
	"strings"
)

// TurnMetrics 单个 turn 的指标快照。
type TurnMetrics struct {
	Turn int `json:"turn"`

	// L1 — hunk 生成质量
	ParseOK    bool `json:"parse_ok"`

	// L2 — 执行正确性
	FirstPassOK    bool `json:"first_pass_ok"`
	SideEffect     bool `json:"side_effect"`
	HunkMatchFail  bool `json:"hunk_match_fail"`
	WarningSeen    bool `json:"warning_seen"`
	WarningRespond bool `json:"warning_respond"`
	BlindEdit      bool `json:"blind_edit"`
	WriteUsed      bool `json:"write_used"`
	Abandoned      bool `json:"abandoned,omitempty"`

	ToolCalls []ToolCallRecord `json:"tool_calls"`
}

type ToolCallRecord struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"-"`
	ParseOK   bool   `json:"parse_ok,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
}

type RunMetrics struct {
	TaskName string        `json:"task_name"`
	Passed   bool          `json:"passed"`
	Turns    []TurnMetrics `json:"turns"`

	// L1 — hunk 解析
	TotalEdits       int     `json:"total_edits"`
	ParseSuccess     int     `json:"parse_success"`
	ParseRate        float64 `json:"parse_rate"`

	// L2 — 执行
	FirstPassCount    int `json:"first_pass_count"`
	FirstPassRate     float64 `json:"first_pass_rate"`
	SideEffects       int `json:"side_effects"`
	HunkMatchFailures int `json:"hunk_match_failures"`
	WarningsCount     int `json:"warnings_count"`
	WarningsResponded int `json:"warnings_responded"`
	BlindEdits        int `json:"blind_edits"`
	WriteCount        int `json:"write_count"`

	// L3
	TotalTurns   int  `json:"total_turns"`
	EditDistance int  `json:"edit_distance"`
	CompileOK    bool `json:"compile_ok"`
	Abandoned    bool `json:"abandoned"`
}

func NewRunMetrics(taskName string) *RunMetrics {
	return &RunMetrics{TaskName: taskName}
}

func (m *RunMetrics) RecordTurn(tm TurnMetrics) {
	m.Turns = append(m.Turns, tm)

	for _, tc := range tm.ToolCalls {
		if tc.Name != "edit" {
			continue
		}
		m.TotalEdits++
		if tc.ParseOK {
			m.ParseSuccess++
		}
		if tc.Error != "" {
			if strings.Contains(tc.Error, "tag_mismatch") || strings.Contains(tc.Result, "tag_mismatch") ||
				strings.Contains(tc.Error, "content mismatch") || strings.Contains(tc.Result, "no_match") {
				m.HunkMatchFailures++
			}
		}
		if strings.Contains(tc.Result, "re-read") || strings.Contains(tc.Result, "SKIPPING VERIFICATION") {
			m.WarningsCount++
		}
	}

	if tm.FirstPassOK {
		m.FirstPassCount++
	}
	if tm.SideEffect {
		m.SideEffects++
	}
	if tm.WarningRespond {
		m.WarningsResponded++
	}
	if tm.BlindEdit {
		m.BlindEdits++
	}
	if tm.WriteUsed {
		m.WriteCount++
	}
	if tm.Abandoned {
		m.Abandoned = true
	}
}

func (m *RunMetrics) Finalize() {
	if m.TotalEdits > 0 {
		m.ParseRate = float64(m.ParseSuccess) / float64(m.TotalEdits) * 100
		m.FirstPassRate = float64(m.FirstPassCount) / float64(m.TotalEdits) * 100
	}
	m.TotalTurns = len(m.Turns)
}

func extractEditMetrics(resultText, arguments, toolError string) (parseOK bool, hasOld bool, hasWarning bool, hasHunkFail bool) {
	// 基于 tool error 判断 hunk 是否成功应用
	parseOK = toolError == ""
	hasOld = false // deprecated — no sentinel in hunk format
	hasWarning = strings.Contains(resultText, "re-read") || strings.Contains(resultText, "SKIPPING VERIFICATION")
	hasHunkFail = strings.Contains(toolError, "tag_mismatch") ||
		strings.Contains(toolError, "content mismatch") ||
		strings.Contains(resultText, "no_match") ||
		strings.Contains(resultText, "has not been read yet")
	return
}

func TryParseToolArgs(args string, target interface{}) error {
	return json.Unmarshal([]byte(args), target)
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
