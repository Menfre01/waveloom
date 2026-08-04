package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ============================================================================
// bwrapBackend — Linux bubblewrap 后端
// ============================================================================

// bwrapBackend 用 bubblewrap 构造沙箱 argv:
// 只读根 + 工作区可写 + 敏感路径遮蔽(/dev/null|tmpfs)+ 环境变量剥离。
type bwrapBackend struct {
	bin      string   // bwrap 二进制路径
	hasArgv0 bool     // 支持 --argv0(bwrap >= 0.9.0)
	hasPerms bool     // 支持 --perms(bwrap >= 0.9.0)
	nullFile *os.File // 空数据源(--bind-data 遮蔽用,读内容为空)
	nullOnce sync.Once // nullFile 懒加载互斥(二审 Low-5)
	// runCombined 执行外部命令并返回合并输出(测试注入用)。
	runCombined func(name string, args ...string) ([]byte, error)
}

func newBwrapBackend() *bwrapBackend {
	return &bwrapBackend{bin: "bwrap", runCombined: runCombinedDefault}
}

// Name 返回后端名。
func (b *bwrapBackend) Name() string { return "bwrap" }

// NullFD 是 --bind-data 遮蔽使用的文件描述符编号(ExtraFiles 从 3 开始)。
const nullFD = 3

// ExtraFiles 返回 --bind-data 遮蔽所需的额外 fd(调用方附加到 exec.Cmd.ExtraFiles)。
// fd 指向 /dev/null:bwrap 读取内容为空 → 普通空文件 bind 遮蔽目标。
func (b *bwrapBackend) ExtraFiles() []*os.File {
	b.nullOnce.Do(func() {
		f, err := os.Open("/dev/null")
		if err != nil {
			return
		}
		b.nullFile = f
	})
	if b.nullFile == nil {
		return nil
	}
	return []*os.File{b.nullFile}
}

// runCombinedDefault 默认执行器:exec.Command + CombinedOutput。
func runCombinedDefault(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// apparmorSysctlPath 是 Ubuntu 24.04+ userns 限制的 sysctl 路径(测试可覆盖)。
var apparmorSysctlPath = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"

// ============================================================================
// 能力探测
// ============================================================================

// Probe 探测 bwrap 可用性:
//  1. 二进制存在
//  2. --help 解析 --argv0/--perms 能力
//  3. AppArmor userns 限制探测(Ubuntu 24.04+,给出修复指引)
//  4. 冒烟测试:验证 --ro-bind / / 基本挂载能力
func (b *bwrapBackend) Probe() error {
	bin, err := exec.LookPath(b.bin)
	if err != nil {
		return fmt.Errorf("sandbox: bwrap not found in PATH — %s; set failIfUnavailable=true to hard-fail", linuxBwrapInstallHint())
	}
	b.bin = bin

	if err := b.probeCapabilities(); err != nil {
		return err
	}
	if err := probeAppArmorUserns(); err != nil {
		return err
	}
	if err := b.probeSmokeTest(); err != nil {
		return err
	}
	return nil
}

// linuxBwrapInstallHint 生成 bwrap 缺失时的首次使用安装引导:
// 按发行版(/etc/os-release)给出安装命令;检测到 Flatpak 时提示可能已随其安装。
func linuxBwrapInstallHint() string {
	distro := ""
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "ID=") {
				distro = strings.Trim(strings.TrimPrefix(line, "ID="), `"`)
				break
			}
		}
	}

	hint := "install via '" + bwrapInstallCommandFor(distro) + "'"
	// Flatpak 用户:bubblewrap 是 Flatpak 沙箱的核心依赖,系统可能已安装但不在 PATH
	if _, flatpakErr := exec.LookPath("flatpak"); flatpakErr == nil {
		hint += "; Flatpak detected — bubblewrap may already be installed (check 'which bwrap')"
	}
	return hint
}

// bwrapInstallCommandFor 按发行版返回 bubblewrap 安装命令(测试可直调)。
func bwrapInstallCommandFor(distro string) string {
	switch distro {
	case "ubuntu", "debian", "pop", "linuxmint", "raspbian":
		return "sudo apt install bubblewrap"
	case "fedora", "rhel", "centos", "rocky", "almalinux":
		return "sudo dnf install bubblewrap"
	case "arch", "manjaro", "endeavouros":
		return "sudo pacman -S bubblewrap"
	case "alpine":
		return "sudo apk add bubblewrap"
	case "opensuse", "opensuse-tumbleweed", "suse":
		return "sudo zypper install bubblewrap"
	default:
		return "install bubblewrap from your distribution's package repository (e.g. 'sudo apt install bubblewrap' on Debian/Ubuntu)"
	}
}

