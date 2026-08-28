package llm

import (
	"testing"
)

// TestRegression_ProjectSkeletonKeepsGlobalProfile 验证:home 全局为 profile 形态
// 配置(顶层无 api_key/model),项目 /model 切换生成的骨架 profile(仅 curr_model)
// 合并后不丢失全局 profile 的 api_key/model/base_url/extra_params。
//
// REGRESSION: 原实现 Profiles 合并为整对象替换,骨架覆盖全局同名 profile 导致
// 纯 profile 形态配置下启动 api_key 缺失;字段级合并后骨架只携带 curr_model,
// 其余字段保留全局值。
func TestRegression_ProjectSkeletonKeepsGlobalProfile(t *testing.T) {
	global := &LLMSettings{
		Provider: "deepseek",
		Profiles: map[string]*LLMSettings{
			"deepseek": {
				APIKey:   "sk-deepseek",
				Model:    "deepseek-v4-pro",
				SubModel: "deepseek-v4-flash",
				BaseURL:  "https://api.deepseek.com",
				ExtraParams: map[string]any{
					"thinking":         map[string]any{"type": "enabled"},
					"reasoning_effort": "max",
				},
			},
			"glm": {
				APIKey:  "sk-glm",
				Model:   "glm-4.6",
				BaseURL: "https://open.bigmodel.cn/api/paas/v4",
			},
		},
	}

	// 模拟项目无 .waveloom → /model deepseek-v4-flash 后生成的骨架文件
	project := &LLMSettings{
		Profiles: map[string]*LLMSettings{
			"deepseek": {CurrModel: "deepseek-v4-flash"},
		},
	}

	merged := MergeLLMSettings(global, project)
	merged.ResolveProfile()

	if merged.APIKey != "sk-deepseek" {
		t.Errorf("APIKey = %q, want sk-deepseek (global profile field lost)", merged.APIKey)
	}
	if merged.Model != "deepseek-v4-pro" {
		t.Errorf("Model = %q, want deepseek-v4-pro", merged.Model)
	}
	if merged.SubModel != "deepseek-v4-flash" {
		t.Errorf("SubModel = %q, want deepseek-v4-flash", merged.SubModel)
	}
	if merged.BaseURL != "https://api.deepseek.com" {
		t.Errorf("BaseURL = %q, want api.deepseek.com", merged.BaseURL)
	}
	if merged.ExtraParams == nil || merged.ExtraParams["reasoning_effort"] != "max" {
		t.Errorf("ExtraParams = %v, want reasoning_effort=max preserved", merged.ExtraParams)
	}
	// 骨架的 curr_model 必须生效(项目选择优先)
	if merged.CurrModel != "deepseek-v4-flash" {
		t.Errorf("CurrModel = %q, want deepseek-v4-flash (project skeleton must win)", merged.CurrModel)
	}
	// 其他 profile 不受影响
	if merged.Profiles["glm"] == nil || merged.Profiles["glm"].Model != "glm-4.6" {
		t.Errorf("profiles.glm = %+v, want preserved", merged.Profiles["glm"])
	}
}

// TestMergeLLMSettings_ProfileFieldMerge 验证同名 profile 字段级合并语义:
// override 非空字段覆盖,空字段保留 base(与顶层标量合并一致)。
func TestMergeLLMSettings_ProfileFieldMerge(t *testing.T) {
	base := &LLMSettings{
		Profiles: map[string]*LLMSettings{
			"deepseek": {
				APIKey:   "sk-base",
				Model:    "base-model",
				BaseURL:  "https://base.example.com",
				CurrModel: "base-choice",
			},
		},
	}
	override := &LLMSettings{
		Profiles: map[string]*LLMSettings{
			"deepseek": {Model: "override-model"}, // 只改 Model,其余留空
		},
	}

	got := MergeLLMSettings(base, override)
	ds := got.Profiles["deepseek"]
	if ds == nil {
		t.Fatal("profiles.deepseek missing")
	}
	if ds.Model != "override-model" {
		t.Errorf("Model = %q, want override-model", ds.Model)
	}
	if ds.APIKey != "sk-base" {
		t.Errorf("APIKey = %q, want sk-base (empty override field must keep base)", ds.APIKey)
	}
	if ds.BaseURL != "https://base.example.com" {
		t.Errorf("BaseURL = %q, want base URL preserved", ds.BaseURL)
	}
	if ds.CurrModel != "base-choice" {
		t.Errorf("CurrModel = %q, want base-choice preserved", ds.CurrModel)
	}
}
