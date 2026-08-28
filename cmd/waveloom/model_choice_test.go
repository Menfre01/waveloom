package main

import (
	"encoding/json"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// TestApplyCliModel 验证 --model 覆盖逻辑:proplan 特殊值不覆盖 model 锚点,
// 普通模型名正常覆盖。
func TestApplyCliModel(t *testing.T) {
	t.Run("proplan does not overwrite anchor", func(t *testing.T) {
		merged := &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash"}
		applyCliModel(merged, llm.ModelChoiceProPlan)
		if merged.Model != "deepseek-v4-pro" {
			t.Errorf("Model = %q, want deepseek-v4-pro (anchor must survive proplan)", merged.Model)
		}
	})

	t.Run("normal model overwrites", func(t *testing.T) {
		merged := &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro"}
		applyCliModel(merged, "deepseek-v4-flash")
		if merged.Model != "deepseek-v4-flash" {
			t.Errorf("Model = %q, want deepseek-v4-flash", merged.Model)
		}
	})

	t.Run("empty cli model is no-op", func(t *testing.T) {
		merged := &llm.LLMSettings{Provider: "deepseek", Model: "deepseek-v4-pro"}
		applyCliModel(merged, "")
		if merged.Model != "deepseek-v4-pro" {
			t.Errorf("Model = %q, want deepseek-v4-pro", merged.Model)
		}
	})
}

// TestResolveModelChoice 验证选择值解析优先级(--model > curr_model > model)
// 与 proplan 锚点校验。
func TestResolveModelChoice(t *testing.T) {
	t.Run("default uses curr_model", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro", CurrModel: "deepseek-v4-flash"}
		choice, plan, sub, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "deepseek-v4-flash" {
			t.Errorf("choice = %q, want deepseek-v4-flash", choice)
		}
		if plan != "deepseek-v4-pro" || sub != "" {
			t.Errorf("anchors = (%q, %q), want (deepseek-v4-pro, empty)", plan, sub)
		}
	})

	t.Run("empty curr_model falls back to model", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro"}
		choice, _, _, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "deepseek-v4-pro" {
			t.Errorf("choice = %q, want deepseek-v4-pro", choice)
		}
	})

	t.Run("cli model overrides curr_model", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro", CurrModel: "deepseek-v4-flash"}
		choice, _, _, err := resolveModelChoice("gpt-4o", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "gpt-4o" {
			t.Errorf("choice = %q, want gpt-4o", choice)
		}
	})

	t.Run("proplan with anchors ok", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro", SubModel: "deepseek-v4-flash", CurrModel: llm.ModelChoiceProPlan}
		choice, plan, sub, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != llm.ModelChoiceProPlan {
			t.Errorf("choice = %q, want %q", choice, llm.ModelChoiceProPlan)
		}
		if plan != "deepseek-v4-pro" || sub != "deepseek-v4-flash" {
			t.Errorf("anchors = (%q, %q), want (deepseek-v4-pro, deepseek-v4-flash)", plan, sub)
		}
	})

	t.Run("curr_model proplan with missing sub_model falls back to model", func(t *testing.T) {
		// 配置残留(非 CLI 显式):回退到 model,不阻断启动
		s := &llm.LLMSettings{Model: "deepseek-v4-pro", CurrModel: llm.ModelChoiceProPlan}
		choice, plan, sub, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "deepseek-v4-pro" {
			t.Errorf("choice = %q, want deepseek-v4-pro (fallback)", choice)
		}
		if plan != "deepseek-v4-pro" || sub != "" {
			t.Errorf("anchors = (%q, %q), want (deepseek-v4-pro, empty)", plan, sub)
		}
	})

	t.Run("curr_model proplan with missing model falls back to empty", func(t *testing.T) {
		s := &llm.LLMSettings{SubModel: "deepseek-v4-flash", CurrModel: llm.ModelChoiceProPlan}
		choice, _, _, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "" {
			t.Errorf("choice = %q, want empty (client default)", choice)
		}
	})

	t.Run("curr_model proplan with self-referencing anchor falls back", func(t *testing.T) {
		s := &llm.LLMSettings{
			Model:     "deepseek-v4-pro",
			SubModel:  llm.ModelChoiceProPlan, // 手改配置:sub_model 写成 proplan
			CurrModel: llm.ModelChoiceProPlan,
		}
		choice, _, _, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "deepseek-v4-pro" {
			t.Errorf("choice = %q, want deepseek-v4-pro (fallback, not proplan)", choice)
		}
	})

	t.Run("curr_model proplan with all anchors missing falls back to empty", func(t *testing.T) {
		s := &llm.LLMSettings{CurrModel: llm.ModelChoiceProPlan}
		choice, _, _, err := resolveModelChoice("", s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if choice != "" {
			t.Errorf("choice = %q, want empty (client default)", choice)
		}
	})

	t.Run("cli proplan rejected when anchors missing", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro"} // sub_model 缺失
		_, _, _, err := resolveModelChoice(llm.ModelChoiceProPlan, s)
		if err == nil {
			t.Fatal("expected error for --model proplan with missing sub_model")
		}
	})
}

