// Package memory 提供 AGENTS.md 持久记忆的发现和加载。
//
// 对标 Codex codex-rs/core/src/agents_md.rs 的设计：
//   - 从 CWD 向上遍历到 Git 根，收集路径上所有 AGENTS.md
//   - 加载 ~/.waveloom/AGENTS.md 作为全局记忆
//   - 一次性读盘，会话期间不变
//   - 注入位置为用户消息（非 system prompt）
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

const maxBytes = 64 * 1024 // 64KB

// Loader 负责发现和加载 AGENTS.md 文件。
type Loader struct {
	CWD     string // 当前工作目录（起点）
	HomeDir string // 用户主目录（用于 ~/.waveloom/AGENTS.md）
}

// NewLoader 创建一个新的 Loader。
func NewLoader(cwd, homeDir string) *Loader {
	return &Loader{CWD: cwd, HomeDir: homeDir}
}

// Load 发现并加载所有 AGENTS.md 文件，返回带来源标注的拼接文本。
func (l *Loader) Load() (text string, warnings []string, err error) {
	type part struct {
		path    string
		content string
	}
	var parts []part
	remaining := maxBytes

	// 1. 全局记忆
	if l.HomeDir != "" {
		globalPath := filepath.Join(l.HomeDir, ".waveloom", "AGENTS.md")
		content, warn, readErr := readFile(globalPath, remaining)
		if readErr != nil {
			return "", nil, fmt.Errorf("read global AGENTS.md: %w", readErr)
		}
		if content != "" {
			parts = append(parts, part{path: "~/.waveloom/AGENTS.md", content: content})
			remaining -= len(content)
		}
		warnings = append(warnings, warn...)
	}

	// 2. 层级发现
	absCWD, absErr := filepath.Abs(l.CWD)
	if absErr != nil {
		return "", nil, fmt.Errorf("resolve absolute cwd: %w", absErr)
	}

	projectRoot := pathutil.FindProjectRoot(absCWD)

	var dirs []string
	if projectRoot != "" {
		dirs = dirChain(projectRoot, absCWD)
	} else {
		dirs = []string{absCWD}
	}

	// 3. 加载各层 AGENTS.md
	for _, dir := range dirs {
		if remaining <= 0 {
			break
		}
		filePath := discoverAgentsMd(dir)
		if filePath == "" {
			continue
		}
		content, warn, readErr := readFile(filePath, remaining)
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("skip %s: %v", filePath, readErr))
			continue
		}
		if content != "" {
			parts = append(parts, part{path: filePath, content: content})
			remaining -= len(content)
		}
		warnings = append(warnings, warn...)
	}

	if len(parts) == 0 {
		return "", warnings, nil
	}

	// 4. 拼接
	blocks := make([]string, 0, len(parts))
	for _, p := range parts {
		blocks = append(blocks, "## "+p.path+"\n"+p.content)
	}
	body := strings.Join(blocks, "\n\n")

	text = fmt.Sprintf("# AGENTS.md instructions for %s\n\n<INSTRUCTIONS>\n\n%s\n</INSTRUCTIONS>", absCWD, body)

	if remaining <= 0 {
		text += "\n\n[AGENTS.md 内容被截断：已达到大小上限]"
		warnings = append(warnings, "AGENTS.md content truncated: max bytes limit reached")
	}

	return text, warnings, nil
}

// dirChain 返回从 root 到 leaf（含两端）的目录链。
func dirChain(root, leaf string) []string {
	var chain []string
	for dir := leaf; ; dir = filepath.Dir(dir) {
		chain = append([]string{dir}, chain...)
		if dir == root {
			break
		}
	}
	return chain
}

// discoverAgentsMd 在指定目录中查找 AGENTS.md。
// 返回文件绝对路径，未找到返回空字符串。
func discoverAgentsMd(dir string) string {
	path := filepath.Join(dir, "AGENTS.md")
	info, err := os.Stat(path)
	if err == nil && info.Mode().IsRegular() {
		return path
	}
	return ""
}

// readFile 读取文件内容，最多 maxBytes 字节。
// 返回 trim 后的 UTF-8 文本、warnings 和 error。
func readFile(path string, max int) (content string, warnings []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("read %s: %w", path, err)
	}

	if len(data) > max {
		data = data[:max]
	}

	if !utf8.Valid(data) {
		warnings = append(warnings, fmt.Sprintf(
			"%s contains invalid UTF-8; invalid sequences replaced", path))
	}

	content = strings.TrimSpace(string(data))
	return content, warnings, nil
}
