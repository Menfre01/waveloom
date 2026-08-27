package main

// waveloom skill 子命令:远程 Skill 安装 + 版本锁定。
// 结构对齐 runMCPCommand(独立 FlagSet 自解析);输出经 Messages i18n。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
	"github.com/Menfre01/waveloom/pkg/skill"
)

// runSkill 处理 `waveloom skill <add|list|update|remove>` 子命令。
// args 为去除 "skill" 后的剩余参数;调用点在 createLLMClient 之前,无需 LLM 依赖。
func runSkill(args []string, homeDir string, loc Locale) {
	m := messagesFor(loc)
	if len(args) == 0 {
		printSkillUsage(m)
		os.Exit(1)
	}
	cwd, _ := os.Getwd()
	switch args[0] {
	case "add":
		skillAdd(args[1:], cwd, homeDir, m)
	case "list":
		skillList(cwd, homeDir, m)
	case "update":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, localizedMsg(m.SkillCmdNeedName, "usage: waveloom skill update <name>"))
			os.Exit(1)
		}
		skillUpdate(args[1], cwd, homeDir, m)
	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, localizedMsg(m.SkillCmdNeedName, "usage: waveloom skill remove <name>"))
			os.Exit(1)
		}
		skillRemove(args[1], cwd, homeDir, m)
	default:
		fmt.Fprintf(os.Stderr, "%s: %s\n",
			localizedMsg(m.SkillCmdUnknownSub, "unknown subcommand"), args[0])
		printSkillUsage(m)
		os.Exit(1)
	}
}

func printSkillUsage(m *Messages) {
	usage := fmt.Sprintf(`%s

  waveloom skill add    <url>[@ref] [--path <dir>] [--name <name>] [--global]
  waveloom skill list
  waveloom skill update <name>
  waveloom skill remove <name>`,
		localizedMsg(m.SkillCmdUsage, "manage skills (install from git, list, update, remove)"))
	fmt.Println(usage)
}

// ---------------------------------------------------------------------------
// add
// ---------------------------------------------------------------------------

func skillAdd(args []string, cwd, homeDir string, m *Messages) {
	fs := flag.NewFlagSet("waveloom skill add", flag.ExitOnError)
	subPath := fs.String("path", "", "subdirectory inside the repository")
	name := fs.String("name", "", "override installed skill name")
	global := fs.Bool("global", false, "install to ~/.waveloom/skills instead of project level")
	// 支持 flag 与位置参数任意交错(Go flag 遇首个位置参数即停,
	// 手动分区后仅把 flag 喂给 FlagSet)。
	flags, pos := partitionFlagArgs(args, map[string]bool{
		"-path": true, "--path": true, "-name": true, "--name": true,
	})
	if err := fs.Parse(flags); err != nil {
		os.Exit(1)
	}
	rest := pos
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, localizedMsg(m.SkillCmdNeedURL, "usage: waveloom skill add <url>[@ref]"))
		os.Exit(1)
	}

	url, ref := parseURLRef(rest[0])

	destDir, err := skillsTargetDir(cwd, homeDir, *global)
	if err != nil {
		fmt.Fprintln(os.Stderr, humanizeSkillErr(err))
		os.Exit(1)
	}

	opts := skill.InstallOptions{
		URL:     url,
		Ref:     ref,
		SubPath: *subPath,
		Name:    *name,
		DestDir: destDir,
	}
	entry, installErr := skill.Install(context.Background(), opts)
	if installErr != nil {
		fmt.Fprintln(os.Stderr, renderInstallErr(installErr, m))
		os.Exit(1)
	}

	source := entry.URL
	if entry.Path != "" {
		source += "#" + entry.Path
	}
	fmt.Printf("%s\n  %s\n  %s %s@%s\n  %s %s\n",
		localizedMsg(m.SkillCmdAdded, "✓ skill installed"),
		entry.Name,
		localizedMsg(m.SkillCmdSource, "source:"), source, shortCommit(entry.ResolvedCommit),
		localizedMsg(m.SkillCmdLocation, "location:"), filepath.Join(destDir, entry.Name),
	)
}

