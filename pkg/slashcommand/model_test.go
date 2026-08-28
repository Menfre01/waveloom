package slashcommand

import (
	"context"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// TestModelCommandProPlan 验证 /model proplan 跳过模型列表校验,
// 持久化到 profiles.<provider>.curr_model,不修改 model 锚点。
func TestModelCommandProPlan(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			SubModel: "deepseek-v4-flash",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
			},
		},
		projectSettings: &llm.LLMSettings{
			Provider: "deepseek",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
			},
		},
	}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-pro"}, {ID: "deepseek-v4-flash"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

	result, err := cmd.Execute(context.Background(), "proplan")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Text == "" {
		t.Error("expected success text")
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectModelSwitched {
		t.Fatalf("SideEffects = %+v, want [ModelSwitched]", result.SideEffects)
	}
	if result.SideEffects[0].Detail != "proplan" {
		t.Errorf("SideEffect Detail = %q, want proplan", result.SideEffects[0].Detail)
	}

	saved := store.savedSettings
	if saved == nil {
		t.Fatal("SaveLLM not called")
	}
	// 写入当前 provider 的 profile.curr_model
	if saved.Profiles["deepseek"].CurrModel != llm.ModelChoiceProPlan {
		t.Errorf("profile curr_model = %q, want %q", saved.Profiles["deepseek"].CurrModel, llm.ModelChoiceProPlan)
	}
	// model 锚点不被修改
	if saved.Profiles["deepseek"].Model != "deepseek-v4-pro" {
		t.Errorf("profile model = %q, want deepseek-v4-pro (anchor untouched)", saved.Profiles["deepseek"].Model)
	}
}

// TestModelCommandUnknownRejected 验证未知模型仍被列表校验拒绝。
func TestModelCommandUnknownRejected(t *testing.T) {
	store := &mockSettingsStore{settings: &llm.LLMSettings{Provider: "deepseek"}}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-pro"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

	result, err := cmd.Execute(context.Background(), "not-a-model")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Text == "" {
		t.Error("expected unknown-model error text")
	}
	if store.savedSettings != nil {
		t.Error("SaveLLM should not be called for unknown model")
	}
}

// TestModelCommandKnownWritesProfileCurrModel 验证已知模型写入 profile 的
// curr_model,不写顶层 model(行为变更:model 字段成为纯锚点)。
func TestModelCommandKnownWritesProfileCurrModel(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro"},
			},
		},
		projectSettings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro"},
			},
		},
	}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-flash"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

	if _, err := cmd.Execute(context.Background(), "deepseek-v4-flash"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	saved := store.savedSettings
	if saved.Profiles["deepseek"].CurrModel != "deepseek-v4-flash" {
		t.Errorf("profile curr_model = %q, want deepseek-v4-flash", saved.Profiles["deepseek"].CurrModel)
	}
	if saved.Model != "deepseek-v4-pro" {
		t.Errorf("top-level model = %q, want deepseek-v4-pro (anchor untouched)", saved.Model)
	}
	if saved.Profiles["deepseek"].Model != "deepseek-v4-pro" {
		t.Errorf("profile model = %q, want deepseek-v4-pro (anchor untouched)", saved.Profiles["deepseek"].Model)
	}
}

// TestModelCommandNoProviderDefaultsToDeepseekProfile 验证 provider 为空
// (未配置)时按 deepseek 默认写入 profiles.deepseek.curr_model,
// 不 panic、不创建 profiles[""]、不写顶层。
func TestModelCommandNoProviderDefaultsToDeepseekProfile(t *testing.T) {
	store := &mockSettingsStore{settings: &llm.LLMSettings{}}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "some-model"}}}
	cmd := NewModelCommand(store, lister, "", testMessagesZhCN())

	if _, err := cmd.Execute(context.Background(), "some-model"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	saved := store.savedSettings
	if saved.CurrModel != "" {
		t.Errorf("top-level curr_model = %q, want empty (must not leak to top level)", saved.CurrModel)
	}
	if saved.Profiles["deepseek"] == nil || saved.Profiles["deepseek"].CurrModel != "some-model" {
		t.Errorf("profiles.deepseek.curr_model = %+v, want some-model (empty provider defaults to deepseek)", saved.Profiles)
	}
	if _, ok := saved.Profiles[""]; ok {
		t.Error("must not create profile with empty provider key")
	}
}

