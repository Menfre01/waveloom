// Package prompt 提供 Waveloom 的系统提示词,供生产环境和评估工具共享引用。
package prompt

import _ "embed"

//go:embed default.md
var Default string
