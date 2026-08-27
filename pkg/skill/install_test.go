package skill

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 测试 fixture:本地 git 仓库(零外网依赖)
// ---------------------------------------------------------------------------

// gitFixture 是测试仓库的路径与初始 commit 的集合。
type gitFixture struct {
	RepoPath   string // 仓库根目录
	InitCommit string // 首个 commit SHA(tag v1.0 指向)
}

// runGit 在指定目录执行 git 命令,失败时 panic(t.Fatal 语义)。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// createGitRepo 创建最小 skill 仓库:
// SKILL.md 位于根目录(名为 test-skill),打 tag v1.0。
func createGitRepo(t *testing.T) gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	writeFixtureFile(t, filepath.Join(repo, "SKILL.md"),
		"---\nname: test-skill\ndescription: a test skill\n---\n\n# Test Skill\n\nbody text.\n")
	gitEnv(t, repo)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.email=test@test.com", "-c", "user.name=test", "commit", "-m", "init")
	initCommit := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "tag", "v1.0")
	return gitFixture{RepoPath: repo, InitCommit: initCommit}
}

// gitEnv 关闭 gpg/签名等本地配置干扰,保证 commit 身份确定性。
func gitEnv(t *testing.T, dir string) {
	t.Helper()
	cfgs := [][2]string{
		{"commit.gpgsign", "false"},
		{"tag.gpgsign", "false"},
	}
	for _, c := range cfgs {
		cmd := exec.Command("git", "config", c[0], c[1])
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config %s=%s: %v\n%s", c[0], c[1], err, out)
		}
	}
}

// addSecondCommit 在 main 上追加一个新 commit(返回新 SHA),用于 ref 变更场景。
func addSecondCommit(t *testing.T, f gitFixture) string {
	t.Helper()
	writeFixtureFile(t, filepath.Join(f.RepoPath, "CHANGELOG.md"), "# updated\n")
	runGit(t, f.RepoPath, "add", ".")
	runGit(t, f.RepoPath, "-c", "user.email=test@test.com", "-c", "user.name=test", "commit", "-m", "update")
	return runGit(t, f.RepoPath, "rev-parse", "HEAD")
}

// writeFile 写入文件(自动创建父目录)。
func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installOpts 构造指向 fixture 仓库的 InstallOptions(name 显式指定便于断言)。
func installOpts(f gitFixture, destDir string) InstallOptions {
	return InstallOptions{
		URL:     f.RepoPath,
		Ref:     "main",
		Name:    "test-skill",
		DestDir: destDir,
	}
}

// lockPathFor 推导 DestDir 对应的 lock 文件路径(与生产同规则)。
func lockPathFor(destDir string) string {
	return filepath.Join(filepath.Dir(destDir), "skill.lock.json")
}

// requireNoSkillmdError 断言 err 链中包含 ErrNoSKILLmd。
func requireNoSKILLmdError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrNoSKILLmd) {
		t.Fatalf("err = %v, want ErrNoSKILLmd", err)
	}
}

// ---------------------------------------------------------------------------
// Install
// ---------------------------------------------------------------------------

func TestInstall_HappyPath(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	entry, err := Install(context.Background(), installOpts(f, dest))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	// SKILL.md 落位
	got, readErr := os.ReadFile(filepath.Join(dest, "test-skill", "SKILL.md"))
	if readErr != nil {
		t.Fatalf("installed SKILL.md missing: %v", readErr)
	}
	if !strings.Contains(string(got), "test-skill") {
		t.Errorf("SKILL.md content unexpected: %q", got)
	}
	// lock 字段正确性
	if entry.Name != "test-skill" {
		t.Errorf("Name = %q", entry.Name)
	}
	if entry.ResolvedCommit != f.InitCommit {
		t.Errorf("ResolvedCommit = %q, want %q", entry.ResolvedCommit, f.InitCommit)
	}
	if entry.Ref != "main" || entry.URL != f.RepoPath || entry.Path != "" {
		t.Errorf("entry = %+v", entry)
	}
	if entry.InstalledAt == "" {
		t.Error("InstalledAt empty")
	}
}

