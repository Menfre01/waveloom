package agentloop

import (
	"encoding/json"
	"os"
	"time"
)

// toolTimeoutSettings 对应 settings.json 中的 tool 配置块。
type toolTimeoutSettings struct {
	ToolTimeout string `json:"tool_timeout"` // Go Duration 格式（如 "10m" / "600s" / "0s"）
}

// LoadToolTimeout 从 settings.json 文件读取 tool_timeout 配置。
// 文件不存在或字段为空时返回 (0, false)。
// JSON 解析错误时返回 error。
func LoadToolTimeout(path string) (time.Duration, bool, error) {
	if path == "" {
		return 0, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}

	var s toolTimeoutSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, false, err
	}
	if s.ToolTimeout == "" {
		return 0, false, nil
	}

	d, err := time.ParseDuration(s.ToolTimeout)
	if err != nil {
		return 0, false, err
	}
	return d, true, nil
}
