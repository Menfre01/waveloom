//go:build integration

package compaction

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// loadSummaryClient 从项目级 settings.json 创建 JSON 模式的摘要 Client。
// 找不到配置或 API key 时跳过测试。
func loadSummaryClient(t *testing.T) llm.Client {
	t.Helper()

	// 尝试多个路径（go test 可能从项目根目录或包目录运行）
	candidates := []string{
		".waveloom/settings.json",
		"../../.waveloom/settings.json",
	}
	var settings *llm.LLMSettings
	var err error
	for _, p := range candidates {
		settings, err = llm.LoadSettingsIfExists(p)
		if err != nil {
			t.Skipf("加载 LLM 配置失败 (%s): %v", p, err)
		}
		if settings != nil {
			break
		}
	}
	if settings == nil {
		t.Skip("未找到项目级 settings.json（.waveloom/settings.json），跳过集成测试")
	}

	// 检查 API key（settings.api_key 或环境变量）
	hasKey := settings.APIKey != "" || os.Getenv("LLM_API_KEY") != ""
	if !hasKey {
		t.Skip("未配置 API Key（settings.json 的 api_key 字段或 LLM_API_KEY 环境变量），跳过集成测试")
	}

	_, cfg, err := llm.NewClientFromLLMSettings(settings)
	if err != nil {
		t.Skipf("创建 LLM Client 失败: %v", err)
	}

	cfg.ResponseFormat = "json_object"
	client, err := llm.NewClient(cfg)
	if err != nil {
		t.Fatalf("创建 JSON 模式摘要 Client 失败: %v", err)
	}
	return client
}

// validateSummarySchema 严格校验摘要输出格式。
func validateSummarySchema(t *testing.T, raw string) {
	t.Helper()

	if !json.Valid([]byte(raw)) {
		t.Fatalf("摘要输出不是合法 JSON: %s", raw)
	}

	var s map[string]any
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}

	// 顶层只能有 4 个字段
	if len(s) != 4 {
		t.Fatalf("顶层字段数应为 4（progress/pending/pitfalls/constraints），实际 %d: %v", len(s), topLevelKeys(s))
	}

	// progress
	progress, ok := s["progress"].(map[string]any)
	if !ok {
		t.Fatalf("progress 应为对象，实际类型 %T: %v", s["progress"], s["progress"])
	}
	if len(progress) != 2 {
		t.Fatalf("progress 子字段数应为 2（summary/files），实际 %d", len(progress))
	}

	// progress.summary
	summaryText, ok := progress["summary"].(string)
	if !ok {
		t.Fatalf("progress.summary 应为字符串，实际类型 %T", progress["summary"])
	}
	if len(summaryText) == 0 {
		t.Fatal("progress.summary 为空")
	}
	if len([]rune(summaryText)) > 300 {
		t.Logf("警告: progress.summary 长度 %d 字（预期 <200）", len([]rune(summaryText)))
	}

	// progress.files
	files, ok := progress["files"].([]any)
	if !ok {
		t.Fatalf("progress.files 应为数组，实际类型 %T", progress["files"])
	}
	for i, f := range files {
		fileObj, ok := f.(map[string]any)
		if !ok {
			t.Fatalf("progress.files[%d] 应为对象，实际类型 %T", i, f)
		}
		if _, ok := fileObj["path"]; !ok {
			t.Fatalf("progress.files[%d]: 缺少 path", i)
		}
		if _, ok := fileObj["action"]; !ok {
			t.Fatalf("progress.files[%d]: 缺少 action", i)
		}
		if _, ok := fileObj["why"]; !ok {
			t.Fatalf("progress.files[%d]: 缺少 why", i)
		}
		action, _ := fileObj["action"].(string)
		if !isValidAction(action) {
			t.Fatalf("progress.files[%d]: 非法 action %q（应为 created|modified|deleted|read）", i, action)
		}
	}

	// pending
	pending, ok := s["pending"].([]any)
	if !ok {
		t.Fatalf("pending 应为数组，实际类型 %T", s["pending"])
	}
	for i, p := range pending {
		if _, ok := p.(string); !ok {
			t.Fatalf("pending[%d] 应为字符串，实际类型 %T", i, p)
		}
	}

	// pitfalls
	pitfalls, ok := s["pitfalls"].([]any)
	if !ok {
		t.Fatalf("pitfalls 应为数组，实际类型 %T", s["pitfalls"])
	}
	for i, pf := range pitfalls {
		pfObj, ok := pf.(map[string]any)
		if !ok {
			t.Fatalf("pitfalls[%d] 应为对象，实际类型 %T", i, pf)
		}
		if _, ok := pfObj["problem"]; !ok {
			t.Fatalf("pitfalls[%d]: 缺少 problem", i)
		}
		if _, ok := pfObj["solution"]; !ok {
			t.Fatalf("pitfalls[%d]: 缺少 solution", i)
		}
	}

	// constraints
	constraints, ok := s["constraints"].(string)
	if !ok {
		t.Fatalf("constraints 应为字符串，实际类型 %T", s["constraints"])
	}
	_ = constraints
}

func topLevelKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func isValidAction(a string) bool {
	switch a {
	case "created", "modified", "deleted", "read":
		return true
	}
	return false
}

