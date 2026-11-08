package compaction

import (
	"strings"
	"testing"
)

// TestRegression_CompactionNoFalseClaim 断言截断提示不声称内容已被处理,并给出恢复路径。
// REGRESSION: 原文案 "[... N lines omitted — full result already processed by Agent]"
// 暗示模型已知晓被省略的内容;实际这些行已从上下文永久丢弃,模型据此以为"看过"
// 而跳过重读,基于不存在的记忆继续推理。
func TestRegression_CompactionNoFalseClaim(t *testing.T) {
	repeatedLines := strings.Repeat("line\n", 200)

	cases := []struct {
		name string
		in   string
		s    truncationStrategy
	}{
		{
			name: "行数截断-保留尾部",
			in:   repeatedLines,
			s:    truncationStrategy{maxLines: 60, headLines: 20, tailLines: 30, maxTotalChars: 20000},
		},
		{
			name: "行数截断-不保留尾部",
			in:   repeatedLines,
			s:    truncationStrategy{maxLines: 60, headLines: 20, tailLines: 0, maxTotalChars: 20000},
		},
		{
			name: "字符截断",
			in:   strings.Repeat("x", 500),
			s:    truncationStrategy{maxTotalChars: 100},
		},
	}

	for _, c := range cases {
		out, triggered := truncateByStrategy(c.in, c.s)
		if !triggered {
			t.Fatalf("%s: 应触发截断", c.name)
		}
		if strings.Contains(out, "already processed") {
			t.Errorf("%s: 提示声称内容已被处理: %q", c.name, out)
		}
		if !strings.Contains(out, "re-read") {
			t.Errorf("%s: 缺恢复路径提示: %q", c.name, out)
		}
	}
}
