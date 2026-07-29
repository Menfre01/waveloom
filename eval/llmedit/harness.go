package llmedit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/prompt"
	"github.com/Menfre01/waveloom/pkg/tool"
)

type Runner struct {
	client       llm.Client
	registry     tool.Registry
	SystemPrompt string
	MaxTurns     int
	Model        string
	Verbose      bool
}

func NewRunner(client llm.Client, registry tool.Registry, systemPrompt string) *Runner {
	return &Runner{
		client:       client,
		registry:     registry,
		SystemPrompt: systemPrompt,
		MaxTurns:     8,
		Model:        "default",
	}
}

type RunResult struct {
	Task          *Task        `json:"task"`
	Metrics       *RunMetrics  `json:"metrics"`
	Score         *ScoreResult `json:"score"`
	Error         string       `json:"error,omitempty"`
	Elapsed       float64      `json:"elapsed_ms"`
	VerboseOutput string       `json:"verbose_output,omitempty"`
}

func (r *Runner) Run(ctx context.Context, task *Task) *RunResult {
	start := time.Now()
	result := &RunResult{Task: task}

	workDir, err := os.MkdirTemp("", "llmedit-*")
	if err != nil {
		result.Error = fmt.Sprintf("create workdir: %v", err)
		result.Elapsed = sinceMs(start)
		return result
	}
	defer os.RemoveAll(workDir)

	for path, content := range task.Files {
		fullPath := filepath.Join(workDir, filepath.Base(path))
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			result.Error = fmt.Sprintf("mkdir %s: %v", dir, err)
			result.Elapsed = sinceMs(start)
			return result
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			result.Error = fmt.Sprintf("write %s: %v", path, err)
			result.Elapsed = sinceMs(start)
			return result
		}
	}

	// 双盲: LLM 只看到自然语言指令
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: task.Instruction},
	}

	maxTurns := task.MaxTurns
	if maxTurns <= 0 {
		maxTurns = r.MaxTurns
	}

	sp := r.SystemPrompt
	if sp == "" {
		sp = buildSystemPrompt(r.registry, workDir)
	}
	// 切换到 workDir,确保工具的相对路径解析正确。
	origCWD, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		result.Error = fmt.Sprintf("chdir %s: %v", workDir, err)
		result.Elapsed = sinceMs(start)
		return result
	}
	defer func() { _ = os.Chdir(origCWD) }()
	loop := agentloop.New(r.client, r.registry, agentloop.Config{
		MaxTurns:     maxTurns,
		SystemPrompt: sp,
	})
	metrics := NewRunMetrics(task.Name)
	loopCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	eventCh := loop.Run(loopCtx, messages)

	hadRead := false
	var prevWarningSeen bool
	pendingArgs := make(map[string]string) // ToolCallID → arguments

	var verboseBuf strings.Builder
	var streamBuf strings.Builder
	for ev := range eventCh {
		switch e := ev.(type) {
		case agentloop.StreamDelta:
			streamBuf.WriteString(e.ContentDelta)
		case agentloop.ToolCallStart:
			if r.Verbose {
				fmt.Fprintf(&verboseBuf, "  [turn %d] TOOL START: %s(%s)\n", e.Turn, e.ToolCallName, e.Arguments)
			}
			pendingArgs[e.ToolCallID] = e.Arguments
		case agentloop.ToolCallResult:
			tm := TurnMetrics{Turn: e.Turn}
			args := pendingArgs[e.ToolCallID]
			delete(pendingArgs, e.ToolCallID)
			if r.Verbose {
				resultPreview := e.Result
				if len(resultPreview) > 200 {
					resultPreview = resultPreview[:200] + "..."
				}
				fmt.Fprintf(&verboseBuf, "  [turn %d] TOOL DONE:  %s -> %s (err=%v kind=%s)\n",
					e.Turn, e.ToolCallName, resultPreview, e.Error, e.ErrorKind)
			}
			rec := ToolCallRecord{
				Name:      e.ToolCallName,
				Arguments: args,
				Result:    e.Result,
				Error:     e.Error,
				ErrorKind: e.ErrorKind,
			}
			if e.ToolCallName == "read" && e.Error == "" {
				hadRead = true
			}
			if e.ToolCallName == "edit" || e.ToolCallName == "multiedit" {
				parseOK, hasOld, hasWarn, hasTag := extractEditMetrics(e.Result, rec.Arguments, e.Error)
				rec.ParseOK = parseOK
				tm.ParseOK = parseOK
				tm.HasOldSent = hasOld
				tm.WarningSeen = hasWarn
				tm.TAGMismatch = hasTag
				if !hadRead {
					tm.BlindEdit = true
				}
				if prevWarningSeen && hadRead {
					tm.WarningRespond = true
				}
				tm.ToolCalls = append(tm.ToolCalls, rec)
				metrics.RecordTurn(tm)
			} else if e.ToolCallName == "write" {
				tm.WriteUsed = true
				tm.ToolCalls = append(tm.ToolCalls, rec)
				metrics.RecordTurn(tm)
				hadRead = false
			} else {
				tm.ToolCalls = append(tm.ToolCalls, rec)
				metrics.RecordTurn(tm)
			}
		case agentloop.LoopDone:
			if r.Verbose {
				streamText := streamBuf.String()
				if len(streamText) > 500 {
					streamText = streamText[:500] + "...[truncated]"
				}
				fmt.Fprintf(&verboseBuf, "  [turn %d] STREAM TEXT: %s\n", e.Turn, streamText)
				fmt.Fprintf(&verboseBuf, "  [turn %d] LOOP DONE: reason=%s err=%v\n", e.Turn, e.Reason, e.Err)
			}
			if e.Reason == agentloop.ReasonToolFatal || e.Err != nil {
				tm := TurnMetrics{Turn: e.Turn, Abandoned: true}
				metrics.RecordTurn(tm)
			}
		}
	}
	if r.Verbose && verboseBuf.Len() > 0 {
		result.VerboseOutput = verboseBuf.String()
	}

	gotFiles := make(map[string]string, len(task.Files))
	for path := range task.Files {
		fullPath := filepath.Join(workDir, filepath.Base(path))
		data, err := os.ReadFile(fullPath)
		if err != nil {
			gotFiles[path] = fmt.Sprintf("<read error: %v>", err)
		} else {
			gotFiles[path] = string(data)
		}
	}

	metrics.Finalize()
	score := Score(task, gotFiles)
	metrics.Passed = score.Passed
	metrics.EditDistance = score.EditDistance
	metrics.CompileOK = score.CompileOK

	result.Metrics = metrics
	result.Score = score
	result.Elapsed = sinceMs(start)
	return result
}

func sinceMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000.0
}

type BatchResult struct {
	Total   int          `json:"total"`
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Errors  int          `json:"errors"`
	Results []*RunResult `json:"results"`
	Elapsed float64      `json:"elapsed_ms"`
}

func (r *Runner) RunBatch(ctx context.Context, tasks []*Task) *BatchResult {
	start := time.Now()
	br := &BatchResult{Total: len(tasks)}
	for _, task := range tasks {
		res := r.Run(ctx, task)
		br.Results = append(br.Results, res)
		if res.Error != "" {
			br.Errors++
		} else if res.Score != nil && res.Score.Passed {
			br.Passed++
		} else {
			br.Failed++
		}
	}
	br.Elapsed = sinceMs(start)
	return br
}

func (br *BatchResult) SummaryJSON() string {
	type summary struct {
		Total   int     `json:"total"`
		Passed  int     `json:"passed"`
		Failed  int     `json:"failed"`
		Errors  int     `json:"errors"`
		Rate    float64 `json:"pass_rate"`
		Elapsed float64 `json:"elapsed_ms"`
	}
	s := summary{
		Total:   br.Total,
		Passed:  br.Passed,
		Failed:  br.Failed,
		Errors:  br.Errors,
		Rate:    safeRate(br.Passed, br.Total),
		Elapsed: br.Elapsed,
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return string(data)
}

func safeRate(passed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) / float64(total) * 100
}

func RegisterEditTools(r tool.Registry) {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
}

func buildSystemPrompt(registry tool.Registry, workDir string) string {
	sp := prompt.Default
	sp += fmt.Sprintf("\n\n## Workspace\n\nCurrent working directory: %s\nAll file paths are resolved relative to this directory.", workDir)
	toolPrompts := registry.FormatToolPrompts()
	if toolPrompts == "" {
		return sp
	}
	return sp + "\n\n" + toolPrompts
}
// hunkOnlyEdit 包装 EditFile,只暴露 hunk 参数,强制使用 diff hunk 格式。
type hunkOnlyEdit struct {
	inner *tool.EditFile
}
type hunkOnlyParams struct {
	FilePath string `json:"file_path"`
	Hunk     string `json:"hunk"`
}
func (t *hunkOnlyEdit) Name() string        { return "edit" }
func (t *hunkOnlyEdit) Description() string {
	return "Edit a file using unified diff hunks. Format: @@ optional-header\\n context\\n-old\\n+new\\n context. Include 2-3 context lines around each change."
}
func (t *hunkOnlyEdit) Prompt() string       { return "" }
func (t *hunkOnlyEdit) ConcurrentSafe() bool { return false }
func (t *hunkOnlyEdit) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {"type": "string", "description": "Target file path"},
			"hunk": {"type": "string", "description": "Diff hunk: @@ header, space=context, -=delete, +=insert. Include 2-3 lines of context around each change."}
		},
		"required": ["file_path", "hunk"]
	}`)
}
func (t *hunkOnlyEdit) Execute(ctx context.Context, p hunkOnlyParams) (*tool.ToolResult, error) {
	return t.inner.Execute(ctx, tool.EditFileParams{
		FilePath: p.FilePath,
		Hunk:     p.Hunk,
	})
}