// TestModelCommandAutoCreatesProfile 验证项目文件已有 provider 但无 profile
// 时,/model 自动创建骨架 profile(仅含 curr_model)。字段级 profile 合并
// (mergeProfileFields)下骨架不再覆盖全局配置,写顶层已无必要。
func TestModelCommandAutoCreatesProfile(t *testing.T) {
	store := &mockSettingsStore{
		settings:        &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro"},
		projectSettings: &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro"},
	}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-flash"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

	if _, err := cmd.Execute(context.Background(), "deepseek-v4-flash"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	saved := store.savedSettings
	if saved.CurrModel != "" {
		t.Errorf("top-level curr_model = %q, want empty (must not leak to top level)", saved.CurrModel)
	}
	prof := saved.Profiles["deepseek"]
	if prof == nil || prof.CurrModel != "deepseek-v4-flash" {
		t.Errorf("profiles.deepseek.curr_model = %+v, want deepseek-v4-flash (skeleton)", prof)
	}
	if prof.Model != "" || prof.APIKey != "" {
		t.Errorf("skeleton profile must only carry curr_model, got %+v", prof)
	}
	// 顶层锚点保留
	if saved.Model != "deepseek-v4-pro" {
		t.Errorf("top-level model = %q, want deepseek-v4-pro", saved.Model)
	}
}

// TestModelCommandProPlanRejectedWithoutAnchors 验证 /model proplan 在
// model/sub_model 锚点缺失时被拒绝(不写盘,防重启报错 / "proplan" 泄漏)。
func TestModelCommandProPlanRejectedWithoutAnchors(t *testing.T) {
	t.Run("missing sub_model", func(t *testing.T) {
		store := &mockSettingsStore{
			settings: &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro"},
		}
		lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-pro"}}}
		cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

		result, err := cmd.Execute(context.Background(), "proplan")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if result.Text == "" {
			t.Error("expected anchor-missing error text")
		}
		if store.savedSettings != nil {
			t.Error("SaveLLM must not be called when anchors are missing")
		}
	})

	t.Run("missing model", func(t *testing.T) {
		store := &mockSettingsStore{
			settings: &llm.LLMSettings{Provider: "deepseek", SubModel: "deepseek-v4-flash"},
		}
		lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-flash"}}}
		cmd := NewModelCommand(store, lister, "", testMessagesZhCN())

		if _, err := cmd.Execute(context.Background(), "proplan"); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if store.savedSettings != nil {
			t.Error("SaveLLM must not be called when anchors are missing")
		}
	})

	t.Run("anchor equal proplan", func(t *testing.T) {
		store := &mockSettingsStore{
			settings: &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro", SubModel: llm.ModelChoiceProPlan},
		}
		lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-pro"}}}
		cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

		if _, err := cmd.Execute(context.Background(), "proplan"); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if store.savedSettings != nil {
			t.Error("SaveLLM must not be called when anchor == proplan")
		}
	})
}

// TestRegression_ProPlanAnchorsFromProfile 回归:锚点只配置在 profile 内
// (顶层无 sub_model,Merge 白名单不含 SubModel)时,/model proplan 必须通过。
// 校验需 ResolveProfile 后判断,否则 profile 锚点不可见导致误拒。
func TestRegression_ProPlanAnchorsFromProfile(t *testing.T) {
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-flash", // 顶层残留 flash(会被 profile 覆盖)
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
			},
		},
		projectSettings: &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-flash",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
			},
		},
	}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-pro"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-flash", testMessagesZhCN())

	result, err := cmd.Execute(context.Background(), "proplan")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if store.savedSettings == nil {
		t.Fatalf("SaveLLM not called (proplan rejected): %q", result.Text)
	}
	if store.savedSettings.Profiles["deepseek"].CurrModel != llm.ModelChoiceProPlan {
		t.Errorf("profile curr_model = %q, want %q", store.savedSettings.Profiles["deepseek"].CurrModel, llm.ModelChoiceProPlan)
	}
	// 合并结果(profile 解析值)不写回顶层:原 settings 用于持久化
	if store.savedSettings.Model != "deepseek-v4-flash" {
		t.Errorf("top-level model = %q, want deepseek-v4-flash (merged profile must not be persisted)", store.savedSettings.Model)
	}
}

