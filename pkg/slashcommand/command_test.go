package slashcommand

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)
// --- Test helpers ---

// testMessagesZhCN 返回中文测试文案，与 cmd/waveloom/i18n.go 中 zhCN 保持一致。
func testMessagesZhCN() *SlashMessages {
	return &SlashMessages{
		NewDescription:        "创建全新 session",
		NewCreated:            "新 session 已创建。",
		NewFailed:             "创建新 session 失败: %v",
		ModelDescription:      "显示或切换模型",
		ModelListFailed:       "无法获取模型列表: %v",
		ModelListFailedNoNet:  "无法获取模型列表，请检查网络连接后重试。",
		ModelUnknown:          "未知模型: %s。输入 /model 查看可用列表。",
		ModelConfigReadFailed: "读取配置失败: %v",
		ModelConfigSaveFailed: "保存配置失败: %v",
		ModelSwitched:          "模型已切换为 %s。",
		ModelAdvisorModeNotice: "注意：当前为 advisor 模式。",
		ThemeDescription:       "选择主题（Auto / Dark / Light）",
		LocaleDescription:     "切换语言（zh-CN / en-US）",
		HelpDescription:       "显示所有可用命令",
		HelpText:              "使用技巧:\n\nwaveloom --continue",
	}
}

// --- Mocks ---

type mockSessionCreator struct {
	err error
}

func (m *mockSessionCreator) NewSession() error { return m.err }

type mockSettingsStore struct {
	settings      *llm.LLMSettings
	loadErr       error
	savedSettings *llm.LLMSettings
	saveErr       error
}

func (m *mockSettingsStore) LoadLLM() (*llm.LLMSettings, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.settings, nil
}

func (m *mockSettingsStore) SaveLLM(settings *llm.LLMSettings) error {
	m.savedSettings = settings
	return m.saveErr
}

type mockModelLister struct {
	models []llm.ModelInfo
	err    error
}

func (m *mockModelLister) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.models, nil
}

// --- /new Tests ---