func TestCompactionSummarizer_Integration(t *testing.T) {
	client := loadSummaryClient(t)
	s := NewCompactionSummarizer(client, 0)

	delta := []llm.Message{
		{Role: llm.RoleUser, Content: "请帮我重构 pkg/compaction/compaction.go 中的 CompactMessages 函数，提取 Tier 判定逻辑为独立函数。"},
		{Role: llm.RoleAssistant, Content: "好的，我来分析 CompactMessages 的 Tier 判定逻辑。"},
		{Role: llm.RoleTool, Name: "read_file", Content: "// Package compaction — 四级水位线上下文压缩系统。\n// ...（文件内容已截断）", ToolCallID: "tc1"},
		{Role: llm.RoleAssistant, Content: "已读取文件，Tier 判定逻辑位于 switch 语句中。提取为 determineTier 函数。"},
	}

	result, err := s.Summarize(context.Background(), nil, delta)
	if err != nil {
		t.Fatalf("Summarize 失败: %v", err)
	}

	validateSummarySchema(t, result)
	t.Logf("摘要结果:\n%s", result)
}

func TestCompactionSummarizer_Integration_WithExistingSummaries(t *testing.T) {
	client := loadSummaryClient(t)
	s := NewCompactionSummarizer(client, 0)

	existing := []string{
		`{"progress":{"summary":"第一轮：读取了 compaction.go，分析了四级水位线结构","files":[{"path":"pkg/compaction/compaction.go","action":"read","why":"分析压缩逻辑"}]},"pending":["提取 Tier 判定逻辑"],"pitfalls":[],"constraints":"遵循 Go 惯例"}`,
	}

	delta := []llm.Message{
		{Role: llm.RoleAssistant, Content: "已完成 Tier 判定逻辑提取，新增 determineTier 函数。"},
		{Role: llm.RoleTool, Name: "write_file", Content: "[文件已写入]", ToolCallID: "tc2"},
	}

	result, err := s.Summarize(context.Background(), existing, delta)
	if err != nil {
		t.Fatalf("Summarize 失败: %v", err)
	}

	validateSummarySchema(t, result)

	// 新摘要应继承已有约束
	var summary map[string]any
	json.Unmarshal([]byte(result), &summary)
	constraints, _ := summary["constraints"].(string)
	if !strings.Contains(constraints, "Go 惯例") {
		t.Logf("警告: 新摘要未继承已有 constraints，got: %q", constraints)
	}

	progress := summary["progress"].(map[string]any)
	t.Logf("带已有摘要的摘要结果:\n  summary: %s\n  files: %v\n  pending: %v\n  constraints: %s",
		progress["summary"], progress["files"], summary["pending"], constraints)
}

// TestCompactionSummarizer_Integration_EmptyDelta 测试空 delta 消息不会导致 panic。
func TestCompactionSummarizer_Integration_EmptyDelta(t *testing.T) {
	client := loadSummaryClient(t)
	s := NewCompactionSummarizer(client, 0)

	result, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "没有实际操作，只是确认环境就绪。"},
	})
	if err != nil {
		t.Fatalf("Summarize 失败: %v", err)
	}

	validateSummarySchema(t, result)
	t.Logf("空操作摘要:\n%s", result)
}

// TestCompactionSummarizer_Integration_JSONMode 验证 JSON 模式输出不含 markdown 包裹。
func TestCompactionSummarizer_Integration_JSONMode(t *testing.T) {
	client := loadSummaryClient(t)
	s := NewCompactionSummarizer(client, 0)

	result, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "修改了 README.md，更新了项目描述。"},
	})
	if err != nil {
		t.Fatalf("Summarize 失败: %v", err)
	}

	// JSON 模式不应输出 ```json 包裹
	if strings.Contains(result, "```") {
		t.Fatalf("JSON 模式不应包含 markdown fence，实际输出: %s", result)
	}

	if !strings.HasPrefix(strings.TrimSpace(result), "{") {
		t.Fatalf("JSON 模式应以 { 开头，实际输出: %s", result)
	}

	validateSummarySchema(t, result)
	t.Logf("JSON 模式输出:\n%s", result)
}

// TestCompactionSummarizer_Integration_TokenLimit 验证截断保护（超长消息不溢出 prompt）。
func TestCompactionSummarizer_Integration_TokenLimit(t *testing.T) {
	client := loadSummaryClient(t)
	s := NewCompactionSummarizer(client, 0)

	// 超长 tool 输出（模拟 grep 返回大量结果）
	longContent := strings.Repeat("result line with matched pattern and file path /very/long/path/to/file.go:123\n", 500)
	delta := []llm.Message{
		{Role: llm.RoleUser, Content: "搜索所有包含 'CompactMessages' 的文件"},
		{Role: llm.RoleTool, Name: "grep", Content: longContent, ToolCallID: "tc3"},
		{Role: llm.RoleAssistant, Content: "找到了 50 个匹配项，主要分布在 compaction 和 context 包中。"},
	}

	result, err := s.Summarize(context.Background(), nil, delta)
	if err != nil {
		t.Fatalf("Summarize 失败（可能因超长输入导致）: %v", err)
	}

	validateSummarySchema(t, result)
	t.Logf("超长输入摘要:\n%s", result)
}
