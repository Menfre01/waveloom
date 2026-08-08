package main

import (
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

	t.Run("proplan missing sub_model rejected", func(t *testing.T) {
		s := &llm.LLMSettings{Model: "deepseek-v4-pro", CurrModel: llm.ModelChoiceProPlan}
		_, _, _, err := resolveModelChoice("", s)
		if err == nil {
			t.Fatal("expected error for missing sub_model anchor")
		}
	})

	t.Run("proplan missing model rejected", func(t *testing.T) {
		s := &llm.LLMSettings{SubModel: "deepseek-v4-flash", CurrModel: llm.ModelChoiceProPlan}
		_, _, _, err := resolveModelChoice("", s)
		if err == nil {
			t.Fatal("expected error for missing model anchor")
		}
	})

	t.Run("anchor equal proplan rejected", func(t *testing.T) {
		s := &llm.LLMSettings{
			Model:     "deepseek-v4-pro",
			SubModel:  llm.ModelChoiceProPlan, // 手改配置:sub_model 写成 proplan
			CurrModel: llm.ModelChoiceProPlan,
		}
		_, _, _, err := resolveModelChoice("", s)
		if err == nil {
			t.Fatal("expected error for anchor == proplan")
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