func TestNewCommandSuccess(t *testing.T) {
	creator := &mockSessionCreator{}
	msg := testMessagesZhCN()
	cmd := NewNewCommand(creator, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "新 session 已创建。" {
		t.Errorf("Text = %q, want %q", result.Text, "新 session 已创建。")
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectSessionReset {
		t.Errorf("expected SideEffectSessionReset, got %+v", result.SideEffects)
	}
}

func TestNewCommandError(t *testing.T) {
	creator := &mockSessionCreator{err: errors.New("session dir not writable")}
	msg := testMessagesZhCN()
	cmd := NewNewCommand(creator, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "创建新 session 失败") {
		t.Errorf("Text = %q, should contain error", result.Text)
	}
}

func TestNewCommandAliases(t *testing.T) {
	msg := testMessagesZhCN()
	cmd := NewNewCommand(nil, msg)
	if cmd.Name() != "new" {
		t.Errorf("Name = %q, want new", cmd.Name())
	}
	aliases := cmd.Aliases()
	if len(aliases) != 1 || aliases[0] != "clear" {
		t.Errorf("Aliases = %v, want [clear]", aliases)
	}
}

// --- Getters — 验证 SlashMessages 注入后 getter 非空且不会 nil panic ---

func TestNewCommandGetters(t *testing.T) {
	msg := testMessagesZhCN()
	cmd := NewNewCommand(nil, msg)
	if cmd.Name() != "new" {
		t.Errorf("Name = %q, want new", cmd.Name())
	}
	if cmd.Description() != msg.NewDescription {
		t.Errorf("Description = %q, want %q", cmd.Description(), msg.NewDescription)
	}
	if cmd.ArgsPlaceholder() != "" {
		t.Errorf("ArgsPlaceholder = %q, want empty", cmd.ArgsPlaceholder())
	}
}

func TestModelCommandGetters(t *testing.T) {
	msg := testMessagesZhCN()
	cmd := NewModelCommand(nil, nil, "deepseek-v4", msg)
	if cmd.Name() != "model" {
		t.Errorf("Name = %q, want model", cmd.Name())
	}
	if cmd.Description() != msg.ModelDescription {
		t.Errorf("Description = %q, want %q", cmd.Description(), msg.ModelDescription)
	}
	if cmd.ArgsPlaceholder() != "model" {
		t.Errorf("ArgsPlaceholder = %q, want model", cmd.ArgsPlaceholder())
	}
	if aliases := cmd.Aliases(); aliases != nil {
		t.Errorf("Aliases = %v, want nil", aliases)
	}
}

func TestThemeCommandGetters(t *testing.T) {
	msg := testMessagesZhCN()
	cmd := NewThemeCommand(msg)
	if cmd.Description() != msg.ThemeDescription {
		t.Errorf("Description = %q, want %q", cmd.Description(), msg.ThemeDescription)
	}
	if cmd.ArgsPlaceholder() != "" {
		t.Errorf("ArgsPlaceholder = %q, want empty", cmd.ArgsPlaceholder())
	}
	if aliases := cmd.Aliases(); aliases != nil {
		t.Errorf("Aliases = %v, want nil", aliases)
	}
}

// --- /model Tests ---

func TestModelCommandNoArgsSuccess(t *testing.T) {
	lister := &mockModelLister{
		models: []llm.ModelInfo{
			{ID: "deepseek-v4-pro", Object: "model", OwnedBy: "deepseek"},
			{ID: "deepseek-v4-flash", Object: "model", OwnedBy: "deepseek"},
		},
	}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectOpenModelPicker {
		t.Errorf("expected SideEffectOpenModelPicker, got %+v", result.SideEffects)
	}
	if result.SideEffects[0].Detail == "" {
		t.Error("Detail should contain JSON serialized models")
	}
	// Detail should contain both model IDs
	if !strings.Contains(result.SideEffects[0].Detail, "deepseek-v4-pro") {
		t.Error("Detail should contain deepseek-v4-pro")
	}
}

func TestModelCommandNoArgsAPIError(t *testing.T) {
	lister := &mockModelLister{err: errors.New("network timeout")}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "无法获取模型列表") {
		t.Errorf("Text = %q, should contain error message", result.Text)
	}
	if len(result.SideEffects) != 0 {
		t.Errorf("expected no side effects on error, got %+v", result.SideEffects)
	}
}

func TestModelCommandWithArgsSuccess(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{Model: "deepseek-v4-pro", Provider: "deepseek"},
	}
	lister := &mockModelLister{
		models: []llm.ModelInfo{
			{ID: "deepseek-v4-pro", Object: "model", OwnedBy: "deepseek"},
			{ID: "deepseek-v4-flash", Object: "model", OwnedBy: "deepseek"},
		},
	}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "模型已切换为 deepseek-v4-flash") {
		t.Errorf("Text = %q, should contain switch message", result.Text)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectModelSwitched {
		t.Errorf("expected SideEffectModelSwitched, got %+v", result.SideEffects)
	}
	if result.SideEffects[0].Detail != "deepseek-v4-flash" {
		t.Errorf("SideEffect Detail = %q, want deepseek-v4-flash", result.SideEffects[0].Detail)
	}
	if store.savedSettings == nil {
		t.Fatal("SaveLLM should have been called")
	}
	if store.savedSettings.Model != "deepseek-v4-flash" {
		t.Errorf("saved Model = %q, want deepseek-v4-flash", store.savedSettings.Model)
	}
}

func TestModelCommandWithArgsAPIError(t *testing.T) {
	lister := &mockModelLister{err: errors.New("network timeout")}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "无法获取模型列表，请检查网络连接后重试") {
		t.Errorf("Text = %q, should contain retry message", result.Text)
	}
	if len(result.SideEffects) != 0 {
		t.Errorf("expected no side effects on API error, got %+v", result.SideEffects)
	}
}

func TestModelCommandWithArgsUnknownModel(t *testing.T) {
	lister := &mockModelLister{
		models: []llm.ModelInfo{
			{ID: "deepseek-v4-pro", Object: "model", OwnedBy: "deepseek"},
		},
	}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "gpt-nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "未知模型") {
		t.Errorf("Text = %q, should contain 'unknown model'", result.Text)
	}
	if len(result.SideEffects) != 0 {
		t.Errorf("expected no side effects for unknown model, got %+v", result.SideEffects)
	}
}