// probeCapabilities 解析 bwrap --help 输出,探测 --argv0/--perms(v0.9.0+)。
// 缺失时不返回错误——Transform 走兼容 argv 构造。
func (b *bwrapBackend) probeCapabilities() error {
	out, err := b.runCombined(b.bin, "--help")
	if err != nil {
		return fmt.Errorf("sandbox: bwrap --help failed: %w", err)
	}
	help := string(out)
	b.hasArgv0 = strings.Contains(help, "--argv0")
	b.hasPerms = strings.Contains(help, "--perms")
	return nil
}

// probeAppArmorUserns 检测 Ubuntu 24.04+ 的 unprivileged userns 限制。
// sysctl 不存在(非 Ubuntu 24.04)或不可读时视为不拦截。
func probeAppArmorUserns() error {
	data, err := os.ReadFile(apparmorSysctlPath)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(data)) == "1" {
		return fmt.Errorf("sandbox: AppArmor blocks unprivileged user namespaces (Ubuntu 24.04+). " +
			"Temporary fix: 'sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0'. " +
			"Permanent fix: install an AppArmor profile allowing bwrap userns (see bubblewrap docs)")
	}
	return nil
}

// probeSmokeTest 冒烟测试:验证 --ro-bind / / 在当前内核/文件系统组合下可用。
func (b *bwrapBackend) probeSmokeTest() error {
	if _, err := b.runCombined(b.bin, "--ro-bind", "/", "/", "--bind", "/tmp", "/tmp", "true"); err != nil {
		return fmt.Errorf("sandbox: bwrap smoke test failed (--ro-bind / /): %w — user namespaces may be restricted by the kernel/container policy", err)
	}
	return nil
}

// ============================================================================
// Transform — argv 构造(贴纸叠贴顺序,不可重排)
// ============================================================================

