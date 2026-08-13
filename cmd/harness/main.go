// harness — SWE-bench 评测 CLI(参照 cmd/llmedit 先例:逻辑在 eval/harness 库包)。
//
// 用法(仓库根):
//   go run ./cmd/harness -instances <id1,id2> [-parallel N] [-fast|-full]
//
// 默认数据目录:eval/swebench/results;LLM 配置:eval/swebench/settings.json
// (API key 经 LLM_API_KEY 环境变量注入,与 run.py 的 .api_key 文件同源)。
package main

import (
	"os"

	"github.com/Menfre01/waveloom/eval/harness"
)

func main() {
	os.Exit(harness.RunCLI(os.Args[1:]))
}