func TestModelCommandWithArgsLoadError(t *testing.T) {
	store := &mockSettingsStore{loadErr: errors.New("file read error")}
	lister := &mockModelLister{
		models: []llm.ModelInfo{
			{ID: "deepseek-v4-flash", Object: "model", OwnedBy: "deepseek"},
		},
	}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "读取配置失败") {
		t.Errorf("Text = %q, should contain load error", result.Text)
	}
}

func TestModelCommandWithArgsSaveError(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{Model: "deepseek-v4-pro"},
		saveErr:  errors.New("disk full"),
	}
	lister := &mockModelLister{
		models: []llm.ModelInfo{
			{ID: "deepseek-v4-flash", Object: "model", OwnedBy: "deepseek"},
		},
	}
	msg := testMessagesZhCN()
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", msg)
	result, err := cmd.Execute(context.Background(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "保存配置失败") {
		t.Errorf("Text = %q, should contain save error", result.Text)
	}
}

// --- /theme Tests ---

func TestThemeCommand(t *testing.T) {
	msg := testMessagesZhCN()
	cmd := NewThemeCommand(msg)
	if cmd.Name() != "theme" {
		t.Errorf("Name = %q, want theme", cmd.Name())
	}
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectOpenThemePicker {
		t.Errorf("expected SideEffectOpenThemePicker, got %+v", result.SideEffects)
	}
	if result.Text != "" {
		t.Errorf("Text should be empty, got %q", result.Text)
	}
}

// --- /help Tests ---

func TestHelpCommandEmpty(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "使用技巧") {
		t.Errorf("Text = %q, should contain usage tips", result.Text)
	}
}

func TestHelpCommandWithCommands(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	r.Register(NewThemeCommand(msg))
	r.Register(NewNewCommand(nil, msg))
	r.Register(NewHelpCommand(r, msg))

	cmd := NewHelpCommand(r, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "使用技巧") {
		t.Error("help should contain usage tips")
	}
	if !strings.Contains(result.Text, "waveloom --continue") {
		t.Error("help should mention session resume")
	}
}

// --- modelInList ---

func TestModelInList(t *testing.T) {
	models := []llm.ModelInfo{
		{ID: "deepseek-v4-pro"},
		{ID: "gpt-4o"},
	}
	if !modelInList(models, "deepseek-v4-pro") {
		t.Error("modelInList should return true for existing model")
	}
	if modelInList(models, "nonexistent") {
		t.Error("modelInList should return false for missing model")
	}
	if modelInList(nil, "any") {
		t.Error("modelInList should return false for nil list")
	}
}

// --- /provider Tests ---

func TestProviderCommandGetters(t *testing.T) {
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(nil, msg)
	if cmd.Name() != "provider" {
		t.Errorf("Name = %q, want provider", cmd.Name())
	}
	if cmd.Description() != msg.ProviderDescription {
		t.Errorf("Description = %q, want %q", cmd.Description(), msg.ProviderDescription)
	}
	if cmd.ArgsPlaceholder() != "provider" {
		t.Errorf("ArgsPlaceholder = %q, want provider", cmd.ArgsPlaceholder())
	}
	if aliases := cmd.Aliases(); aliases != nil {
		t.Errorf("Aliases = %v, want nil", aliases)
	}
}

func TestProviderCommandNoArgsWithProfiles(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro", BaseURL: "https://api.deepseek.com"},
				"openai":   {Model: "gpt-4o", BaseURL: "https://api.openai.com/v1"},
			},
		},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "" {
		t.Errorf("Text should be empty when profiles exist, got %q", result.Text)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectOpenProviderPicker {
		t.Fatalf("expected SideEffectOpenProviderPicker, got %+v", result.SideEffects)
	}
	// Verify JSON payload contains both providers
	var infos []providerInfo
	if err := json.Unmarshal([]byte(result.SideEffects[0].Detail), &infos); err != nil {
		t.Fatalf("failed to unmarshal provider JSON: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(infos))
	}
	// Verify deepseek is current
	foundCurrent := false
	for _, info := range infos {
		if info.Name == "deepseek" && info.Current {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Error("deepseek should be marked as current")
	}
}

func TestProviderCommandNoArgsProfileWithoutDetail(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "kimi",
			Profiles: map[string]*llm.LLMSettings{
				"kimi": {},
			},
		},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectOpenProviderPicker {
		t.Fatalf("expected SideEffectOpenProviderPicker, got %+v", result.SideEffects)
	}
}

func TestProviderCommandNoArgsCurrentNotInProfiles(t *testing.T) {
	// Current provider set but not in profiles — should still be listed in JSON
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			Profiles: map[string]*llm.LLMSettings{
				"openai": {Model: "gpt-4o"},
			},
		},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectOpenProviderPicker {
		t.Fatalf("expected SideEffectOpenProviderPicker, got %+v", result.SideEffects)
	}
	var infos []providerInfo
	if err := json.Unmarshal([]byte(result.SideEffects[0].Detail), &infos); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 providers (current + profiled), got %d", len(infos))
	}
}
func TestProviderCommandNoArgsNoProfiles(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != msg.ProviderNoProfiles {
		t.Errorf("Text = %q, want %q", result.Text, msg.ProviderNoProfiles)
	}
}