// TestPersistModelChoice 验证模型选择持久化位置:始终写
// profiles.<provider>.curr_model,绝不写顶层(curr_model 残留曾导致
// 项目级强制模型选择覆盖全局 provider 切换)。
func TestPersistModelChoice(t *testing.T) {
	t.Run("writes to existing profile", func(t *testing.T) {
		s := &llm.LLMSettings{
			Provider: "deepseek",
			Model:    "deepseek-v4-pro",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro"},
			},
		}
		persistModelChoice(s, llm.ModelChoiceProPlan)
		if s.CurrModel != "" {
			t.Errorf("top-level CurrModel = %q, want empty (must not leak to top level)", s.CurrModel)
		}
		if got := s.Profiles["deepseek"].CurrModel; got != llm.ModelChoiceProPlan {
			t.Errorf("profiles.deepseek.curr_model = %q, want %q", got, llm.ModelChoiceProPlan)
		}
		if s.Profiles["deepseek"].Model != "deepseek-v4-pro" {
			t.Errorf("profile anchor Model changed to %q, want deepseek-v4-pro", s.Profiles["deepseek"].Model)
		}
	})

	t.Run("creates profile skeleton when missing", func(t *testing.T) {
		s := &llm.LLMSettings{Provider: "glm", Model: "glm-4.6"}
		persistModelChoice(s, llm.ModelChoiceProPlan)
		if s.CurrModel != "" {
			t.Errorf("top-level CurrModel = %q, want empty", s.CurrModel)
		}
		p := s.Profiles["glm"]
		if p == nil {
			t.Fatal("profiles.glm not created")
		}
		if p.CurrModel != llm.ModelChoiceProPlan {
			t.Errorf("profiles.glm.curr_model = %q, want %q", p.CurrModel, llm.ModelChoiceProPlan)
		}
		if p.Model != "" || p.SubModel != "" || p.APIKey != "" {
			t.Errorf("skeleton profile must only carry curr_model, got %+v", p)
		}
	})

	t.Run("empty provider defaults to deepseek", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-flash"}
		persistModelChoice(s, "deepseek-v4-flash")
		if s.Profiles["deepseek"] == nil || s.Profiles["deepseek"].CurrModel != "deepseek-v4-flash" {
			t.Errorf("empty provider must default to profiles.deepseek, got %+v", s.Profiles)
		}
	})

	t.Run("serialized shape only carries curr_model in profile", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro"}
		persistModelChoice(s, llm.ModelChoiceProPlan)
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var back struct {
			CurrModel string                     `json:"curr_model"`
			Profiles  map[string]*llm.LLMSettings `json:"profiles"`
		}
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.CurrModel != "" {
			t.Errorf("serialized top-level curr_model = %q, want empty", back.CurrModel)
		}
		if back.Profiles["deepseek"] == nil || back.Profiles["deepseek"].CurrModel != llm.ModelChoiceProPlan {
			t.Errorf("serialized profiles.deepseek.curr_model missing, got %+v", back.Profiles)
		}
	})
}

