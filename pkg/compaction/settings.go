package compaction

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// settings.json compaction 配置
// ---------------------------------------------------------------------------

// CompactionSettings 对应 settings.json 中的 compaction 配置块。
// token 计数字段（protection_zone_tokens / context_limit_tokens）支持：
//   - 裸数字: 8000
//   - K/M 后缀: "8K", "1.5M"
type CompactionSettings struct {
	Tier1Threshold       *float64      `json:"tier1_threshold"`        // Tier 1 触发阈值，nil 使用默认值
	Tier2Threshold       *float64      `json:"tier2_threshold"`        // Tier 2 触发阈值
	Tier3Threshold       *float64      `json:"tier3_threshold"`        // Tier 3 触发阈值
	ProtectionZoneTokens *tokenSetting `json:"protection_zone_tokens"` // 保护区 token 数
	ContextLimit         *tokenSetting `json:"context_limit_tokens"`   // 模型上下文上限（token 数）
}

// tokenSetting 支持 JSON 数字或 "8K"/"1M" 字符串，统一转为 int。
type tokenSetting int

func (t *tokenSetting) UnmarshalJSON(data []byte) error {
	// 尝试字符串
	if len(data) >= 2 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err == nil {
			n, err := parseTokenString(s)
			if err != nil {
				return fmt.Errorf("invalid token setting %q: %w", s, err)
			}
			*t = tokenSetting(n)
			return nil
		}
	}
	// 裸数字
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("token setting must be a number or K/M string: %s", string(data))
	}
	*t = tokenSetting(n)
	return nil
}

func parseTokenString(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}
	multiplier := 1
	last := s[len(s)-1]
	switch last {
	case 'K', 'k':
		multiplier = 1_000
		s = s[:len(s)-1]
	case 'M', 'm':
		multiplier = 1_000_000
		s = s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, err
	}
	return int(f * float64(multiplier)), nil
}

// LoadCompactionSettings 从 settings.json 文件读取 compaction 配置。
// 文件不存在或缺少 compaction 块时返回 nil。
func LoadCompactionSettings(path string) *CompactionSettings {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var wrapper struct {
		Compaction *CompactionSettings `json:"compaction"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil || wrapper.Compaction == nil {
		return nil
	}
	return wrapper.Compaction
}

// ApplyToConfig 将 CompactionSettings 中非 nil 的字段覆盖到 CompactionConfig。
func (s *CompactionSettings) ApplyToConfig(c *CompactionConfig) {
	if s.Tier1Threshold != nil {
		c.Tier1Threshold = *s.Tier1Threshold
	}
	if s.Tier2Threshold != nil {
		c.Tier2Threshold = *s.Tier2Threshold
	}
	if s.Tier3Threshold != nil {
		c.Tier3Threshold = *s.Tier3Threshold
	}
	if s.ProtectionZoneTokens != nil {
		c.ProtectionZoneTokens = int(*s.ProtectionZoneTokens)
	}
	if s.ContextLimit != nil {
		c.ContextLimit = int(*s.ContextLimit)
	}
}