func TestProviderCommandNoArgsLoadError(t *testing.T) {
	store := &mockSettingsStore{loadErr: errors.New("file read error")}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "读取配置失败") {
		t.Errorf("Text = %q, should contain load error", result.Text)
	}
}

func TestProviderCommandWithArgsFromProfile(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			Profiles: map[string]*llm.LLMSettings{
				"openai": {Model: "gpt-4o", BaseURL: "https://api.openai.com/v1"},
			},
		},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "deepseek") && !strings.Contains(result.Text, "openai") {
		t.Error("result should mention old and new provider")
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectProviderSwitched {
		t.Errorf("expected SideEffectProviderSwitched, got %+v", result.SideEffects)
	}
	if result.SideEffects[0].Detail != "openai" {
		t.Errorf("SideEffect Detail = %q, want openai", result.SideEffects[0].Detail)
	}
	if store.savedSettings == nil {
		t.Fatal("SaveLLM should have been called")
	}
	if store.savedSettings.Provider != "openai" {
		t.Errorf("saved Provider = %q, want openai", store.savedSettings.Provider)
	}
}

func TestProviderCommandWithArgsKnownProviderNoProfile(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
		},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "kimi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectProviderSwitched {
		t.Errorf("expected SideEffectProviderSwitched, got %+v", result.SideEffects)
	}
	if store.savedSettings.Provider != "kimi" {
		t.Errorf("saved Provider = %q, want kimi", store.savedSettings.Provider)
	}
}

func TestProviderCommandWithArgsUnknown(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
		},
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "unknown-provider")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "未知 Provider") {
		t.Errorf("Text = %q, should contain 'unknown provider'", result.Text)
	}
	if len(result.SideEffects) != 0 {
		t.Errorf("expected no side effects for unknown provider, got %+v", result.SideEffects)
	}
}

func TestProviderCommandWithArgsSaveError(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
		},
		saveErr: errors.New("disk full"),
	}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "保存配置失败") {
		t.Errorf("Text = %q, should contain save error", result.Text)
	}
}

func TestProviderCommandWithArgsLoadError(t *testing.T) {
	store := &mockSettingsStore{loadErr: errors.New("file read error")}
	msg := testMessagesZhCNFull()
	cmd := NewProviderCommand(store, msg)
	result, err := cmd.Execute(context.Background(), "openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "读取配置失败") {
		t.Errorf("Text = %q, should contain load error", result.Text)
	}
}

// testMessagesZhCNFull 返回包含 provider 相关字段的完整中文测试文案。
func testMessagesZhCNFull() *SlashMessages {
	m := testMessagesZhCN()
	m.ProviderDescription = "显示或切换 LLM Provider（kimi / deepseek / openai）"
	m.ProviderList = "当前 Provider: %s（模型: %s）"
	m.ProviderAvailable = "可用 Provider:\n%s"
	m.ProviderUnknown = "未知 Provider: %s。可用: kimi, deepseek, openai"
	m.ProviderSwitched = "Provider 已从 %s 切换到 %s。"
	m.ProviderNoProfiles = "配置文件未设置 provider profiles。请先通过设置向导配置。"
	m.ProviderNotConfigured = "(未配置)"
	m.ProviderConfigReadFailed = "读取配置失败: %v"
	m.ProviderConfigSaveFailed = "保存配置失败: %v"
	m.ProviderModelNotice = "当前模型: %s。如有需要请执行 /model 切换。"
	return m
}