// Transform 构造 bwrap 包装 argv:
//
//	bwrap --unshare-user --unshare-pid [--unshare-net]
//	      --ro-bind / /                        ← A: 大范围只读
//	      --bind <workspace> <workspace>       ← B: 局部可写
//	      --bind-data 3 <file>                 ← C: 敏感文件遮蔽(空内容 bind)
//	      --tmpfs <dir>                        ← C: 敏感目录遮蔽(空覆盖)
//	      --proc /proc --dev /dev --tmpfs /tmp
//	      --chdir <workspace> --setenv TMPDIR /tmp
//	      --cap-drop ALL [--cap-add ...] --die-with-parent --new-session
//	      --unsetenv <name> ...
//	      bash -c "<cmd>"
//
func (b *bwrapBackend) Transform(shellBin string, args []string, cfg *Config, workspace string) ([]string, error) {
	argv := []string{b.bin, "--unshare-user", "--unshare-pid"}
	if cfg.Network.Mode == NetworkModeOff {
		argv = append(argv, "--unshare-net")
	}

	// 贴纸 A: 大范围只读
	argv = append(argv, "--ro-bind", "/", "/")

	// 临时区先清空(--tmpfs /tmp 提前):后续挂载点(workspace/遮蔽/.cache/
	// allowWrite)若在 /tmp 下(TMPDIR 指向 /tmp 的环境),不会被空 tmpfs 覆盖。
	// REGRESSION: 原顺序 --tmpfs /tmp 在最后,workspace/home 在 /tmp 下时
	// 挂载点全部丢失 → "Can't chdir to workspace" + 遮蔽失效(容器实测)。
	// 提前后 tmpfs 内 mkdir 挂载点不再 EROFS(先清空再叠贴)。
	argv = append(argv, "--tmpfs", "/tmp")

	// 贴纸 B: 工作区可写
	if workspace != "" {
		argv = append(argv, "--bind", workspace, workspace)
	}

	home, _ := os.UserHomeDir()

	// 贴纸 C: 显式遮蔽(denyRead/credentials.files)+ 固定防写/防逃逸
	// (文件 → /dev/null,目录 → tmpfs)
	maskSpecs := collectMaskSpecs(home, workspace, cfg)
	for _, ms := range maskSpecs {
		switch ms.kind {
		case maskFile:
			// REGRESSION: --bind /dev/null 遮蔽在容器/部分内核环境返回
			// Permission denied(设备节点 bind 的 open 检查);--bind-data 让
			// bwrap 从 fd 读空内容生成普通空文件再 bind(实测 rc=0 空读)。
			// fd 3 指向 /dev/null(ExtraFiles),多个遮蔽复用同一 fd(读恒空)。
			argv = append(argv, "--bind-data", strconv.Itoa(nullFD), ms.path)
		case maskSocketFile:
			// socket 遮蔽:bwrap 无法 bind 覆盖已存在的 socket 目标——
			// 文件源 ensure_file() open(O_CREAT|O_WRONLY) 对 socket 失败,
			// 目录源 ensure_dir() 要求目标为目录(均实测 CI
			// "Can't create file/mkdir /var/run/docker.sock: ENOENT")。
			// 唯一可行:tmpfs 遮蔽父目录 —— 沙箱内 /var/run 清空,
			// docker.sock 不可达(遮蔽目标达成)。副作用:父目录其余内容
			// 在沙箱内不可见(安全优先,规格书 HIGH-2 docker.sock 逃逸)。
			// 必须用真实路径:内核挂载目标解析跟随绝对 symlink 时相对
			// 挂载 ns 根(新 tmpfs),/var/run → /run 在 setup 期解析为
			// 不存在的 /run → ENOENT(实测 "Can't mount tmpfs on
			// /newroot/var/run")。EvalSymlinks 得 /run,挂载真实目录。
			parent := filepath.Dir(ms.path)
			if real, err := filepath.EvalSymlinks(parent); err == nil {
				parent = real
			}
			argv = append(argv, "--tmpfs", parent)
		case maskDir:
			argv = append(argv, "--tmpfs", ms.path)
		case maskMissing:
			// 不存在路径:跳过遮蔽。
			// REGRESSION: --file <fd> 在 --ro-bind / / 之后创建目标会 EROFS
			// (bwrap 按 argv 顺序应用挂载操作,只读根上无法 open O_CREAT),
			// 实测导致所有沙箱命令失败。跳过是安全的:目标在只读根下,
			// 沙箱内命令无法创建它(EROFS),沙箱外无文件可读;构造阶段
			// 不 touch 文件,无 mkdir 逃逸窗口(规格书原 TOCTOU 论证不成立)。
			// 例外:workspace/.git/hooks 位于可写 workspace 内,缺失时跳过
			// 意味着沙箱内可创建该目录并植入 hook(git init/clone 恒建
			// hooks 目录,实际触发窗口极小;既有语义,非本次引入)。
			_ = ms // 保留分类便于日志/调试
		}
	}

	// allowWrite 可写叠加
	maskSeen := make(map[string]bool, len(maskSpecs))
	for _, ms := range maskSpecs {
		maskSeen[ms.path] = true
	}
	// 默认缓存目录:--tmpfs 空覆盖(沙箱内独立缓存,构建 go/npm/cargo 必需;
	// 宿主 ~/.cache 不受影响)。用户遮蔽优先。
	// REGRESSION: 目标不存在时 bwrap mkdir 失败——父目录(home)在只读根上
	// EROFS(实测: 容器 TMPDIR=/tmp 时 home 在 /tmp 下)。目录存在时 bwrap
	// 直接挂载不 mkdir ✓;缺失跳过(真实用户 ~/.cache 几乎总存在)。
	cacheDir := filepath.Join(home, ".cache")
	if maskSeen[cacheDir] {
		cacheDir = ""
	} else if _, err := os.Stat(cacheDir); err != nil {
		cacheDir = ""
	}
	if cacheDir != "" {
		argv = append(argv, "--tmpfs", cacheDir)
	}
	for _, p := range cfg.Filesystem.AllowWrite {
		abs := expandPath(p, home, workspace)
		if abs == "" {
			continue
		}
		// 四审 HIGH-1:allowWrite 为根目录时 --bind / / 会覆盖只读根
		// (ro-bind 贴纸被可写 bind 叠掉,全盘可写)——硬拒绝
		if abs == "/" || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
			slog.Warn("sandbox: allowWrite root path rejected (would override read-only root)", "path", p)
			continue
		}
		conflict := maskSeen[abs]
		// 二审 Medium-3:allowWrite 是遮蔽路径的父目录(如 allowWrite=~ 覆盖
		// 遮蔽的 ~/.ssh)时,子路径遮蔽会因父目录可写绑定而重新可见——
		// 父目录冲突同样遮蔽优先
		if !conflict {
			for _, ms := range maskSpecs {
				if strings.HasPrefix(ms.path, abs+string(filepath.Separator)) {
					conflict = true
					break
				}
			}
		}
		if conflict {
			// 反遮蔽防护:allowWrite 与遮蔽清单冲突(精确或父子路径)时遮蔽优先
			continue
		}
		argv = append(argv, "--bind", abs, abs)
	}

	// 基础文件系统(--tmpfs /tmp 已提前到挂载点之前)
	argv = append(argv, "--proc", "/proc", "--dev", "/dev")
	if workspace != "" {
		argv = append(argv, "--chdir", workspace)
	}
	argv = append(argv, "--setenv", "TMPDIR", "/tmp")
	// 通用环境变量注入(cfg.Env,配置驱动,不绑定任何工具)。
	// 位于 --unsetenv 剥离之前:与凭据剥离冲突的键已被 envVarsToSet 过滤,
	// 双重保证剥离优先。
	for _, kv := range envVarsToSet(cfg, home, workspace) {
		argv = append(argv, "--setenv", kv.Key, kv.Value)
	}

	// 能力:--cap-drop ALL + 按需加回
	argv = append(argv, "--cap-drop", "ALL")
	for _, cap := range cfg.Capabilities.Keep {
		argv = append(argv, "--cap-add", cap)
	}
	// 五审 M5:--die-with-parent + --unshare-pid 下,bwrap 退出即拆 PID namespace,
	// 沙箱内 & 后台子进程随主命令退出被杀(macOS seatbelt 子进程存活但受限)——平台差异,文档化
	argv = append(argv, "--die-with-parent", "--new-session")

	// 环境变量剥离(os.Environ() glob 展开 + credentials.envVars 叠加)
	for _, name := range envVarsToStrip(cfg) {
		argv = append(argv, "--unsetenv", name)
	}

	// 目标命令
	argv = append(argv, shellBin)
	argv = append(argv, args...)
	return argv, nil
}

