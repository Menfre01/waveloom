package tool

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SanitizeToolOutput 从工具输出中移除可用于 prompt injection 的隐藏 Unicode 字符。
//
// 三步清洗管线（对标 Claude Code partiallySanitizeUnicode）：
//
//  1. NFKC 正规化 — 折叠兼容性等價字符（如 ﬁ→fi、K→K），
//     防止攻击者利用 Unicode 同形异义绕过关键词检测。
//  2. 主防御：Unicode 类别检测 — 移除 Cf（格式字符）、Co（私有使用区，含 TAG 字符）、
//     Cs（孤立代理）。对标 Claude Code \p{Cf}\p{Co}\p{Cs}（Method 1）。
//  3. 辅助防御：显式区间 — 控制字符、Unicode 空白、非字符，对标 Method 2。
//
// 关键覆盖：
//   - SOFT HYPHEN (U+00AD)         — Cf，不可见，破坏 token 边界
//   - TAG 字符 (U+E0001, U+E0020+) — Co，HackerOne #3086545 的原始攻击向量
//   - WORD JOINER (U+2060)         — Cf，零宽，影响 tokenization
//   - 全部 16 个平面的 PUA          — Co，自定义字体的走私通道
//   - 孤立代理 (U+D800-U+DFFF)      — Cs，不应出现在合法 UTF-8 中
//
// 返回值：清洗后的字符串。
// 当无字符被移除时直接返回原字符串（避免不必要的内存分配）。
func SanitizeToolOutput(s string) string {
	// Step 1: NFKC 正规化 — 处理兼容性等价字符。
	// 对标 Claude Code current.normalize('NFKC')。
	s = norm.NFKC.String(s)

	// Step 2: 扫描危险字符。快速路径 — 无危险字符时直接返回。
	for i, r := range s {
		if isDangerousUnicode(r) {
			// 发现第一个危险字符 → 从该位置开始构建清洗后的字符串
			var b strings.Builder
			b.WriteString(s[:i])
			for _, r2 := range s[i:] {
				if !isDangerousUnicode(r2) {
					b.WriteRune(r2)
				}
			}
			return b.String()
		}
	}
	return s
}

// isDangerousUnicode 判断 rune 是否属于可用于 prompt injection 的隐藏 Unicode 字符。
//
// 双层防御：
//
//	Primary:   unicode.Cf / unicode.Co / unicode.Cs → 对标 Claude Code \p{Cf}\p{Co}\p{Cs}
//	Secondary: 显式区间 → 控制字符、Unicode 空白、非字符
func isDangerousUnicode(r rune) bool {
	// ── Primary: Unicode 类别检测（对标 Claude Code \p{Cf}\p{Co}\p{Cs}）──
	//
	// Cf = 格式字符：SOFT HYPHEN, ZWJ, ZWNJ, ZWSP, LRM, RLM, BOM,
	//      WORD JOINER, invisible operators, directional formatting, TAG chars,
	//      variation selectors, Arabic format marks, etc.
	// Co = 私有使用区：BMP PUA (U+E000-U+F8FF) + Supplementary PUA planes 15-16。
	//      TAG 字符 (U+E0001, U+E0020-U+E007F) 也属于此类别 —
	//      这是 HackerOne #3086545 中 ASCII smuggling 的载体。
	// Cs = 代理对：U+D800-U+DFFF。合法 UTF-8 不应包含孤立代理，
	//      但防御纵深要求移除它们。
	if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) || unicode.Is(unicode.Cs, r) {
		return true
	}

	// ── Secondary: 显式区间（不被 Cf/Co/Cs 覆盖的类别）──
	switch {
	// Cc — 控制字符（移除不安全的，保留 tab/LF/CR）
	case r >= 0x00 && r <= 0x08:
		return true
	case r == 0x0B || r == 0x0C:
		return true
	case r >= 0x0E && r <= 0x1F:
		return true
	case r == 0x7F:
		return true // DEL

	// Zl/Zp — 行分隔符 / 段分隔符（不在 Cf 中，NFKC 也不转换）
	case r == 0x2028: // Line Separator (Zl)
		return true
	case r == 0x2029: // Paragraph Separator (Zp)
		return true

	// Zs — 空格分隔符中不在 Cf/Co/Cs 也不被 NFKC 转 ASCII 空格的
	// NBSP (U+00A0) 已被 NFKC 转 ASCII 空格，无需单独处理
	case r == 0x1680: // Ogham Space Mark
		return true

	// Cn — 非字符（noncharacters）：每个平面末尾两个码点。
	// U+FFFE 和 U+FFFF 在 BMP 的 Specials 区块，以及各补充平面的对应位置。
	// (r&0xFFFE)==0xFFFE 且 r 在有效 Unicode 范围内时匹配所有平面。
	case r >= 0xFFFE && (r&0xFFFE) == 0xFFFE:
		return true

	default:
		return false
	}
}
