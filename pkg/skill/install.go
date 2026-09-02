package skill

// Install 实现远程 Skill 安装:从 git 仓库(URL)浅克隆指定 ref,
// 校验 SKILL.md 存在后拷贝到目标目录,并返回用于 skill.lock.json 的锁定条目。
//
// 设计约束(见 specs/wave-skill-install.md):
//   - 仅依赖系统 git 与标准库,不引入 go-git
//   - 任何步骤失败不留半成品:临时克隆目录与已拷贝内容均回滚
//   - lock 原子写(tmp + rename),防中断损坏

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// 安装失败的分类错误。上层 CLI 通过 errors.Is 判定并渲染 i18n 文案;
// 错误链保留底层 %w 细节(slog 排障用)。
var (
	ErrInvalidURL  = errors.New("invalid repository url")
	ErrRefNotFound = errors.New("ref not found in repository")
	ErrNoSKILLmd   = errors.New("no SKILL.md found at target path")
	ErrSkillExists = errors.New("skill already installed from a different source")
)

// installTimeout 是单次安装(git clone/fetch + 拷贝)的总超时。
const installTimeout = 60 * time.Second

// tmpClonePrefix 是临时克隆目录前缀(测试据此断言无泄漏)。
const tmpClonePrefix = "waveloom-skill-install-"

// InstallOptions 描述一次安装的全部输入。
type InstallOptions struct {
	URL     string // git 仓库地址或本地路径
	Ref     string // branch / tag / 40 位 commit SHA;空 = "main"
	SubPath string // 仓库内 skill 所在子目录;空 = 根目录
	Name    string // 显式覆盖安装名;空 = 从 SubPath 尾段或仓库名推导
	DestDir string // 安装目标 skills 目录(绝对路径,如 <project>/.waveloom/skills)
}

// LockEntry 是 skill.lock.json 中的一条安装记录。
type LockEntry struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	Ref            string `json:"ref"`
	ResolvedCommit string `json:"resolved_commit"`
	Path           string `json:"path"`         // 仓库内子目录("" = 根)
	InstalledAt    string `json:"installed_at"` // RFC3339 UTC
}

// ---------------------------------------------------------------------------
// Install
// ---------------------------------------------------------------------------

// Install 执行安装并返回 lock 条目。
func Install(ctx context.Context, opts InstallOptions) (LockEntry, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return LockEntry{}, fmt.Errorf("%w: empty url", ErrInvalidURL)
	}
	if strings.TrimSpace(opts.DestDir) == "" {
		return LockEntry{}, fmt.Errorf("%w: empty dest dir", ErrInvalidURL)
	}
	ref := opts.Ref
	if ref == "" {
		ref = "main"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return LockEntry{}, fmt.Errorf("git executable not found in PATH: %w", err)
	}

	// REGRESSION: Ctrl+C/SIGTERM 直接终止进程时 defer 清理不执行,临时克隆
	// 目录残留。绑定信号 → ctx 取消 → git 子进程被杀 → 走正常错误路径,
	// defer os.RemoveAll(tmpDir) 得以执行。
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	name := opts.Name
	if name == "" {
		name = deriveSkillName(opts.URL, opts.SubPath)
	}

	destSkillsDir, err := filepath.Abs(opts.DestDir)
	if err != nil {
		return LockEntry{}, fmt.Errorf("resolve dest dir: %w", err)
	}
	lockPath := filepath.Join(filepath.Dir(destSkillsDir), "skill.lock.json")
	lock, err := ReadLock(lockPath)
	if err != nil {
		return LockEntry{}, fmt.Errorf("read lock: %w", err)
	}

	// 幂等/冲突判定依据已有 lock 记录 + 目标目录当前状态。
	if prev, installed := lock[name]; installed && isDifferentSource(prev, opts.URL) {
		return LockEntry{}, fmt.Errorf(
			"%w: %q already installed from %s (remove it first or choose another name)",
			ErrSkillExists, name, prev.URL)
	}
	_, installed := lock[name]
	// REGRESSION: 目标目录已存在但 lock 无记录 = 手写/外部安装的 skill。
	// 直接 RemoveAll 覆盖会静默销毁用户文件,与 remove 拒绝手写 skill 的
	// 保护语义矛盾。拒绝并提示(与文档"手写 skill 不受管理"对齐)。
	if target := filepath.Join(destSkillsDir, name); dirExists(target) && !installed {
		return LockEntry{}, fmt.Errorf(
			"%w: %q exists in %s but has no skill.lock.json record (it was created manually — remove it first or choose another name)",
			ErrSkillExists, name, destSkillsDir)
	}

	tmpDir, err := os.MkdirTemp("", tmpClonePrefix+"*")
	if err != nil {
		return LockEntry{}, fmt.Errorf("create temp dir: %w", err)
	}
	// 清理失败不影响主流程(临时目录由 OS 兜底回收),显式忽略返回值。
	defer func() { _ = os.RemoveAll(tmpDir) }()

	resolvedCommit, err := cloneRepo(ctx, opts.URL, ref, tmpDir)
	if err != nil {
		return LockEntry{}, err
	}

	srcSkillDir := filepath.Join(tmpDir, filepath.FromSlash(opts.SubPath))
	skillMD := filepath.Join(srcSkillDir, "SKILL.md")
	if !fileExists(skillMD) {
		return LockEntry{}, fmt.Errorf("%w at %q (ref %s)", ErrNoSKILLmd, opts.SubPath, ref)
	}

	entry := LockEntry{
		Name:           name,
		URL:            opts.URL,
		Ref:            ref,
		ResolvedCommit: resolvedCommit,
		Path:           opts.SubPath,
		InstalledAt:    time.Now().UTC().Format(time.RFC3339),
	}

	// 同 commit 幂等:lock 有记录且目录完好 → 不重拷(lock 刷新 InstalledAt 无意义,直接返回)。
	if prev, installed := lock[name]; installed &&
		prev.ResolvedCommit == resolvedCommit && fileExists(filepath.Join(destSkillsDir, name, "SKILL.md")) {
		return entry, nil
	}

	targetDir := filepath.Join(destSkillsDir, name)
	if err := os.RemoveAll(targetDir); err != nil { // 覆盖语义:先清旧内容
		return LockEntry{}, fmt.Errorf("clear old install dir: %w", err)
	}
	if err := copyDir(srcSkillDir, targetDir); err != nil {
		_ = os.RemoveAll(targetDir) // 半成品回滚
		return LockEntry{}, fmt.Errorf("copy skill files: %w", err)
	}

	lock[name] = entry
	if err := WriteLock(lockPath, lock); err != nil {
		_ = os.RemoveAll(targetDir) // lock 失败 → 回滚文件,保持"lock 未写则无安装"
		return LockEntry{}, fmt.Errorf("write lock: %w", err)
	}
	return entry, nil
}