func TestInstall_TagRef(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	opts := installOpts(f, dest)
	opts.Ref = "v1.0"
	entry, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("Install(v1.0): %v", err)
	}
	if entry.ResolvedCommit != f.InitCommit {
		t.Errorf("ResolvedCommit = %q, want tag v1.0 → %q", entry.ResolvedCommit, f.InitCommit)
	}
	if entry.Ref != "v1.0" {
		t.Errorf("Ref = %q, want v1.0", entry.Ref)
	}
}

func TestInstall_CommitRef_FetchTwoStep(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	opts := installOpts(f, dest)
	opts.Ref = f.InitCommit // 40 位 SHA:clone --branch 不支持,走 fetch 两步法
	entry, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("Install(commit): %v", err)
	}
	if entry.ResolvedCommit != f.InitCommit {
		t.Errorf("ResolvedCommit = %q, want %q", entry.ResolvedCommit, f.InitCommit)
	}
}

func TestInstall_SubPath_NameDerived(t *testing.T) {
	f := createGitRepo(t)
	writeFixtureFile(t, filepath.Join(f.RepoPath, "nested", "skill-a", "SKILL.md"),
		"---\nname: skill-a\ndescription: nested\n---\n\nnested body\n")
	runGit(t, f.RepoPath, "add", ".")
	runGit(t, f.RepoPath, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "nested")

	dest := t.TempDir()
	opts := installOpts(f, dest)
	opts.SubPath = "nested/skill-a"
	opts.Name = "" // 不显式指定 → 从 SubPath 尾段推导
	entry, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("Install(SubPath): %v", err)
	}
	if entry.Name != "skill-a" {
		t.Errorf("Name = %q, want derived %q", entry.Name, "skill-a")
	}
	if entry.Path != "nested/skill-a" {
		t.Errorf("Path = %q", entry.Path)
	}
	if _, err := os.Stat(filepath.Join(dest, "skill-a", "SKILL.md")); err != nil {
		t.Errorf("nested SKILL.md not installed: %v", err)
	}
}

func TestInstall_IdempotentSameCommit(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	first, err := Install(context.Background(), installOpts(f, dest))
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}
	mtime1 := mtimeOf(t, filepath.Join(dest, "test-skill", "SKILL.md"))

	second, err := Install(context.Background(), installOpts(f, dest))
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if second.ResolvedCommit != first.ResolvedCommit {
		t.Errorf("second ResolvedCommit = %q, want same", second.ResolvedCommit)
	}
	if mtime2 := mtimeOf(t, filepath.Join(dest, "test-skill", "SKILL.md")); !mtime1.Equal(mtime2) {
		t.Error("file rewritten on idempotent install (mtime changed)")
	}
}

func TestInstall_RefChange_Overwrites(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	if _, err := Install(context.Background(), installOpts(f, dest)); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	newCommit := addSecondCommit(t, f)

	entry, err := Install(context.Background(), installOpts(f, dest))
	if err != nil {
		t.Fatalf("re-Install: %v", err)
	}
	if entry.ResolvedCommit != newCommit {
		t.Errorf("ResolvedCommit = %q, want new %q", entry.ResolvedCommit, newCommit)
	}
	if _, err := os.Stat(filepath.Join(dest, "test-skill", "CHANGELOG.md")); err != nil {
		t.Errorf("new file not present after overwrite: %v", err)
	}
}

func TestInstall_NoSKILLmd_FailsClean(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	opts := installOpts(f, dest)
	opts.SubPath = "nonexistent"

	_, err := Install(context.Background(), opts)
	requireNoSKILLmdError(t, err)
	// 目标目录无残留
	if _, statErr := os.Stat(filepath.Join(dest, "test-skill")); !os.IsNotExist(statErr) {
		t.Errorf("partial install left behind: %v", statErr)
	}
	// lock 未写入
	if _, statErr := os.Stat(lockPathFor(dest)); !os.IsNotExist(statErr) {
		t.Errorf("lock written on failure: %v", statErr)
	}
}

func TestInstall_RefNotFound(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	opts := installOpts(f, dest)
	opts.Ref = "no-such-ref"

	_, err := Install(context.Background(), opts)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
}

func TestInstall_InvalidURL(t *testing.T) {
	dest := t.TempDir()
	opts := InstallOptions{URL: "", Ref: "main", Name: "x", DestDir: dest}
	if _, err := Install(context.Background(), opts); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("err = %v, want ErrInvalidURL", err)
	}
}

