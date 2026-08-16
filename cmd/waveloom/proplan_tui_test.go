package main

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/session"
)

// newProPlanTUIModel 构造启用 proplan 语义的最小 TUI model。
func newProPlanTUIModel() *model {
	m := &model{
		lc:          &enUS,
		input:       textarea.New(),
		keys:        makeKeyMap(&enUS),
		width:       80,
		height:      24,
		llmClient:   &mockLLMClient{},
		cm:          session.New(buildSystemPrompt("", LocaleEnUS)),
		modelChoice: llm.ModelChoiceProPlan,
		planModel:   "pro-x",
		subModel:    "flash-x",
	}
	return m
}

// TestProPlanTUI_PlanModeEnterSwitchesHud 验证 plan 进入时 footer 切换为
// plan 模型,并补存 PlanFile(wireLoop 重建后 RestorePlanMode 用)。
func TestProPlanTUI_PlanModeEnterSwitchesHud(t *testing.T) {
	m := newProPlanTUIModel()
	m.hudModel = "flash-x"
	m.inPlanMode = false

	_, _ = m.Update(agentloop.PlanModeEnter{PlanFile: "plan.md", PairID: "ab12"})

	if m.hudModel != "pro-x" {
		t.Errorf("hudModel = %q, want pro-x (plan model)", m.hudModel)
	}
	if m.planFile != "plan.md" {
		t.Errorf("planFile = %q, want plan.md (must be stored for wireLoop restore)", m.planFile)
	}
	if !m.inPlanMode {
		t.Error("inPlanMode should be true")
	}
}

// TestProPlanTUI_PlanModeExitRestoresHud 验证 plan 退出(审批通过)后 footer
// 恢复为日常模型(sub_model)。
func TestProPlanTUI_PlanModeExitRestoresHud(t *testing.T) {
	m := newProPlanTUIModel()
	m.hudModel = "pro-x"
	m.inPlanMode = true

	_, _ = m.Update(agentloop.PlanModeExit{Approved: true})

	if m.hudModel != "flash-x" {
		t.Errorf("hudModel = %q, want flash-x (daily model)", m.hudModel)
	}
	if m.inPlanMode {
		t.Error("inPlanMode should be false")
	}
}

// TestProPlanTUI_PlanModeExitRejectedKeepsPlanHud 验证 plan 退出被拒绝时
// 保持 plan 状态与模型显示。
func TestProPlanTUI_PlanModeExitRejectedKeepsPlanHud(t *testing.T) {
	m := newProPlanTUIModel()
	m.hudModel = "pro-x"
	m.inPlanMode = true

	_, _ = m.Update(agentloop.PlanModeExit{Approved: false})

	if m.hudModel != "pro-x" {
		t.Errorf("hudModel = %q, want pro-x (plan rejected, keep plan model)", m.hudModel)
	}
	if !m.inPlanMode {
		t.Error("inPlanMode should stay true when plan rejected")
	}
}

// TestProPlanTUI_WireLoopRestoresPlanMode 验证 wireLoop 重建(如 /model 切换)
// 后 plan 状态恢复:plan 中切换模型不会静默降级为日常模型。
func TestProPlanTUI_WireLoopRestoresPlanMode(t *testing.T) {
	m := newProPlanTUIModel()
	m.inPlanMode = true
	m.planFile = "plan.md"

	m.wireLoop()

	if m.loop == nil {
		t.Fatal("wireLoop did not create loop")
	}
	if !m.loop.InPlanMode() {
		t.Error("loop plan state lost after wireLoop rebuild")
	}
}

// TestProPlanTUI_WireLoopNotPlanKeepsNormal 验证非 plan 状态下 wireLoop 不注入 plan。
func TestProPlanTUI_WireLoopNotPlanKeepsNormal(t *testing.T) {
	m := newProPlanTUIModel()
	m.inPlanMode = false

	m.wireLoop()

	if m.loop == nil {
		t.Fatal("wireLoop did not create loop")
	}
	if m.loop.InPlanMode() {
		t.Error("loop should not be in plan mode")
	}
}

// TestProPlanTUI_ReconfigureRejectsMissingAnchors 验证 /model proplan 在
// 锚点缺失时被拒绝:modelChoice 保持不变,不切换。
func TestProPlanTUI_ReconfigureRejectsMissingAnchors(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := newProPlanTUIModel()
	m.settingsStore = &tuiSettingsStore{projectPath: projectPath}
	m.planModel = "" // 锚点缺失:model 空
	m.subModel = ""
	m.modelChoice = "deepseek-v4-pro"
	m.hudModel = "deepseek-v4-pro"

	m.reconfigureLLMClient(llm.ModelChoiceProPlan)

	if m.modelChoice != "deepseek-v4-pro" {
		t.Errorf("modelChoice = %q, want deepseek-v4-pro (proplan must be rejected)", m.modelChoice)
	}
	if m.hudModel != "deepseek-v4-pro" {
		t.Errorf("hudModel = %q, want deepseek-v4-pro (must not leak 'proplan' string)", m.hudModel)
	}
}