// ============================================================================
// 遮蔽清单(显式配置 + 固定防写/防逃逸)
// ============================================================================

type maskKind int

const (
	maskFile maskKind = iota // 存在且为文件 → --bind /dev/null
	maskDir                  // 存在且为目录 → --tmpfs
	maskMissing              // 不存在 → --file 占位
	maskSocketFile           // 存在且为 socket → --ro-bind 空文件
)

type maskSpec struct {
	kind maskKind
	path string
}

// collectMaskSpecs 汇总遮蔽路径(显式 denyRead + credentials.files + 固定防写/防逃逸),
// 按存在性分类:文件 → /dev/null、目录 → tmpfs、不存在 → 跳过。
func collectMaskSpecs(home, workspace string, cfg *Config) []maskSpec {
	seen := make(map[string]bool)
	var out []maskSpec

	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		info, err := os.Stat(p)
		switch {
		case err != nil:
			out = append(out, maskSpec{kind: maskMissing, path: p})
		case info.IsDir():
			out = append(out, maskSpec{kind: maskDir, path: p})
		case info.Mode()&os.ModeSocket != 0:
			// socket 无法 --bind-data(open O_WRONLY socket 语义失败,实测 CI
			// "Can't create file at ...: No such file or directory");
			// 归入 maskSocketFile 走 --tmpfs 父目录遮蔽。
			out = append(out, maskSpec{kind: maskSocketFile, path: p})
		default:
			out = append(out, maskSpec{kind: maskFile, path: p})
		}
	}

	// 用户显式遮蔽(denyRead / credentials.files):优先级最高,先收集。
	for _, p := range cfg.Filesystem.DenyRead {
		add(expandPath(p, home, workspace))
	}
	for _, p := range cfg.Credentials.Files {
		add(expandPath(p, home, workspace))
	}

	// 固定内置遮蔽(防写/防逃逸,不可配置移除):
	//   - workspace/.git/hooks:防持久化注入(hooks 被执行,写入即逃逸)
	//     仅 workspace 非空时加入——空 workspace 下 Join 得相对路径
	//     ".git/hooks",遮蔽目标退化(挂载到错误位置),直接跳过
	//   - /var/run/docker.sock:可访问 = 完整逃逸(docker run -v /:/host)
	// 2026-09 决策(对齐 Claude Code / Codex):默认凭据读遮蔽移除,
	// 凭据防护由 denyRead / credentials.files 显式配置(推荐清单见文档)。
	if workspace != "" {
		add(filepath.Join(workspace, ".git", "hooks"))
	}
	add("/var/run/docker.sock")

	return out
}

// ============================================================================
// 路径展开(~ / //abs / ./ 或裸名)
// ============================================================================

// expandPath 按配置路径前缀语义展开:
//   - //abs → 绝对路径(去一个 /)
//   - ~/   → 家目录
//   - ./ 或裸名 → 项目根(workspace)
func expandPath(p, home, workspace string) string {
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	case strings.HasPrefix(p, "//"):
		return p[1:]
	case strings.HasPrefix(p, "/"):
		return p // 原生绝对路径(配置语法 //abs 之外,直接写绝对路径也支持)
	case strings.HasPrefix(p, "./"):
		return filepath.Join(workspace, strings.TrimPrefix(p, "./"))
	default:
		return filepath.Join(workspace, p)
	}
}