// TestInstall_CleanupOnFail 失败后临时克隆目录不残留。
func TestInstall_CleanupOnFail(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	opts := installOpts(f, dest)
	opts.Ref = "no-such-ref"

	if _, err := Install(context.Background(), opts); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
	tmpBase := os.TempDir()
	entries, readErr := os.ReadDir(tmpBase)
	if readErr != nil {
		t.Skipf("cannot read tmp dir: %v", readErr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "waveloom-skill-install-") {
			t.Errorf("temp clone dir leaked: %s", e.Name())
		}
	}
}

func TestInstall_DiffURLConflict(t *testing.T) {
	f := createGitRepo(t)
	dest := t.TempDir()
	if _, err := Install(context.Background(), installOpts(f, dest)); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	// 同名不同来源 → 冲突
	other := createGitRepoNamed(t, "other-repo")
	opts := installOpts(other, dest)
	_, err := Install(context.Background(), opts)
	if !errors.Is(err, ErrSkillExists) {
		t.Fatalf("err = %v, want ErrSkillExists", err)
	}
}

// createGitRepoNamed 创建指定目录名的独立仓库(用于冲突场景)。
func createGitRepoNamed(t *testing.T, name string) gitFixture {
	t.Helper()
	f := createGitRepo(t)
	// createGitRepo 已用独立 TempDir;仅需把 SKILL.md 内容区分开避免幂等误判
	newPath := filepath.Join(filepath.Dir(f.RepoPath), name)
	if err := os.Rename(f.RepoPath, newPath); err != nil {
		t.Fatal(err)
	}
	f.RepoPath = newPath
	return f
}

// mtimeOf 返回文件修改时间。
func mtimeOf(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// ---------------------------------------------------------------------------
// Lock 读写
// ---------------------------------------------------------------------------

func TestLock_RoundTrip(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "skill.lock.json")
	entries := map[string]LockEntry{
		"a": {Name: "a", URL: "https://example.com/a.git", Ref: "v1", ResolvedCommit: "abc123", Path: "", InstalledAt: "2026-01-01T00:00:00Z"},
	}
	if err := WriteLock(lock, entries); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	got, err := ReadLock(lock)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	e, ok := got["a"]
	if !ok {
		t.Fatal("entry 'a' missing after roundtrip")
	}
	if e.ResolvedCommit != "abc123" || e.Ref != "v1" || e.URL != "https://example.com/a.git" {
		t.Errorf("roundtrip mismatch: %+v", e)
	}
}

func TestLock_MissingReturnsEmpty(t *testing.T) {
	got, err := ReadLock(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("ReadLock(missing) = %v, want nil error", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestLock_AtomicWrite_NoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "skill.lock.json")
	if err := WriteLock(lock, map[string]LockEntry{}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("tmp file leftover: %s", e.Name())
		}
	}
}

func TestLock_CorruptJSONReturnsError(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "broken.json")
	writeFile(t, lock, "{not json")
	if _, err := ReadLock(lock); err == nil {
		t.Fatal("ReadLock(corrupt) = nil error, want parse error")
	}
}

// ---------------------------------------------------------------------------
// 回归防护:安装结果必须能被现有 Loader 发现
// ---------------------------------------------------------------------------

func TestRegression_SkillAddVisibleToLoader(t *testing.T) {
	f := createGitRepo(t)
	// 把安装目标布置成项目结构:<projectRoot>/.waveloom/skills/
	// projectRoot 必须含 .git(FindProjectRoot 的判定条件),故在临时项目内
	// git init 一层空仓库,与真实使用场景一致。
	projectRoot := t.TempDir()
	runGit(t, projectRoot, "init", "-b", "main")
	skillsDir := filepath.Join(projectRoot, ".waveloom", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), installOpts(f, skillsDir)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	loader := NewLoader(projectRoot, filepath.Join(projectRoot, "home"), "", "medium", nil)
	infos, listErr := loader.List()
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	found := false
	for _, info := range infos {
		if info.Name == "test-skill" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(infos))
		for _, i := range infos {
			names = append(names, i.Name)
		}
		t.Errorf("installed skill not discovered by Loader; found: %v", names)
	}
}