// TestPersistEffortSwitch 验证思考档位持久化位置:始终写
// profiles.<provider>.extra_params,绝不写顶层(与 persistModelChoice 同源缺陷:
// 顶层残留档位曾导致切换 provider 后档位仍生效)。
func TestPersistEffortSwitch(t *testing.T) {
	t.Run("writes to existing profile extra_params", func(t *testing.T) {
		s := &llm.LLMSettings{
			Provider: "deepseek",
			ExtraParams: map[string]any{
				"thinking": map[string]any{"type": "enabled"},
			},
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {Model: "deepseek-v4-pro"},
			},
		}
		persistEffortSwitch(s, "high")
		if s.ExtraParams["reasoning_effort"] != nil {
			t.Errorf("top-level reasoning_effort set: %v (must not leak to top level)", s.ExtraParams)
		}
		p := s.Profiles["deepseek"]
		if p.ExtraParams["reasoning_effort"] != "high" {
			t.Errorf("profiles.deepseek.reasoning_effort = %v, want high", p.ExtraParams["reasoning_effort"])
		}
		if p.Model != "deepseek-v4-pro" {
			t.Errorf("profile anchor Model changed to %q", p.Model)
		}
	})

	t.Run("creates profile skeleton when missing", func(t *testing.T) {
		s := &llm.LLMSettings{Provider: "glm", Model: "glm-4.6"}
		persistEffortSwitch(s, "medium")
		p := s.Profiles["glm"]
		if p == nil {
			t.Fatal("profiles.glm not created")
		}
		if p.ExtraParams["reasoning_effort"] != "medium" {
			t.Errorf("profiles.glm.reasoning_effort = %v, want medium", p.ExtraParams["reasoning_effort"])
		}
		if p.Model != "" || p.CurrModel != "" || p.APIKey != "" {
			t.Errorf("skeleton profile must only carry extra_params, got %+v", p)
		}
		if s.ExtraParams != nil {
			t.Errorf("top-level ExtraParams = %v, want nil", s.ExtraParams)
		}
	})

	t.Run("off clears reasoning_effort and disables thinking", func(t *testing.T) {
		s := &llm.LLMSettings{
			Provider: "deepseek",
			Profiles: map[string]*llm.LLMSettings{
				"deepseek": {
					ExtraParams: map[string]any{
						"thinking":         map[string]any{"type": "enabled"},
						"reasoning_effort": "max",
					},
				},
			},
		}
		persistEffortSwitch(s, "off")
		p := s.Profiles["deepseek"]
		if p.ExtraParams["reasoning_effort"] != nil {
			t.Errorf("reasoning_effort = %v, want removed on off", p.ExtraParams["reasoning_effort"])
		}
		thinking, ok := p.ExtraParams["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" {
			t.Errorf("thinking = %v, want disabled", p.ExtraParams["thinking"])
		}
	})

	t.Run("empty provider defaults to deepseek", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-flash"}
		persistEffortSwitch(s, "low")
		p := s.Profiles["deepseek"]
		if p == nil || p.ExtraParams["reasoning_effort"] != "low" {
			t.Errorf("empty provider must default to profiles.deepseek, got %+v", s.Profiles)
		}
	})

	t.Run("serialized shape keeps top level clean", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro"}
		persistEffortSwitch(s, "max")
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var back struct {
			ExtraParams map[string]any                `json:"extra_params"`
			Profiles    map[string]*llm.LLMSettings   `json:"profiles"`
		}
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.ExtraParams != nil {
			t.Errorf("serialized top-level extra_params = %v, want absent", back.ExtraParams)
		}
		if back.Profiles["deepseek"] == nil || back.Profiles["deepseek"].ExtraParams["reasoning_effort"] != "max" {
			t.Errorf("serialized profiles.deepseek.extra_params missing, got %+v", back.Profiles)
		}
	})
}