// ============================================================================
// 环境变量剥离
// ============================================================================

// stripEnvPatterns 默认剥离模式。
// /*KEY* 不列入——模式过宽会误伤 KEYBOARD_* 等;API key 由 *_API_KEY /
// *_API_SECRET 后缀 + LLM provider 前缀覆盖;含密码 URL 单独列出。
var stripEnvPatterns = []string{
	"*TOKEN*", "*SECRET*", "*PASSWORD*", "*_API_KEY", "*_API_SECRET", "SSH_AUTH_SOCK",
	"AWS_*", "GH_*", "GITHUB_*", "NPM_*", "DOCKER_*", "GCP_*", "AZURE_*",
	"OPENAI_*", "ANTHROPIC_*", "DEEPSEEK_*", "GEMINI_*", "KIMI_*",
	"DATABASE_URL", "REDIS_URL", "RABBITMQ_URL",
}

// stripEnvExcludes 宽模式(*TOKEN* 等)的排除名单:
//   - TOKENIZERS_PARALLELISM:HF tokenizer 配置开关,非机密,含 "TOKEN" 被误伤
//   - PASSWORD_STORE_DIR:pass 密码库目录路径,非密码本身,含 "PASSWORD" 被误伤
var stripEnvExcludes = map[string]bool{
	"TOKENIZERS_PARALLELISM": true,
	"PASSWORD_STORE_DIR":     true,
}

// envVarsToStrip 扫描 os.Environ(),按默认模式 glob 展开 + 叠加
// credentials.envVars(合并去重,排序保证确定性输出)。
func envVarsToStrip(cfg *Config) []string {
	seen := make(map[string]bool)
	var out []string

	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	for _, kv := range os.Environ() {
		eq := strings.Index(kv, "=")
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		if envNameStripped(name, cfg) {
			add(name)
		}
	}
	for _, v := range cfg.Credentials.EnvVars {
		add(v)
	}
	sort.Strings(out)
	return out
}

// envNameStripped 判定环境变量名是否命中凭据剥离(默认模式 + credentials.envVars)。
// excludes 名单(TOKENIZERS_PARALLELISM 等)优先,命中即不剥离。
// envVarsToSet 复用此判定:与剥离冲突的 env 注入被忽略(剥离优先,防回填)。
func envNameStripped(name string, cfg *Config) bool {
	if stripEnvExcludes[name] {
		return false
	}
	for _, pattern := range stripEnvPatterns {
		if matchStripPattern(name, pattern) {
			return true
		}
	}
	for _, v := range cfg.Credentials.EnvVars {
		if v == name {
			return true
		}
	}
	return false
}

// envKV 是沙箱内环境变量注入的键值对。
type envKV struct {
	Key   string
	Value string
}

// envVarsToSet 返回配置的沙箱内环境变量注入列表(排序保证确定性)。
// 通用机制,不绑定任何工具:典型用途是构建工具缓存重定向
// (go 的 GOPATH/GOMODCACHE/GOCACHE、npm 的 npm_config_cache 等)。
// 值按路径前缀语义展开(~/、//abs、/abs、./workspace 相对);
// 非路径值(如 GOPROXY 的 URL)原样注入。
// 键命中凭据剥离清单时忽略(剥离优先)。
func envVarsToSet(cfg *Config, home, workspace string) []envKV {
	var out []envKV
	for k, v := range cfg.Env {
		if envNameStripped(k, cfg) {
			continue
		}
		out = append(out, envKV{Key: k, Value: envValueExpand(v, home, workspace)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// envValueExpand 展开 env 配置值:路径前缀(~/、//、/、./)按路径语义,
// 其余(裸名/URL 等)原样注入。
func envValueExpand(v, home, workspace string) string {
	switch {
	case v == "~" || strings.HasPrefix(v, "~/"):
		return filepath.Join(home, strings.TrimPrefix(v, "~"))
	case strings.HasPrefix(v, "//"):
		return v[1:]
	case strings.HasPrefix(v, "/"):
		return v
	case strings.HasPrefix(v, "./"):
		return filepath.Join(workspace, strings.TrimPrefix(v, "./"))
	default:
		return v
	}
}

// matchStripPattern 匹配环境变量剥离模式(* 通配)。
func matchStripPattern(name, pattern string) bool {
	if pattern == name {
		return true
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) >= 2 {
		return strings.Contains(name, pattern[1:len(pattern)-1])
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(name, strings.TrimPrefix(pattern, "*"))
	}
	return false
}