// partitionFlagArgs 把参数拆成 flag 与位置参数两组(flag 可在位置参数之后);
// valueFlags 中声明的短横线参数带一个值。
func partitionFlagArgs(args []string, valueFlags map[string]bool) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		if valueFlags[a] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positional
}

// parseURLRef 拆分 <url>[@ref];ref 缺省为空(Install 内回退 main)。
func parseURLRef(arg string) (url, ref string) {
	// 仅常规 URL 切分 <url>@<ref>:scp-like(git@host:path)与带凭据的
	// https://user:token@host/repo.git 均含 @ 但不是 ref 分隔符,须排除。
	// 判定:最后 @ 之前不含 "://"(排除 https://token@host),且不以 "git@" 开头。
	if i := strings.LastIndex(arg, "@"); i > 0 &&
		!strings.Contains(arg[:i], "://") &&
		!strings.HasPrefix(arg, "git@") {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// skillsTargetDir 返回安装目标 skills 目录:项目级 .waveloom/skills 或用户级 ~/.waveloom/skills。
func skillsTargetDir(cwd, homeDir string, global bool) (string, error) {
	base := ""
	if global {
		if homeDir == "" {
			return "", errors.New("cannot determine home directory")
		}
		base = filepath.Join(homeDir, ".waveloom")
	} else {
		projectRoot := pathutil.FindProjectRoot(cwd)
		if projectRoot == "" {
			// 无 .git 的目录按 cwd 就地作为项目根。
			var absErr error
			projectRoot, absErr = filepath.Abs(cwd)
			if absErr != nil {
				return "", absErr
			}
		}
		base = filepath.Join(projectRoot, ".waveloom")
	}
	return filepath.Join(base, "skills"), nil
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func skillList(cwd, homeDir string, m *Messages) {
	loader := skill.NewLoader(cwd, homeDir, "", "medium", nil)
	infos, err := loader.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, humanizeSkillErr(err))
		os.Exit(1)
	}

	// lock 对账标注远程来源;读失败降级为全部显示 local(不中断)。
	lockByDir := map[string][]skill.LockEntry{} // skills 目录 → entries
	for _, dir := range allSkillsDirs(cwd, homeDir) {
		if lock, lockErr := skill.ReadLock(filepath.Join(filepath.Dir(dir), "skill.lock.json")); lockErr == nil {
			for _, e := range lock {
				lockByDir[dir] = append(lockByDir[dir], e)
			}
		}
	}

	fmt.Printf("%-24s %s\n",
		localizedMsg(m.SkillCmdListName, "NAME"),
		localizedMsg(m.SkillCmdListSource, "SOURCE"))
	for _, info := range infos {
		src := localizedMsg(m.SkillCmdLocal, "(local)")
		// Source 形如 <skills>/<name>/SKILL.md,上跳两级才是 skills 目录(lock 所在同级)。
		dir := filepath.Dir(filepath.Dir(info.Source))
		// 安装名 = 目录名(frontmatter name 可能不同,update/remove 均按目录名)。
		dirName := filepath.Base(filepath.Dir(info.Source))
		for _, e := range lockByDir[dir] {
			if e.Name == dirName {
				src = e.URL + "@" + shortCommit(e.ResolvedCommit)
				break
			}
		}
		fmt.Printf("%-24s %s\n", info.Name, src)
	}
}

// allSkillsDirs 返回 loader 扫描的两个 waveloom skills 目录(user + project),用于对账。
func allSkillsDirs(cwd, homeDir string) []string {
	var dirs []string
	if homeDir != "" {
		dirs = append(dirs, filepath.Join(homeDir, ".waveloom", "skills"))
	}
	projectRoot := pathutil.FindProjectRoot(cwd)
	if projectRoot == "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			projectRoot = abs
		}
	}
	if projectRoot != "" {
		dirs = append(dirs, filepath.Join(projectRoot, ".waveloom", "skills"))
	}
	return dirs
}

// ---------------------------------------------------------------------------
// update / remove
// ---------------------------------------------------------------------------

