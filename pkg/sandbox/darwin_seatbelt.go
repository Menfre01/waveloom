package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ============================================================================
// seatbeltBackend — macOS Seatbelt 后端
// ============================================================================

// seatbeltBackend 用系统自带 sandbox-exec + 动态 .sbpl profile 实现沙箱。
//
// 与 Linux bwrap 的语义差异(按平台降级):
//   - 无 tmpfs 等价物:目录遮蔽只能 deny file-read* → 读返回 EPERM,
//     而非 Linux 的"存在但为空"
//   - 文件遮蔽可 deny file-read* (literal path)
//   - 违规反馈:stderr 中的 EPERM 由 violation.go 归类为 read masked
//   - 环境变量剥离:Seatbelt 无 unsetenv 机制,Transform 在沙箱内
//     插入 /usr/bin/env -u <name> 逐条剥离
type seatbeltBackend struct {
	bin         string // sandbox-exec 路径
	runCombined func(name string, args ...string) ([]byte, error)
}

func newSeatbeltBackend() *seatbeltBackend {
	return &seatbeltBackend{bin: "sandbox-exec", runCombined: runCombinedDefault}
}

// Name 返回后端名。
func (b *seatbeltBackend) Name() string { return "seatbelt" }

// Probe 探测 seatbelt 可用性:
//  1. sandbox-exec 存在(macOS 系统自带)
//  2. 冒烟测试:最小 profile 运行 true
func (b *seatbeltBackend) Probe() error {
	bin, err := exec.LookPath(b.bin)
	if err != nil {
		return fmt.Errorf("sandbox: sandbox-exec not found (Seatbelt requires macOS): %w", err)
	}
	b.bin = bin

	// 冒烟测试:allow default 最小 profile,验证 sandbox-exec 基本可用
	if _, err := b.runCombined(b.bin, "-p", "(version 1)(allow default)", "/usr/bin/true"); err != nil {
		return fmt.Errorf("sandbox: seatbelt smoke test failed: %w", err)
	}
	return nil
}

// Transform 构造 sandbox-exec argv:
//
//	sandbox-exec -p <profile> /usr/bin/env -u <name1> -u <name2> ... bash -c "<cmd>"
//
// 无 --file/fd 需求(Seatbelt 遮蔽为 deny 规则,不存在路径无需占位),
// ExtraFiles 恒为 nil。
func (b *seatbeltBackend) Transform(shellBin string, args []string, cfg *Config, workspace string) ([]string, error) {
	profile := buildSeatbeltProfile(cfg, workspace)
	argv := []string{b.bin, "-p", profile}

	// env 包装:选项(-u)在前、赋值(TMPDIR=/tmp)在后——
	// macOS env 语法 [OPTION]... [NAME=VALUE]... COMMAND,顺序颠倒会把 -u 当命令名
	stripped := envVarsToStrip(cfg)
	argv = append(argv, "/usr/bin/env")
	for _, name := range stripped {
		argv = append(argv, "-u", name)
	}
	argv = append(argv, "TMPDIR=/tmp") // 沙箱内构建需要可写临时目录(对齐 bwrap --setenv)

	argv = append(argv, shellBin)
	argv = append(argv, args...)
	return argv, nil
}

// ============================================================================
// .sbpl profile 构造
// ============================================================================