// deriveSkillName 从 URL 与 SubPath 推导安装名:
// SubPath 尾段 > 仓库名(去 .git 后缀)。
func deriveSkillName(url, subPath string) string {
	if subPath != "" {
		parts := strings.Split(strings.Trim(subPath, "/"), "/")
		if last := parts[len(parts)-1]; last != "" && last != "." && last != ".." {
			return last
		}
	}
	base := strings.TrimSuffix(filepath.Base(url), ".git")
	if base == "" || base == "." || base == "/" {
		return "unnamed-skill"
	}
	return base
}

// isDifferentSource 判断已有记录与新安装是否来源不同(仅比 URL,
// 同仓库换 ref 属更新而非冲突)。
func isDifferentSource(prev LockEntry, newURL string) bool {
	return prev.URL != newURL
}

// cloneRepo 将仓库克隆到 dst 并返回 resolved commit SHA。
// 40 位十六进制 ref 走 init+fetch 两步法(clone --branch 不接受 SHA);
// 其余(branch/tag)走 --depth 1 浅克隆。本地路径作为 origin 合法
// (git 原生支持),测试零外网。
func cloneRepo(ctx context.Context, url, ref, dst string) (string, error) {
	gitErr := func(out []byte, err error, step string) error {
		msg := strings.TrimSpace(string(out))
		switch {
		case strings.Contains(msg, "not found") && strings.Contains(msg, "upstream"):
			fallthrough
		case strings.Contains(msg, "Remote branch") && strings.Contains(msg, "not found"):
			fallthrough
		case strings.Contains(msg, "couldn't find remote ref"):
			return fmt.Errorf("%w: %s (%s)", ErrRefNotFound, ref, msg)
		default:
			return fmt.Errorf("git %s failed: %w\n%s", step, err, msg)
		}
	}

	var execErr error
	var err error
	run := func(step string, args ...string) bool {
		// REGRESSION: CommandContext 取消时仅杀 git 主进程,git-remote-https
		// 等子进程继承管道 fd 导致 CombinedOutput 读阻塞、进程卡死。改用
		// 进程组(Setpgid)+ ctx 监听,取消时 kill 整个进程组。
		cmd := exec.Command("git", args...)
		cmd.Dir = dst
		setSysProcAttrSkill(cmd)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		if execErr = cmd.Start(); execErr != nil {
			err = fmt.Errorf("git %s start failed: %w", step, execErr)
			return false
		}
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case execErr = <-done:
			if execErr != nil {
				err = gitErr(outBuf.Bytes(), execErr, step)
				return false
			}
		case <-ctx.Done():
			// 跨平台:先杀主进程(Windows 无进程组语义,TerminateProcess 即可);
			// Unix 再补组杀,覆盖继承管道 fd 的子进程(git-remote-https)。
			_ = cmd.Process.Kill()
			killProcessGroupSkill(cmd.Process.Pid)
			<-done
			err = fmt.Errorf("git %s interrupted: %w", step, ctx.Err())
			return false
		}
		return true
	}

	if isCommitRef(ref) {
		// 两步法:init 空 repo → fetch 指定 SHA → checkout FETCH_HEAD。
		if !run("init", "init", "--quiet") {
			return "", err
		}
		if !run("remote", "remote", "add", "origin", url) {
			return "", err
		}
		if !run("fetch", "fetch", "--depth", "1", "origin", ref) {
			return "", err
		}
		if !run("checkout", "checkout", "--quiet", "FETCH_HEAD") {
			return "", err
		}
	} else {
		if !run("clone", "clone", "--quiet", "--depth", "1",
			"--branch", ref, "--single-branch", url, ".") {
			return "", err
		}
	}

	revCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	revCmd.Dir = dst
	revBytes, revErr := revCmd.Output()
	if revErr != nil {
		return "", fmt.Errorf("resolve commit (rev-parse): %w", revErr)
	}
	return strings.TrimSpace(string(revBytes)), nil
}

