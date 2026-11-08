package main

import (
	"os"
	"strings"
	"testing"
)

// Test_TailRulesInjectedBothEntries 断言主流程与 ACP 两条组装链都注入了尾部规则。
// REGRESSION: 尾部规则是本项目唯一被实测证实有遵从率的注入位置
// (experiments/attention_position:END 83% vs MID 0%);任一入口漏注入,
// 该路径就完全失去"自主执行、禁止建议式收尾"的行为约束。
func Test_TailRulesInjectedBothEntries(t *testing.T) {
	for _, file := range []string{"main.go", "acp.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(data), "prompt.TailRules") {
			t.Errorf("%s 未注入 prompt.TailRules,该入口将失去尾部行为约束", file)
		}
	}
}

// Test_TailRulesIsLastSection 断言尾部规则位于工具指南之后(组装链末尾)。
//
// 局限:源码级断言,不覆盖运行时字符串。若将来把组装逻辑收口到独立函数,
// 应改为对组装结果的行为断言。
func Test_TailRulesIsLastSection(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(data)

	toolIdx := strings.Index(src, "FormatToolPrompts")
	if toolIdx < 0 {
		t.Fatal("未找到 FormatToolPrompts 注入点")
	}
	tailIdx := strings.Index(src, "prompt.TailRules")
	if tailIdx < 0 {
		t.Fatal("未找到 prompt.TailRules 注入点")
	}
	if tailIdx < toolIdx {
		t.Error("prompt.TailRules 必须在 FormatToolPrompts 之后注入,否则落入 MID 低遵从区")
	}

	// 尾部规则之后不应再有 system prompt 追加(其自身那一处除外)。
	if n := strings.Count(src[tailIdx:], "systemPrompt +="); n != 1 {
		t.Errorf("prompt.TailRules 之后仍有 %d 处 systemPrompt 追加,尾部位置被破坏", n-1)
	}
}
