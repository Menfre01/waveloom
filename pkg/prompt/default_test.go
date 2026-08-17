package prompt

import (
	"strings"
	"testing"
)

// TestDefault_ActionPreviewRule 锁定 "Two legal response forms" 条款中的
// action preview 语义化定义(未来指向预告必须带工具调用,状态汇报合法)。
// 该规则与 loop 层的 hasPreviewSuffix 兜底(agentloop)互为两道防线:
// prompt 降低发生率,代码兜底残余失败。若此条款被误删,回归测试失败。
func TestDefault_ActionPreviewRule(t *testing.T) {
	if !strings.Contains(Default, "action preview") {
		t.Fatal("default prompt must define 'action preview' (Two legal response forms)")
	}
	// 未来指向的预告(以冒号结尾宣告下一步动作)必须被标记为非法
	if !strings.Contains(Default, "without the matching tool call") {
		t.Fatal("default prompt must state that an action preview without tool call is illegal")
	}
	// 已完成工作/当前状态汇报必须被显式豁免(避免误伤等待后台任务/用户输入的合法场景)
	if !strings.Contains(Default, "reporting work already completed or current state") {
		t.Fatal("default prompt must exempt status reporting from the action-preview ban")
	}
}

// TestDefault_ActionPreviewExample 验证定义中包含中英文示例,
// 防止示例被简化后失去操作性(模型对具体示例的遵从度高于抽象规则)。
func TestDefault_ActionPreviewExample(t *testing.T) {
	if !strings.Contains(Default, "启动 cold 审核:") {
		t.Error("action preview rule should include a Chinese example (e.g. 启动 cold 审核:)")
	}
	if !strings.Contains(Default, "Let me check the file:") {
		t.Error("action preview rule should include an English example")
	}
}
