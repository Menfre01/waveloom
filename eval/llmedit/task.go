// Package llmedit 实现 Layer 3 LLM-in-the-loop 编辑可用性评估。
//
// 核心流程:
//   任务(初始文件 + 自然语言指令 + gold 文件)
//     → agentloop(read → edit → verify)
//     → 指标采集(14 个指标,按 turn 记录)
//     → 判分(byte-level diff + 编译检查)
//     → 报告
//
// 依赖注入: 通过 Runner.New(llm.Client, tool.Registry) 注入 LLM 客户端,
// 支持真实 LLM 或 mock 用于自测。
package llmedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TaskType 编辑任务类型。
type TaskType string

const (
	TaskSingleLine  TaskType = "single-line"  // 单行替换
	TaskMultiLine   TaskType = "multi-line"   // 多行重构(单文件)
	TaskCrossFile   TaskType = "cross-file"   // 跨文件编辑
	TaskAddFunction TaskType = "add-function" // 新增函数/代码块
)

// Task 定义单个编辑评估任务。
type Task struct {
	// Name 任务名,报告中使用。
	Name string `json:"name"`
	// Instruction 自然语言编辑指令。
	Instruction string `json:"instruction"`
	// Type 任务类型。
	Type TaskType `json:"type"`
	// Files 初始文件状态(path → content)。
	Files map[string]string `json:"files"`
	// Gold 期望的最终文件状态(path → content)。
	Gold map[string]string `json:"gold"`
	// MaxTurns 最大 LLM turn 数,默认 8。
	MaxTurns int `json:"max_turns,omitempty"`
}

// Validate 检查任务定义是否合法。
func (t *Task) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("task name required")
	}
	if t.Instruction == "" {
		return fmt.Errorf("instruction required")
	}
	if len(t.Files) == 0 {
		return fmt.Errorf("at least one file required")
	}
	if len(t.Gold) == 0 {
		return fmt.Errorf("at least one gold file required")
	}
	if t.MaxTurns <= 0 {
		t.MaxTurns = 8
	}
	return nil
}

// LoadTask 从 JSON 文件加载单个任务。
func LoadTask(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read task %s: %w", path, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse task %s: %w", path, err)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("invalid task %s: %w", path, err)
	}
	return &t, nil
}

// LoadTasks 加载目录下所有 .json 任务文件。
func LoadTasks(dir string) ([]*Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read task dir: %w", err)
	}
	var tasks []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		t, err := LoadTask(path)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks found in %s", dir)
	}
	return tasks, nil
}