// buildSeatbeltProfile 构造 Seatbelt 沙箱 profile。
//
// 结构(deny 优先于 allow,遮蔽靠 deny 规则):
//
//	(version 1)
//	(import "system.sb")                          ← 系统基础允许
//	(deny default)
//	(allow process*)                              ← 进程执行
//	(deny network*)                               ← 网络 off 时
//	(deny file-read* (literal 文件遮蔽))           ← 敏感文件不可读
//	(deny file-read* (subpath 目录遮蔽))           ← 敏感目录不可读(EPERM)
//	(allow file-read*)                            ← 只读根语义(全盘可读)
//	(allow file-write* (subpath workspace) (subpath /tmp))  ← 局部可写
//
// 注意:Seatbelt 是 first-match 语义(profile 中第一个匹配的 allow/deny 决定),
// 遮蔽 deny 必须放在全盘 allow 之前,否则 allow 先匹配导致遮蔽失效。
func buildSeatbeltProfile(cfg *Config, workspace string) string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n")
	sb.WriteString("(import \"system.sb\")\n\n")
	sb.WriteString("(deny default)\n")
	sb.WriteString("(allow process*)\n\n")

	// 网络:off → 全断;on → 直连
	if cfg.Network.Mode == NetworkModeOff {
		sb.WriteString("(deny network*)\n\n")
	} else {
		sb.WriteString("(allow network*)\n\n")
	}

	// 遮蔽(deny 优先,first-match 语义要求先于 allow):文件 → literal;
	// 目录 → subpath;不存在 → 跳过(与 Linux 一致,无内容可读)
	home, _ := os.UserHomeDir()
	maskSpecs := collectMaskSpecs(home, workspace, cfg)
	for _, ms := range maskSpecs {
		// macOS 路径符号链接(/tmp → /private/tmp 等):Seatbelt 按真实路径
		// 匹配,必须 EvalSymlinks 规范化,否则 literal/subpath 全部落空
		maskPath := realPath(ms.path)
		switch ms.kind {
		case maskFile:
			sb.WriteString("(deny file-read* (literal " + sbplString(maskPath) + "))\n")
		case maskDir:
			sb.WriteString("(deny file-read* (subpath " + sbplString(maskPath) + "))\n")
		case maskMissing:
			// 跳过
		}
	}
	// macOS 特有遮蔽(钥匙串/cookie/浏览器数据等——网络 on 时可被读走外传):
	// 目录存在 → subpath deny;不存在 → 跳过
	for _, p := range darwinMaskedDirsFiltered(home, workspace, cfg) {
		maskPath := realPath(p)
		if _, err := os.Stat(maskPath); err == nil {
			sb.WriteString("(deny file-read* (subpath " + sbplString(maskPath) + "))\n")
		}
	}
	sb.WriteString("\n")

	// 文件系统:只读全盘 + 工作区与 /tmp 可写(贴纸 A/B 的 Seatbelt 等价物)
	// 用通配符 file-read*/file-write*:Seatbelt 自动展开为当前系统存在的操作
	// 变量(新 macOS 移除了 file-write-setmode 等,显式列出会 unbound variable)
	sb.WriteString("(allow file-read*)\n")
	// /tmp 可能是指向 /private/tmp 的符号链接,两者都放行
	writePaths := []string{"/tmp", realPath("/tmp")}
	if workspace != "" {
		writePaths = append([]string{realPath(workspace)}, writePaths...)
	}
	// 默认缓存目录可写(沙箱内构建 go/npm/cargo 必需;缓存无敏感数据)。
	// 与 Linux --tmpfs 语义不同:Seatbelt 直接写宿主 ~/.cache(缓存本就该可写)
	cacheDir := filepath.Join(home, ".cache")
	writePaths = append(writePaths, realPath(cacheDir))
	// macOS GOCACHE 默认在 ~/Library/Caches(非 ~/.cache)——沙箱内 go 构建必需
	libraryCache := filepath.Join(home, "Library", "Caches")
	writePaths = append(writePaths, realPath(libraryCache))
	// 遮蔽路径集合(allowWrite 冲突检查用,四审 MED-1 对齐 bwrap)
	darwinMasked := darwinMaskedDirsFiltered(home, workspace, cfg)
	maskPaths := make([]string, 0, len(maskSpecs)+len(darwinMasked))
	for _, ms := range maskSpecs {
		maskPaths = append(maskPaths, realPath(ms.path))
	}
	for _, p := range darwinMasked {
		maskPaths = append(maskPaths, realPath(p))
	}
	// allowWrite 额外可写路径(与 Linux --bind 叠加语义一致)
	for _, p := range cfg.Filesystem.AllowWrite {
		abs := realPath(expandPath(p, home, workspace))
		if abs != "" {
			// 根目录拒绝(四审 HIGH-1 对齐)+ 遮蔽父子冲突(遮蔽优先)
			if abs == "/" || abs == home {
				continue
			}
			conflict := false
			for _, mp := range maskPaths {
				if mp == abs || strings.HasPrefix(mp, abs+string(filepath.Separator)) {
					conflict = true
					break
				}
			}
			if !conflict {
				writePaths = append(writePaths, abs)
			}
		}
	}
	sb.WriteString("(allow file-write*")
	for _, p := range writePaths {
		sb.WriteString(" (subpath " + sbplString(p) + ")")
	}
	sb.WriteString(")\n")

	return sb.String()
}

// darwinMaskedDirs 返回 macOS 特有的敏感目录(相对 home 展开)。
// 与 Linux 默认清单互补:这些路径承载钥匙串密码、HTTP cookie、
// 浏览器会话等,网络 on 时未遮蔽即可读走外传。
func darwinMaskedDirs(home string) []string {
	return []string{
		filepath.Join(home, "Library", "Keychains"),           // 钥匙串(所有密码)
		filepath.Join(home, "Library", "HTTPStorages"),        // HTTP cookie 存储
		filepath.Join(home, "Library", "Cookies"),             // cookie
		"/var/run/docker.sock",                                // 四审 HIGH-2:docker 逃逸面
		filepath.Join(home, ".docker", "run", "docker.sock"),  // Docker Desktop socket
		filepath.Join(home, "Library", "Application Support", "Google", "Chrome"),
		filepath.Join(home, "Library", "Application Support", "Firefox"),
		filepath.Join(home, "Library", "Application Support", "Microsoft", "Edge"),
		// 四审 MED-3:网络 on 可外传凭证面
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".gnupg"),
	}
}

// darwinMaskedDirsFiltered 返回经 filesystem.allowRead 显式放行过滤后的
// macOS 特有遮蔽目录。匹配语义同 Linux(filterDefaultMasks):allowRead 条目
// a 命中目录 m(精确相等,或 m 是 a 的后代)即移除 m。
func darwinMaskedDirsFiltered(home, workspace string, cfg *Config) []string {
	allowed := allowReadExpanded(home, workspace, cfg)
	if len(allowed) == 0 {
		return darwinMaskedDirs(home)
	}
	sep := string(filepath.Separator)
	var out []string
	for _, p := range darwinMaskedDirs(home) {
		hit := false
		for a := range allowed {
			if p == a || strings.HasPrefix(p, a+sep) {
				hit = true
				break
			}
		}
		if !hit {
			out = append(out, p)
		}
	}
	return out
}

// realPath 解析符号链接为真实路径(EvalSymlinks);失败时保留原值。
// Seatbelt 的 literal/subpath 按真实路径匹配,macOS 的 /tmp → /private/tmp
// 等链接若不解析会导致遮蔽与可写规则全部落空。
func realPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// sbplString 转义 Seatbelt 字符串字面量(引号与反斜杠)。
func sbplString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