// isCommitRef 判断 ref 是否为 40 位十六进制 commit SHA。
func isCommitRef(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 拷贝(标准库实现,跨平台安全)
// ---------------------------------------------------------------------------

// copyDir 递归拷贝 src → dst(dst 自动创建;不跟随 symlink 之外的特殊对象)。
// REGRESSION: 仓库根安装(SKILL.md 位于根)时曾把克隆产物的 .git 目录与仓库内
// 隐藏文件整体拷入安装目录,导致 Loader 的 scanSupportingFiles 将 .git 内部文件
// 全部列进 Supporting files 清单。此处跳过所有 "." 开头条目(根目录自身除外),
// 安装产物只保留 skill 内容。
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir // 跳过 .git 等隐藏目录及其全部内容
			}
			return nil // 跳过隐藏文件(.gitignore/.DS_Store 等)
		}
		target := filepath.Join(dst, rel)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			// symlink 一律按普通文件复制其指向内容不可取;跳过链接本体,
			// 保留 skill 主文件(SKILL.md/附属文件均不依赖 symlink)。
			return nil
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

// copyFile 单文件拷贝,保留权限位。
func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src) // #nosec G304 — src 来自受控克隆目录内部遍历
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck
	_, err = io.Copy(out, in)
	return err
}

// dirExists 判断路径是否为存在的目录(与 fileExists 对称,供安装前冲突检查用)。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ---------------------------------------------------------------------------
// skill.lock.json
// ---------------------------------------------------------------------------

// ReadLock 读取 lock 文件;不存在时返回空 map(nil 错误)——首次安装合法状态。
func ReadLock(lockPath string) (map[string]LockEntry, error) {
	data, err := os.ReadFile(lockPath) // #nosec G304 — lockPath 由调用方目录推导
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]LockEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries map[string]LockEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", lockPath, err)
	}
	if entries == nil {
		entries = map[string]LockEntry{}
	}
	return entries, nil
}

// WriteLock 原子写 lock(tmp + rename),防进程中断产生半写 JSON。
func WriteLock(lockPath string, entries map[string]LockEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := lockPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, lockPath)
}

// UpdateInstalled 按 lock 记录重新拉取(ref 不变,漂移到该 ref 当前 HEAD),
// 返回更新后的条目。name 无 lock 记录时返回 ErrNoSKILLmd 包装的明确错误由
// 上层区分提示(手写 skill 不可 update/remove)。
func UpdateInstalled(ctx context.Context, destSkillsDir, name string) (LockEntry, error) {
	lockPath := filepath.Join(filepath.Dir(destSkillsDir), "skill.lock.json")
	lock, err := ReadLock(lockPath)
	if err != nil {
		return LockEntry{}, err
	}
	prev, ok := lock[name]
	if !ok {
		return LockEntry{}, fmt.Errorf("no install record for %q in skill.lock.json", name)
	}
	opts := InstallOptions{URL: prev.URL, Ref: prev.Ref, SubPath: prev.Path, Name: name, DestDir: destSkillsDir}
	return Install(ctx, opts)
}

// RemoveInstalled 删除 lock 有记录的 skill(目录 + 条目)。
// 无记录时返回 false 表示拒绝(防误删本地手写 skill);dst 目录缺失视为成功。
func RemoveInstalled(destSkillsDir, name string) (bool, error) {
	lockPath := filepath.Join(filepath.Dir(destSkillsDir), "skill.lock.json")
	lock, err := ReadLock(lockPath)
	if err != nil {
		return false, err
	}
	if _, ok := lock[name]; !ok {
		return false, nil
	}
	if err := os.RemoveAll(filepath.Join(destSkillsDir, name)); err != nil {
		return false, err
	}
	delete(lock, name)
	return true, WriteLock(lockPath, lock)
}
