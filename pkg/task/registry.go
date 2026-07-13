// Package task 提供后台 shell 任务的注册与追踪。
// 全局单例 DefaultRegistry 供 shell 工具和 agentloop 共享使用。
package task

import (
	"sync"
	"time"
)

// TaskStatus 表示后台任务的执行状态。
type TaskStatus int

const (
	TaskRunning     TaskStatus = iota // 进程仍在执行
	TaskCompleted                     // 正常退出
	TaskFailed                        // 异常退出（非零退出码）
	TaskInterrupted                   // 进程被中断（session 关闭时仍在运行）
)

// String 返回 TaskStatus 的可读名称。
func (s TaskStatus) String() string {
	switch s {
	case TaskRunning:
		return "running"
	case TaskCompleted:
		return "completed"
	case TaskFailed:
		return "failed"
	case TaskInterrupted:
		return "interrupted"
	default:
		return "unknown"
	}
}

// TaskInfo 记录一个后台任务的运行时信息。
type TaskInfo struct {
	ID            string
	PID           int
	Command       string
	LogPath       string
	Status        TaskStatus
	StartTime     time.Time
	CompletedTime time.Time // 完成时刻（零值 = 未完成）
	ExitCode      int
}

// Registry 是后台任务的并发安全注册表。
type Registry struct {
	mu    sync.RWMutex
	tasks map[string]*TaskInfo
}

// DefaultRegistry 是全局共享的后台任务注册表实例。
var DefaultRegistry = &Registry{
	tasks: make(map[string]*TaskInfo),
}

// Register 注册一个新的后台任务。如果 ID 已存在则不覆盖。
func (r *Registry) Register(id string, info *TaskInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tasks[id]; exists {
		return
	}
	r.tasks[id] = info
}

// Update 更新任务的状态和退出码。如果任务不存在则静默返回。
func (r *Registry) Update(id string, status TaskStatus, exitCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[id]
	if !ok {
		return
	}
	t.Status = status
	t.ExitCode = exitCode
	if status != TaskRunning {
		t.CompletedTime = time.Now()
	}
}

// Get 返回指定 ID 的任务信息。不存在时返回 nil。
func (r *Registry) Get(id string) *TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tasks[id]
}

// CompletedSince 返回自指定时间点以来完成（成功或失败）的所有任务切片。
// 使用 CompletedTime 而非 StartTime 判断，确保跨多轮才完成的任务不会漏报。
func (r *Registry) CompletedSince(since time.Time) []*TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var completed []*TaskInfo
	for _, t := range r.tasks {
		if t.Status != TaskRunning && t.CompletedTime.After(since) {
			completed = append(completed, t)
		}
	}
	return completed
}

// Running 返回所有正在执行的任务切片。
func (r *Registry) Running() []*TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var running []*TaskInfo
	for _, t := range r.tasks {
		if t.Status == TaskRunning {
			running = append(running, t)

		}
	}
	return running
}

// InterruptRunning 将所有 Running 状态的任务标记为 TaskInterrupted。
// 用于 session 恢复时检测：原 waveloom 进程已退出，无法再监控这些任务，
// 将它们标记为中断状态，让 agent 在下轮通知中感知到。
// 返回被中断的任务列表。
func (r *Registry) InterruptRunning() []*TaskInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	var interrupted []*TaskInfo
	now := time.Now()
	for _, t := range r.tasks {
		if t.Status == TaskRunning {
			t.Status = TaskInterrupted
			t.ExitCode = -1
			t.CompletedTime = now
			interrupted = append(interrupted, t)
		}
	}
	return interrupted
}

// List 返回所有已注册任务的切片快照。
func (r *Registry) List() []*TaskInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*TaskInfo, 0, len(r.tasks))
	for _, t := range r.tasks {
		result = append(result, t)
	}
	return result
}

// Remove 从注册表中删除指定任务。
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
}

// Reset 清空所有已注册的任务（用于测试清理）。
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks = make(map[string]*TaskInfo)
}
