package prompt

import (
	"math"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/pricing"
)

// concatResidues 是 Go 字符串拼接语法在 Markdown 里的残留形态。
// REGRESSION: default.md 曾 4 处出现 `` ` + "`read(...)`" + ` ``,
// 经 go:embed 原样进入 system prompt,模型读到的是无法解析的噪声。
var concatResidues = []string{"` + \"", "\" + `"}

// promptSources 返回参与完整性校验的源文本。
func promptSources() map[string]string {
	return map[string]string{
		"default.md": Default,
		"tail.md":    TailRules,
	}
}

// TestRegression_PromptNoConcatResidue 断言 system prompt 源文本不含拼接残留。
func TestRegression_PromptNoConcatResidue(t *testing.T) {
	for name, src := range promptSources() {
		for _, residue := range concatResidues {
			idx := strings.Index(src, residue)
			if idx < 0 {
				continue
			}
			line := strings.Count(src[:idx], "\n") + 1
			t.Errorf("%s:%d 含源码拼接残留 %q —— 会原样进入 system prompt", name, line, residue)
		}
	}
}

// TestRegression_PromptSectionBlankLine 断言每个 Markdown 标题前有空行。
// 标题与正文粘连时分节失效,该节被解析为上一段落的续行,指令失去结构。
func TestRegression_PromptSectionBlankLine(t *testing.T) {
	for name, src := range promptSources() {
		lines := strings.Split(src, "\n")
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if !strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "### ") {
				continue
			}
			if strings.TrimSpace(lines[i-1]) != "" {
				t.Errorf("%s:%d 标题前缺空行: %q", name, i+1, line)
			}
		}
	}
}

// TestRegression_PricingClaimMatchesTable 断言成本倍数断言与定价表一致。
// REGRESSION: default.md 曾声称 "Output tokens are 240x the cost of cached input
// tokens" —— 该比例拿输出价对比缓存命中价,且未写明比较对象;对未命中输入实际仅
// 约 4x,失真的成本信号诱导模型过度压缩输出。
func TestRegression_PricingClaimMatchesTable(t *testing.T) {
	p := pricing.Lookup("deepseek", "deepseek-flash")
	if p.Output == 0 || p.CacheMiss == 0 || p.CacheHit == 0 {
		t.Fatalf("定价表缺 deepseek-flash 条目: %+v", p)
	}

	if vsMiss := p.Output / p.CacheMiss; math.Abs(vsMiss-4) > 0.5 {
		t.Errorf("输出/未命中 = %.2fx,与 default.md 声称的 4x 不符", vsMiss)
	}
	if vsHit := p.Output / p.CacheHit; math.Abs(vsHit-200) > 20 {
		t.Errorf("输出/缓存命中 = %.0fx,与 default.md 声称的 200x 不符", vsHit)
	}

	if !strings.Contains(Default, "4x a cache-miss input token") {
		t.Error("default.md 未声明「相对未命中输入的倍数」")
	}
	if !strings.Contains(Default, "200x a cache-hit input token") {
		t.Error("default.md 未声明「相对缓存命中的倍数」")
	}
}
