package llmedit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ScoreResult 单个文件的判分结果。
type ScoreResult struct {
	TaskName string
	// Passed 是否通过(所有 gold 文件精确匹配)。
	Passed bool
	// FileResults 逐文件得分。
	FileResults map[string]FileScore
	// CompileOK 编译是否通过(仅 Go 文件)。
	CompileOK bool
	// EditDistance 总编辑距离。
	EditDistance int
}

// FileScore 单个文件的得分。
type FileScore struct {
	Path         string
	ExactMatch   bool   // byte-level 完全匹配
	Got          string // 实际内容
	Want         string // 期望内容
	EditDistance int    // 编辑距离
}

// Score 在临时目录中对最终文件进行判分。
// gotFiles 是 LLM 编辑后的文件状态。
func Score(task *Task, gotFiles map[string]string) *ScoreResult {
	sr := &ScoreResult{
		TaskName:    task.Name,
		FileResults: make(map[string]FileScore, len(task.Gold)),
	}

	allMatch := true
	totalDist := 0

	for path, want := range task.Gold {
		got := gotFiles[path]
		dist := levenshtein(got, want)
		totalDist += dist

		fs := FileScore{
			Path:         path,
			ExactMatch:   got == want,
			Got:          got,
			Want:         want,
			EditDistance: dist,
		}
		sr.FileResults[path] = fs
		if !fs.ExactMatch {
			allMatch = false
		}
	}

	sr.Passed = allMatch
	sr.EditDistance = totalDist

	// 编译检查(仅检查 .go 文件)
	sr.CompileOK = checkCompile(task, gotFiles)

	return sr
}

// checkCompile 在临时目录中对编辑后的 Go 文件做编译检查。
func checkCompile(task *Task, gotFiles map[string]string) bool {
	// 无 .go 文件则跳过
	hasGo := false
	for p := range gotFiles {
		if strings.HasSuffix(p, ".go") {
			hasGo = true
			break
		}
	}
	if !hasGo {
		return true // 非 Go 文件,默认通过
	}

	dir, err := os.MkdirTemp("", "llmedit-compile-*")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(dir) }()

	for p, content := range gotFiles {
		fullPath := filepath.Join(dir, filepath.Base(p))
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return false
		}
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// levenshtein 计算两个字符串的编辑距离。
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	// 优化: 使用两行滚动数组
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
