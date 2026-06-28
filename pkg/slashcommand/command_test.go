package slashcommand

import (
	"context"
	"errors"
	"strings"
	"testing"

	"waveloom/pkg/llm"
)

// --- Mocks ---

type mockSessionCreator struct {
	err error
}

func (m *mockSessionCreator) NewSession() error { return m.err }

type mockSettingsStore struct {
	settings  *llm.LLMSettings
	loadErr   error
	savedSettings *llm.LLMSettings
	saveErr      error
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
	cmd := NewNewCommand(creator)
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
	cmd := NewNewCommand(creator)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "创建新 session 失败") {
		t.Errorf("Text = %q, should contain error", result.Text)
	}
}

func TestNewCommandAliases(t *testing.T) {
	cmd := NewNewCommand(nil)
	if cmd.Name() != "new" {
		t.Errorf("Name = %q, want new", cmd.Name())
	}
	aliases := cmd.Aliases()
	if len(aliases) != 1 || aliases[0] != "clear" {
		t.Errorf("Aliases = %v, want [clear]", aliases)
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
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro")
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
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro")
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
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro")
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
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro")
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
	cmd := NewModelCommand(nil, lister, "deepseek-v4-pro")
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
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro")
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
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro")
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
	cmd := NewThemeCommand()
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
	cmd := NewHelpCommand(r)
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
	r.Register(NewThemeCommand())
	r.Register(NewNewCommand(nil))
	r.Register(NewHelpCommand(r))

	cmd := NewHelpCommand(r)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "使用技巧") {
		t.Error("help should contain usage tips")
	}
	if !strings.Contains(result.Text, "wvl --continue") {
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
