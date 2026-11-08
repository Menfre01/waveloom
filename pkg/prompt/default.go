// Package prompt 提供 Waveloom 的系统提示词,供生产环境和评估工具共享引用。
package prompt

import _ "embed"

//go:embed default.md
var Default string

// TailRules 是追加到 system prompt **绝对末尾**的高遵从区规则:决策边界、
// 交付形态、动作完整性。
//
// 由入口(main.go / acp.go)在组装链最后追加,其后不得再追加任何内容——
// 一旦被推到中段,这些规则会落入遵从率接近零的位置。
// 依据:experiments/attention_position 实测竞争条件下 END 10/12(83%)、
// BEGIN 2/12(17%)、MID 0/12(0%)。
//
//go:embed tail.md
var TailRules string
