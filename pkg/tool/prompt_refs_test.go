package tool

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/prompt"
)

// sysPromptSectionRef 匹配 "system prompt ## <Title>" 形式的章节引用。
var sysPromptSectionRef = regexp.MustCompile(`system prompt ## ([A-Za-z][A-Za-z ]*)`)

// TestRegression_ToolPromptReferencesExist 断言工具描述引用的 system prompt 章节真实存在。
// REGRESSION: web_search / web_fetch 的 Description 引用 "## Information Sources",
// 而 default.md 中并无该章节 —— 搜索纪律条款整节悬空,模型只拿到一个空指针。
func TestRegression_ToolPromptReferencesExist(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read pkg/tool: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, match := range sysPromptSectionRef.FindAllStringSubmatch(string(data), -1) {
			checked++
			title := strings.TrimSpace(match[1])
			if !strings.Contains(prompt.Default, "## "+title) {
				t.Errorf("%s 引用了不存在的 system prompt 章节: ## %s", name, title)
			}
		}
	}

	if checked == 0 {
		t.Fatal("未找到任何章节引用,断言失效(正则或引用写法已变更)")
	}
}