// TestRegression_ModelSwitchNoGlobalLeak 回归:双配置(全局+项目)下 /model
// 写回项目文件时,全局配置(其他 profile、api_key)不得被复制进项目文件。
func TestRegression_ModelSwitchNoGlobalLeak(t *testing.T) {
	// 合并结果(LoadLLM):deepseek 来自项目,openai 来自全局
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {APIKey: "sk-project", Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
				"openai":   {APIKey: "sk-global", Model: "gpt-4o"},
			},
		},
		// 项目文件只有 deepseek(全局 openai 不应被复制进来)
		projectSettings: &llm.LLMSettings{
			Provider: "deepseek",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {APIKey: "sk-project", Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
			},
		},
	}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-flash"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

	if _, err := cmd.Execute(context.Background(), "deepseek-v4-flash"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	saved := store.savedSettings
	if saved == nil {
		t.Fatal("SaveLLM not called")
	}
	// 项目文件不含全局的 openai profile(无泄漏)
	if saved.Profiles["openai"] != nil {
		t.Error("global openai profile leaked into project settings")
	}
	// 写入生效:deepseek profile 的 curr_model 更新
	if saved.Profiles["deepseek"].CurrModel != "deepseek-v4-flash" {
		t.Errorf("profile curr_model = %q, want deepseek-v4-flash", saved.Profiles["deepseek"].CurrModel)
	}
}

// TestRegression_ProPlanSkeletonWhenProjectNoProfile 回归:项目文件无该
// provider 的 profile 时,/model proplan 创建骨架 profile(profiles.<provider>.
// curr_model)——不再写顶层(顶层残留曾强制覆盖全局 provider 切换)。字段级
// profile 合并(mergeProfileFields)下骨架不会覆盖全局完整 profile。
func TestRegression_ProPlanSkeletonWhenProjectNoProfile(t *testing.T) {
	// 合并结果:deepseek profile 来自全局
	store := &mockSettingsStore{
		settings: &llm.LLMSettings{
			Provider: "deepseek",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {APIKey: "sk-global", Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"},
			},
		},
		// 项目文件只有顶层 provider,无 deepseek profile
		projectSettings: &llm.LLMSettings{Provider: "deepseek"},
	}
	lister := &mockModelLister{models: []llm.ModelInfo{{ID: "deepseek-v4-pro"}}}
	cmd := NewModelCommand(store, lister, "deepseek-v4-pro", testMessagesZhCN())

	if _, err := cmd.Execute(context.Background(), "proplan"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	saved := store.savedSettings
	if saved.CurrModel != "" {
		t.Errorf("top-level curr_model = %q, want empty (must not leak to top level)", saved.CurrModel)
	}
	prof := saved.Profiles["deepseek"]
	if prof == nil || prof.CurrModel != llm.ModelChoiceProPlan {
		t.Errorf("profiles.deepseek.curr_model = %+v, want %q (skeleton)", prof, llm.ModelChoiceProPlan)
	}
	if prof.APIKey != "" || prof.Model != "" {
		t.Errorf("skeleton profile must only carry curr_model, got %+v", prof)
	}
}
