package tool

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ThrottleStore 提供 web_fetch/web_search 的会话级 per-host 限流状态:
// 记录连续失败次数、最早可重试时间(限流退避)与策略封锁冷却(403 无
// Retry-After),让工具在"还在退避期"时直接短路(不发网络请求),并让
// 错误文案携带"连续 N 次 / 最早可重试时间"——模型不再需要猜该等多久、
// 该不该再试(2026-09 会话实证:模型在无状态错误下换 UA 原地硬刚、
// 整批重跑放大限流)。
//
// 生命周期与 ReadStateStore 一致:loop 级创建、不落盘;经 ctx 注入
// (WithThrottleStore),嵌套 Loop(fork 子代理)由 Config 显式透传,
// 避免子代理覆盖父 ctx 后对刚被限流的 host 继续轰炸。
type ThrottleStore struct {
	mu    sync.RWMutex
	hosts map[string]*hostThrottle
}

// hostThrottle 单个 host 的限流状态。
type hostThrottle struct {
	consecutiveFails int       // 连续失败次数(成功清零)
	nextAllowedAt    time.Time // 限流退避到期(429/503/403+RA/202)
	blockUntil       time.Time // 策略封锁冷却到期(403 无 RA)
	lastFailAt       time.Time // 失败时间(失败后同域最小间隔基准)
	blocked          bool      // 当前状态:true=封锁(403),false=限流退避
}

// Throttle 状态常量(供错误文案区分语义)。
const (
	ThrottleOK         = "ok"
	ThrottleRateLimit  = "rate_limited"
	ThrottleBlocked    = "blocked"
	ThrottleMinGapWait = "min_gap"
)

// 限流策略参数。
const (
	// throttleMaxInToolWait 工具内同步等待上限:超过即返回错误(带 nextAllowedAt)
	// 由模型决定,避免长退避卡死 turn(对齐既有 maxRateLimitWait=10s)。
	throttleMaxInToolWait = 10 * time.Second
	// throttleBackoffBase 指数退避基数:2s × 2^(N-1),封顶 throttleBackoffCap。
	throttleBackoffBase = 2 * time.Second
	throttleBackoffCap  = 60 * time.Second
	// throttleBlockCooldown 403 无 Retry-After(策略封锁)的有界冷却时长。
	throttleBlockCooldown = 10 * time.Minute
	// throttleMinGap 失败后同域最小请求间隔(成功路径不受限)。
	throttleMinGap = time.Second
	// throttleScheduleCap 记录的 nextAllowedAt 距 now 上限(纯调度,不等待)。
	throttleScheduleCap = time.Hour
)

type throttleKey struct{}

// WithThrottleStore 将 ThrottleStore 注入 ctx。
func WithThrottleStore(ctx context.Context, s *ThrottleStore) context.Context {
	return context.WithValue(ctx, throttleKey{}, s)
}

// ThrottleStoreFromContext 从 ctx 提取 ThrottleStore(未注入时返回 nil)。
func ThrottleStoreFromContext(ctx context.Context) *ThrottleStore {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(throttleKey{}).(*ThrottleStore)
	return s
}

// NewThrottleStore 创建空的限流状态 store。
func NewThrottleStore() *ThrottleStore {
	return &ThrottleStore{hosts: make(map[string]*hostThrottle)}
}

// throttleHost 归一化 host key(小写,去端口比较用原串即可;统一小写防大小写分裂)。
func throttleHost(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return strings.ToLower(host[:i])
	}
	return strings.ToLower(host)
}

