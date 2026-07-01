// Package environment — Waveloom 启动时探测宿主编译/运行时工具链可用性。
//
// 本文件实现 GitHub Release 版本更新检查：
// 通过 GitHub API 获取最新 release tag，与当前编译版本比较，
// 有新版时在 TUI header 中显示更新提示。
package environment

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UpdateInfo 包含版本更新检查的结果。
type UpdateInfo struct {
	CurrentVersion string // 当前编译版本号（如 "v0.1.0-alpha.6"）
	LatestVersion  string // GitHub 最新 release tag（如 "v0.2.0"）
	UpdateAvailable bool  // 是否有新版本可用
	URL            string // release 页面 URL
}

// CheckForUpdate 获取 GitHub 最新 release tag 并与当前版本比较。
// 通过访问 releases/latest 重定向地址提取 tag，无需 API 认证，不受限流。
// 返回的 UpdateInfo 线程安全，可跨 goroutine 传递。
// 网络错误静默忽略，返回 (nil, nil) 表示跳过本次检查。
func CheckForUpdate(ctx context.Context, currentVersion string) (*UpdateInfo, error) {
	// 开发版本不检查
	if currentVersion == "" || currentVersion == "dev" {
		return nil, nil
	}

	return checkLatestRelease(ctx, "https://github.com/Menfre01/waveloom/releases/latest", currentVersion)
}

// checkLatestRelease 访问 releases/latest 页面，从重定向目标 URL 中提取 tag。
// GitHub releases/latest 返回 302 到 /releases/tag/<version>，无需 API 认证、不受限流。
// URL 参数化便于 httptest mock。
func checkLatestRelease(ctx context.Context, url, currentVersion string) (*UpdateInfo, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随重定向，我们只需要 Location header
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	req.Header.Set("User-Agent", "waveloom/"+currentVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	// releases/latest 返回 302，Location header 包含 /releases/tag/<version>
	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("check update: unexpected status %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	tag := location
	if idx := strings.LastIndex(location, "/"); idx >= 0 {
		tag = location[idx+1:]
	}

	latest := strings.TrimPrefix(tag, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	info := &UpdateInfo{
		CurrentVersion:  currentVersion,
		LatestVersion:   tag,
		UpdateAvailable: latest != "" && current != "" && latest != current,
		URL:             location,
	}
	return info, nil
}

// ---------------------------------------------------------------------------
// 非阻塞检查（供 TUI Init 使用）
// ---------------------------------------------------------------------------

// CheckForUpdateAsync 在后台 goroutine 执行更新检查，结果通过 channel 返回。
// 2s 超时保护，失败时静默跳过。
func CheckForUpdateAsync(currentVersion string) <-chan *UpdateInfo {
	ch := make(chan *UpdateInfo, 1)
	go func() {
		defer close(ch)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		info, err := CheckForUpdate(ctx, currentVersion)
		if err != nil || info == nil {
			return
		}
		ch <- info
	}()
	return ch
}

// ---------------------------------------------------------------------------
// 线程安全缓存（防止 render 循环中重复检查）
// ---------------------------------------------------------------------------

// UpdateCache 线程安全缓存单次更新检查结果。
type UpdateCache struct {
	mu   sync.RWMutex
	info *UpdateInfo
	done bool
}

// Set 写入检查结果。
func (c *UpdateCache) Set(info *UpdateInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info = info
	c.done = true
}

// Get 读取检查结果。done 表示检查已完成。
func (c *UpdateCache) Get() (*UpdateInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info, c.done
}