// TestProPlanTUI_ReconfigureAppliesProPlan 验证 /model proplan 锚点齐全时
// 正常切换:modelChoice = proplan,hudModel 显示日常 flash。
func TestProPlanTUI_ReconfigureAppliesProPlan(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro","sub_model":"deepseek-v4-flash"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := newProPlanTUIModel()
	m.settingsStore = &tuiSettingsStore{projectPath: projectPath}
	m.planModel = "deepseek-v4-pro"
	m.subModel = "deepseek-v4-flash"
	m.modelChoice = "deepseek-v4-flash"
	m.hudModel = "deepseek-v4-flash"

	m.reconfigureLLMClient(llm.ModelChoiceProPlan)

	if m.modelChoice != llm.ModelChoiceProPlan {
		t.Errorf("modelChoice = %q, want %q", m.modelChoice, llm.ModelChoiceProPlan)
	}
	if m.hudModel != "deepseek-v4-flash" {
		t.Errorf("hudModel = %q, want deepseek-v4-flash (daily display)", m.hudModel)
	}
}

// TestProPlanTUI_ReconfigureNormalModel 验证 /model 具体模型名时正常切换
// (proplan 语义下只改日常)。
func TestProPlanTUI_ReconfigureNormalModel(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	m := newProPlanTUIModel()
	m.settingsStore = &tuiSettingsStore{projectPath: projectPath}
	m.planModel = "deepseek-v4-pro"
	m.subModel = "deepseek-v4-flash"
	m.modelChoice = llm.ModelChoiceProPlan
	m.hudModel = "deepseek-v4-flash"

	m.reconfigureLLMClient("deepseek-v4-pro")

	if m.modelChoice != "deepseek-v4-pro" {
		t.Errorf("modelChoice = %q, want deepseek-v4-pro", m.modelChoice)
	}
	if m.hudModel != "deepseek-v4-pro" {
		t.Errorf("hudModel = %q, want deepseek-v4-pro", m.hudModel)
	}
}

// TestRegression_StartupResolveProfileForThinkingEffort 验证启动路径(runTUI 初始化)
// 对 profile 形态配置解析 thinking 档位。3b3313b 后 /model 切换写入
// profiles.<provider>.curr_model,extra_params 位于 profile 内:启动时若只做
// MergeLLMSettings 不 ResolveProfile,resolveThinkingEffort 读不到顶层
// ExtraParams → HUD 档位不显示。与 createLLMClient 的解析顺序保持一致。
func TestRegression_StartupResolveProfileForThinkingEffort(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	projectPath := filepath.Join(dir, "project.json")
	// 全局无 extra_params;项目为 profile 形态(reasoning_effort 在 profile 内)
	if err := os.WriteFile(globalPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile global: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro","sub_model":"deepseek-v4-flash","profiles":{"deepseek":{"curr_model":"deepseek-v4-flash","extra_params":{"thinking":{"type":"enabled"},"reasoning_effort":"max"}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile project: %v", err)
	}
	store := &tuiSettingsStore{projectPath: projectPath, globalPath: globalPath}

	// 驱动真实启动路径(runTUI 调用 initThinkingEffort):Merge → ResolveProfile → resolveThinkingEffort
	m := newProPlanTUIModel()
	m.settingsStore = store
	m.initThinkingEffort(store)
	if m.hudThinkingEffort != "max" {
		t.Errorf("hudThinkingEffort = %q, want max (profile extra_params must be visible at startup)", m.hudThinkingEffort)
	}
}