func skillUpdate(name string, cwd, homeDir string, m *Messages) {
	destDir, err := skillsTargetDirForName(cwd, homeDir, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, humanizeSkillErr(err))
		os.Exit(1)
	}
	prevLockPath := filepath.Join(filepath.Dir(destDir), "skill.lock.json")
	prev := findLockEntry(prevLockPath, name)

	entry, updateErr := skill.UpdateInstalled(context.Background(), destDir, name)
	if updateErr != nil {
		fmt.Fprintln(os.Stderr, renderInstallErr(updateErr, m))
		os.Exit(1)
	}
	if prev != nil && prev.ResolvedCommit == entry.ResolvedCommit {
		fmt.Printf("%s\n", fmt.Sprintf(localizedMsg(m.SkillCmdUpToDate,
			"already up to date (%s)"), shortCommit(entry.ResolvedCommit)))
		return
	}
	fmt.Printf("%s %s (%s → %s)\n",
		localizedMsg(m.SkillCmdUpdated, "✓ updated"),
		name,
		shortCommitOrDash(prev, prev.ResolvedCommit),
		shortCommit(entry.ResolvedCommit),
	)
}

func skillRemove(name string, cwd, homeDir string, m *Messages) {
	destDir, err := skillsTargetDirForName(cwd, homeDir, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, humanizeSkillErr(err))
		os.Exit(1)
	}
	removed, removeErr := skill.RemoveInstalled(destDir, name)
	if removeErr != nil {
		fmt.Fprintln(os.Stderr, humanizeSkillErr(removeErr))
		os.Exit(1)
	}
	if !removed {
		// lock 无记录 → 手写或非本工具安装,拒绝删除防误伤。
		fmt.Fprintln(os.Stderr, localizedMsg(m.SkillCmdNotTracked,
			"✗ no install record for '"+name+"' in skill.lock.json (manual installs are not removed by this command)"))
		os.Exit(1)
	}
	fmt.Printf("%s %s\n", localizedMsg(m.SkillCmdRemoved, "✓ removed"), name)
}

// skillsTargetDirForName 探测 name 安装在哪个层级(先 project 后 user),返回其 skills 目录。
func skillsTargetDirForName(cwd, homeDir, name string) (string, error) {
	dirs := allSkillsDirs(cwd, homeDir)
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir, nil
		}
	}
	// 未装:默认行为取向与 add 相反——update/remove 需要既有记录,逐目录看 lock。
	for _, dir := range dirs {
		lock := findLockEntry(filepath.Join(filepath.Dir(dir), "skill.lock.json"), name)
		if lock != nil {
			return dir, nil
		}
	}
	return dirs[len(dirs)-1], nil // 兜底:返回 project 级,让下游报"无记录"
}

func findLockEntry(lockPath, name string) *skill.LockEntry {
	lock, err := skill.ReadLock(lockPath)
	if err != nil {
		return nil
	}
	if e, ok := lock[name]; ok {
		return &e
	}
	return nil
}

// ---------------------------------------------------------------------------
// 错误渲染
// ---------------------------------------------------------------------------

func renderInstallErr(err error, m *Messages) string {
	switch {
	case errors.Is(err, skill.ErrRefNotFound):
		return localizedMsg(m.SkillCmdErrRef, "✗ ref not found in repository") + ": " + err.Error()
	case errors.Is(err, skill.ErrNoSKILLmd):
		return localizedMsg(m.SkillCmdErrNoSKILLmd,
			"✗ SKILL.md not found at the target path") + " — " + err.Error()
	case errors.Is(err, skill.ErrSkillExists):
		// 文案已包含完整指引(移除或改名),不再拼接英文 err 详情避免中英重复。
		return localizedMsg(m.SkillCmdErrExists, "✗ name conflict; remove the installed one first or use --name")
	default:
		return humanizeSkillErr(err)
	}
}

func humanizeSkillErr(err error) string {
	return "✗ " + err.Error()
}

func shortCommit(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func shortCommitOrDash(entry *skill.LockEntry, sha string) string {
	if entry == nil || sha == "" {
		return "-"
	}
	return shortCommit(sha)
}