// Reserve 在发起网络请求前调用(nil store 恒放行)。
// allowed=false 时调用方必须短路(不发请求),retryAt 为最早可重试时间,
// state 为 ThrottleRateLimit / ThrottleBlocked / ThrottleMinGapWait。
func (s *ThrottleStore) Reserve(host string, now time.Time) (allowed bool, retryAt time.Time, state string, consecutive int) {
	if s == nil {
		return true, time.Time{}, ThrottleOK, 0
	}
	host = throttleHost(host)
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hosts[host]
	if h == nil {
		return true, time.Time{}, ThrottleOK, 0
	}
	// 状态按强度排序:封锁 > 限流退避 > 失败后最小间隔。
	// (min-gap 若先判,冷却期内的调用会错误地以 rate_limited 弱语义短路)
	if h.blocked && now.Before(h.blockUntil) {
		return false, h.blockUntil, ThrottleBlocked, h.consecutiveFails
	}
	if !h.nextAllowedAt.IsZero() && now.Before(h.nextAllowedAt) {
		return false, h.nextAllowedAt, ThrottleRateLimit, h.consecutiveFails
	}
	// 失败后同域最小间隔:即使退避未生效也避免紧接重发。
	if !h.lastFailAt.IsZero() {
		if gap := throttleMinGap - now.Sub(h.lastFailAt); gap > 0 {
			return false, now.Add(gap), ThrottleMinGapWait, h.consecutiveFails
		}
	}
	return true, time.Time{}, ThrottleOK, h.consecutiveFails
}

// ReportRateLimited 记录一次限流失败响应(429/503/403+RA/202):
// 指数退避 2s×2^(N-1) 封顶 60s,与外部调度时间(externalRetryAt,来自
// Retry-After,未钳制)取较晚者;返回调度到期时间与连续失败次数。
// 锁内不 sleep、不跨网络。
func (s *ThrottleStore) ReportRateLimited(host string, externalRetryAt time.Time, now time.Time) (retryAt time.Time, consecutive int) {
	if s == nil {
		return now.Add(throttleBackoffBase), 1
	}
	host = throttleHost(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hosts[host]
	if h == nil {
		h = &hostThrottle{}
		s.hosts[host] = h
	}
	h.consecutiveFails++
	h.blocked = false
	backoff := throttleBackoffBase << (h.consecutiveFails - 1)
	if backoff > throttleBackoffCap {
		backoff = throttleBackoffCap
	}
	retryAt = now.Add(backoff)
	if externalRetryAt.After(retryAt) {
		retryAt = externalRetryAt
	}
	if d := retryAt.Sub(now); d > throttleScheduleCap {
		retryAt = now.Add(throttleScheduleCap)
	}
	h.nextAllowedAt = retryAt
	h.lastFailAt = now
	return retryAt, h.consecutiveFails
}

// ReportBlocked 记录一次策略封锁响应(403 无 Retry-After):
// 有界冷却 throttleBlockCooldown,冷却期内 Reserve 短路(不发网络)。
func (s *ThrottleStore) ReportBlocked(host string, now time.Time) (blockUntil time.Time, consecutive int) {
	if s == nil {
		return now.Add(throttleBlockCooldown), 1
	}
	host = throttleHost(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hosts[host]
	if h == nil {
		h = &hostThrottle{}
		s.hosts[host] = h
	}
	h.consecutiveFails++
	h.blocked = true
	h.blockUntil = now.Add(throttleBlockCooldown)
	h.lastFailAt = now
	return h.blockUntil, h.consecutiveFails
}

// ReportSuccess 记录一次成功响应:清零连续失败与退避调度。
func (s *ThrottleStore) ReportSuccess(host string) {
	if s == nil {
		return
	}
	host = throttleHost(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hosts[host]
	if h == nil {
		return
	}
	h.consecutiveFails = 0
	h.nextAllowedAt = time.Time{}
	h.blockUntil = time.Time{}
	h.blocked = false
	h.lastFailAt = time.Time{}
}

// rawRetryAfterDelay 解析 Retry-After 为未钳制的调度时长(上限 1h,供 store
// 短路调度用;工具内实际同步等待仍走钳制到 10s 的 retryAfterDuration)。
func rawRetryAfterDelay(header http.Header) time.Duration {
	ra := header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	if secs, err := parseRetryAfterSeconds(ra); err == nil {
		d := time.Duration(secs) * time.Second
		if d > time.Hour {
			return time.Hour
		}
		return d
	}
	if t, err := http.ParseTime(ra); err == nil {
		if d := time.Until(t); d > 0 && d <= time.Hour {
			return d
		}
	}
	return 0
}

func parseRetryAfterSeconds(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}