// TestRegression_ModelSwitchKeepsThinkingEffort 验证 /model 切换后 HUD thinking
// 档位不丢失:reasoning_effort 配置在全局 settings,项目文件无 extra_params。
// 修复前 reconfigureLLMClient 不更新 hudThinkingEffort,commitModelSwitch 又
// 基于仅项目文件的 settings 解析 → 返回空,footer 的 (think ...) 消失。
func TestRegression_ModelSwitchKeepsThinkingEffort(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	projectPath := filepath.Join(dir, "project.json")
	// 全局配置 reasoning_effort(与 setup 流程落盘形态一致)
	if err := os.WriteFile(globalPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro","sub_model":"deepseek-v4-flash","extra_params":{"thinking":{"type":"enabled"},"reasoning_effort":"max"}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile global: %v", err)
	}
	// 项目文件仅含 provider/model(无 extra_params,/model 切换后写入 curr_model 形态)
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro","sub_model":"deepseek-v4-flash","profiles":{"deepseek":{"curr_model":"deepseek-v4-flash"}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile project: %v", err)
	}
	m := newProPlanTUIModel()
	m.settingsStore = &tuiSettingsStore{projectPath: projectPath, globalPath: globalPath}
	m.planModel = "deepseek-v4-pro"
	m.subModel = "deepseek-v4-flash"
	m.modelChoice = "deepseek-v4-flash"
	m.hudModel = "deepseek-v4-flash"
	m.hudThinkingEffort = "" // 缺陷路径:切换后档位被清空;修复后 reconfigure 应恢复为合并配置的档位

	m.reconfigureLLMClient("deepseek-v4-pro")

	if m.hudThinkingEffort != "max" {
		t.Errorf("hudThinkingEffort = %q, want max (must survive /model switch)", m.hudThinkingEffort)
	}
	if m.hudModel != "deepseek-v4-pro" {
		t.Errorf("hudModel = %q, want deepseek-v4-pro", m.hudModel)
	}
}

// TestProPlanTUI_ProviderSwitchKeepsProPlan 验证 /provider 切换到锚点齐全的
// provider 时,proplan 选择保持(锚点/显示切换为新 provider 的模型)。
func TestProPlanTUI_ProviderSwitchKeepsProPlan(t *testing.T) {
	m := newProPlanTUIModel()
	m.modelChoice = llm.ModelChoiceProPlan
	m.planModel = "pro-a"
	m.subModel = "flash-a"
	m.hudModel = "flash-a"

	settings := &llm.LLMSettings{
		APIKey:    "sk-test",
		Provider:  "kimi",
		Model:     "kimi-k3",
		SubModel:  "kimi-k3",
		CurrModel: llm.ModelChoiceProPlan,
	}
	m.reconfigureLLMClientForProvider("kimi", settings)

	if m.modelChoice != llm.ModelChoiceProPlan {
		t.Errorf("modelChoice = %q, want %q (proplan kept across provider switch)", m.modelChoice, llm.ModelChoiceProPlan)
	}
	if m.planModel != "kimi-k3" || m.subModel != "kimi-k3" {
		t.Errorf("anchors = (%q, %q), want (kimi-k3, kimi-k3)", m.planModel, m.subModel)
	}
	if m.hudModel != "kimi-k3" {
		t.Errorf("hudModel = %q, want kimi-k3 (new provider's daily model)", m.hudModel)
	}
}

// TestProPlanTUI_ProviderSwitchAnchorsMissingFallback 验证 /provider 切到
// 锚点缺失的 provider 时,proplan 回退为该 provider 的 model(不静默升 pro)。
func TestProPlanTUI_ProviderSwitchAnchorsMissingFallback(t *testing.T) {
	m := newProPlanTUIModel()
	m.modelChoice = llm.ModelChoiceProPlan
	m.planModel = "pro-a"
	m.subModel = "flash-a"
	m.hudModel = "flash-a"

	settings := &llm.LLMSettings{
		APIKey:    "sk-test",
		Provider:  "kimi",
		Model:     "kimi-k3",
		CurrModel: llm.ModelChoiceProPlan, // kimi 无 sub_model → 锚点缺失
	}
	m.reconfigureLLMClientForProvider("kimi", settings)

	if m.modelChoice != "kimi-k3" {
		t.Errorf("modelChoice = %q, want kimi-k3 (fallback, proplan disabled)", m.modelChoice)
	}
	if m.hudModel != "kimi-k3" {
		t.Errorf("hudModel = %q, want kimi-k3", m.hudModel)
	}
}

// TestProPlanTUI_ProviderSwitchSelfReferentialAnchor 验证畸形配置
// (锚点 == proplan)在 /provider 路径同样被拒绝回退。
func TestProPlanTUI_ProviderSwitchSelfReferentialAnchor(t *testing.T) {
	m := newProPlanTUIModel()
	m.modelChoice = llm.ModelChoiceProPlan
	m.planModel = "pro-a"
	m.subModel = "flash-a"
	m.hudModel = "flash-a"

	settings := &llm.LLMSettings{
		APIKey:    "sk-test",
		Provider:  "kimi",
		Model:     llm.ModelChoiceProPlan, // 畸形:锚点自指
		SubModel:  "flash-x",
		CurrModel: llm.ModelChoiceProPlan,
	}
	m.reconfigureLLMClientForProvider("kimi", settings)

	if m.modelChoice != "" {
		t.Errorf("modelChoice = %q, want empty (self-referential anchor must not leak)", m.modelChoice)
	}
	if m.hudModel == "proplan" {
		t.Error("hudModel must not display raw 'proplan' string")
	}
}
