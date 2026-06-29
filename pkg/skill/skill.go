// Package skill 实现 Waveloom 的 Skill System —— 可复用提示词扩展系统。
//
// 兼容 Claude Code SKILL.md 格式（YAML frontmatter + Markdown body），
// 支持 .claude/skills/、.waveloom/skills/ 双路径发现，
// 以及 .claude/commands/、.waveloom/commands/ 扁平文件兼容。
package skill

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// 类型定义
// ---------------------------------------------------------------------------

// SkillInfo 是 Skill 的轻量描述，发送给 LLM 和 SlashCommand Registry。
type SkillInfo struct {
	Name           string // 技能名（目录名或文件名）
	Description    string // 简短描述（frontmatter description + when_to_use）
	Args           string // 参数占位符提示（argument-hint）
	UserInvocable  bool   // 用户是否可通过 / 调用
	ModelInvocable bool   // LLM 是否可自动调用
	Source         string // 来源路径
}

// LoadedSkill 是渲染后的完整 Skill。
type LoadedSkill struct {
	Info    SkillInfo
	Body    string // 渲染后的 body（变量已替换，!`cmd` 已执行，附属文件清单已追加）
	DirPath string // SKILL.md 所在目录
}

// frontmatter 是 SKILL.md 的 YAML 头部解析结果。
type frontmatter struct {
	Name                  string
	Description           string
	WhenToUse             string `yaml:"when_to_use"`
	ArgumentHint          string `yaml:"argument-hint"`
	Arguments             any    `yaml:"arguments"` // string or []string
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	UserInvocable         *bool  `yaml:"user-invocable"` // pointer to detect unset
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

// Loader 负责发现、解析和渲染 SKILL.md 文件。
type Loader struct {
	CWD       string
	HomeDir   string
	SessionID string
	Effort    string
}

// NewLoader 创建一个新的 Loader。
func NewLoader(cwd, homeDir, sessionID, effort string) *Loader {
	return &Loader{
		CWD:       cwd,
		HomeDir:   homeDir,
		SessionID: sessionID,
		Effort:    effort,
	}
}

// ---------------------------------------------------------------------------
// List — 发现所有可用 skill
// ---------------------------------------------------------------------------

// List 扫描所有 skill 目录和 commands 文件，返回可用 skill 的摘要列表。
func (l *Loader) List() ([]SkillInfo, error) {
	projectRoot := findProjectRoot(l.CWD)

	// 收集所有 skill 来源，按优先级排序
	type source struct {
		dir      string
		priority int // 数字越小优先级越高
		isCmdDir bool // true = commands 目录（flat files）, false = skills 目录
	}

	var sources []source

	// 个人 skills/
	if l.HomeDir != "" {
		sources = append(sources,
			source{filepath.Join(l.HomeDir, ".claude", "skills"), 1, false},
			source{filepath.Join(l.HomeDir, ".waveloom", "skills"), 2, false},
			source{filepath.Join(l.HomeDir, ".claude", "commands"), 3, true},
			source{filepath.Join(l.HomeDir, ".waveloom", "commands"), 4, true},
		)
	}

	// 项目级
	if projectRoot != "" {
		sources = append(sources,
			source{filepath.Join(projectRoot, ".claude", "skills"), 5, false},
			source{filepath.Join(projectRoot, ".waveloom", "skills"), 6, false},
			source{filepath.Join(projectRoot, ".claude", "commands"), 7, true},
			source{filepath.Join(projectRoot, ".waveloom", "commands"), 8, true},
		)
	}

	seen := make(map[string]bool)
	var infos []SkillInfo

	for _, src := range sources {
		if src.isCmdDir {
			infos = append(infos, l.scanCommandsDir(src.dir, src.priority, seen)...)
		} else {
			infos = append(infos, l.scanSkillsDir(src.dir, src.priority, seen)...)
		}
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})

	return infos, nil
}

func (l *Loader) scanSkillsDir(dir string, priority int, seen map[string]bool) []SkillInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var infos []SkillInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
		info, ok := l.parseSkillInfo(skillFile, entry.Name(), false)
		if !ok {
			continue
		}
		if seen[info.Name] {
			continue
		}
		seen[info.Name] = true
		infos = append(infos, info)
	}
	return infos
}

func (l *Loader) scanCommandsDir(dir string, priority int, seen map[string]bool) []SkillInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var infos []SkillInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		if name == entry.Name() { // not .md file
			continue
		}
		// 若同名 skill（目录形式）已存在，跳过
		if seen[name] {
			continue
		}
		skillFile := filepath.Join(dir, entry.Name())
		info, ok := l.parseSkillInfo(skillFile, name, true)
		if !ok {
			continue
		}
		seen[info.Name] = true
		infos = append(infos, info)
	}
	return infos
}

// parseSkillInfo 解析 SKILL.md 或 command.md 的 frontmatter，返回 SkillInfo。
func (l *Loader) parseSkillInfo(filePath, defaultName string, isFlatFile bool) (SkillInfo, bool) {
	fm, _, _, _, err := parseSKILLmd(filePath, isFlatFile)
	if err != nil {
		return SkillInfo{}, false
	}

	name := defaultName
	if fm.Name != "" {
		name = fm.Name
	}

	desc := fm.Description
	if fm.WhenToUse != "" {
		if desc != "" {
			desc += " " + fm.WhenToUse
		} else {
			desc = fm.WhenToUse
		}
	}
	// 截断到 1536 字符
	if len(desc) > 1536 {
		desc = desc[:1536]
	}

	userInvocable := true
	if fm.UserInvocable != nil {
		userInvocable = *fm.UserInvocable
	}

	modelInvocable := !fm.DisableModelInvocation

	return SkillInfo{
		Name:           name,
		Description:    desc,
		Args:           argsPlaceholder(fm.ArgumentHint, fm.Arguments),
		UserInvocable:  userInvocable,
		ModelInvocable: modelInvocable,
		Source:         filePath,
	}, true
}

// ---------------------------------------------------------------------------
// Load — 完整渲染
// ---------------------------------------------------------------------------

// Load 加载并渲染指定名称的 skill。
func (l *Loader) Load(name string, args string) (*LoadedSkill, error) {
	projectRoot := findProjectRoot(l.CWD)

	type candidate struct {
		path       string
		isFlatFile bool
	}

	var candidates []candidate

	// 按优先级顺序查找
	addDir := func(base, sub string) {
		// 目录形式
		skillFile := filepath.Join(base, sub, "SKILL.md")
		if fileExists(skillFile) {
			candidates = append(candidates, candidate{skillFile, false})
		}
	}

	addCmd := func(base string) {
		cmdFile := filepath.Join(base, name+".md")
		if fileExists(cmdFile) {
			candidates = append(candidates, candidate{cmdFile, true})
		}
	}

	if l.HomeDir != "" {
		addDir(l.HomeDir, filepath.Join(".claude", "skills", name))
		addDir(l.HomeDir, filepath.Join(".waveloom", "skills", name))
		addCmd(filepath.Join(l.HomeDir, ".claude", "commands"))
		addCmd(filepath.Join(l.HomeDir, ".waveloom", "commands"))
	}
	if projectRoot != "" {
		addDir(projectRoot, filepath.Join(".claude", "skills", name))
		addDir(projectRoot, filepath.Join(".waveloom", "skills", name))
		addCmd(filepath.Join(projectRoot, ".claude", "commands"))
		addCmd(filepath.Join(projectRoot, ".waveloom", "commands"))
	}

	// 去重：同路径不重复（skill 目录形式优先于 command 扁平文件）
	seenPaths := make(map[string]bool)
	var uniqueCandidates []candidate
	for _, c := range candidates {
		if !seenPaths[c.path] {
			seenPaths[c.path] = true
			uniqueCandidates = append(uniqueCandidates, c)
		}
	}

	for _, c := range uniqueCandidates {
		loaded, err := l.loadFromFile(c.path, name, args, c.isFlatFile)
		if err == nil {
			return loaded, nil
		}
	}

	return nil, fmt.Errorf("skill not found: %s", name)
}

func (l *Loader) loadFromFile(filePath, skillName, args string, isFlatFile bool) (*LoadedSkill, error) {
	fm, body, dirPath, supportingFiles, err := parseSKILLmd(filePath, isFlatFile)
	if err != nil {
		return nil, err
	}

	// 规范名称
	name := skillName
	if fm.Name != "" {
		name = fm.Name
	}

	// 变量替换
	argsList := shellSplit(args)
	namedArgs := parseArgumentsList(fm.Arguments)

	body = l.substituteVariables(body, args, argsList, namedArgs, dirPath)

	// 动态注入
	body = l.executeDynamicInjections(body, dirPath)

	// 追加附属文件清单
	if !isFlatFile && len(supportingFiles) > 0 {
		body += "\n\n## Supporting files\n\n"
		body += "The following files are available in the skill directory. "
		body += "Use read_file to load their content when needed:\n\n"
		for _, f := range supportingFiles {
			body += "- " + f + "\n"
		}
	}

	// $ARGUMENTS 无痛追加
	if args != "" && !strings.Contains(body, args) && !strings.Contains(body, "$ARGUMENTS") {
		body += "\n\nARGUMENTS: " + args
	}

	desc := fm.Description
	if fm.WhenToUse != "" {
		if desc != "" {
			desc += " " + fm.WhenToUse
		} else {
			desc = fm.WhenToUse
		}
	}
	if len(desc) > 1536 {
		desc = desc[:1536]
	}

	userInvocable := true
	if fm.UserInvocable != nil {
		userInvocable = *fm.UserInvocable
	}

	return &LoadedSkill{
		Info: SkillInfo{
			Name:           name,
			Description:    desc,
			Args:           argsPlaceholder(fm.ArgumentHint, fm.Arguments),
			UserInvocable:  userInvocable,
			ModelInvocable: !fm.DisableModelInvocation,
			Source:         filePath,
		},
		Body:    body,
		DirPath: dirPath,
	}, nil
}

// ---------------------------------------------------------------------------
// SKILL.md 解析
// ---------------------------------------------------------------------------

// parseSKILLmd 解析 SKILL.md 文件，返回 frontmatter、body、目录路径、附属文件列表。
func parseSKILLmd(filePath string, isFlatFile bool) (fm frontmatter, body string, dirPath string, supportingFiles []string, err error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fm, "", "", nil, err
	}

	dirPath = filepath.Dir(filePath)
	text := string(content)

	// 解析 YAML frontmatter
	if strings.HasPrefix(text, "---\n") {
		parts := strings.SplitN(text[4:], "\n---\n", 2)
		if len(parts) == 2 {
			fmRaw := parts[0]
			body = parts[1]

			if uerr := yaml.Unmarshal([]byte(fmRaw), &fm); uerr != nil {
				// 畸形 YAML：整个文件作为 body
				fm = frontmatter{}
				body = text
			}
		} else if strings.HasPrefix(text[4:], "---\n") {
			// 空 frontmatter: "---\n---\n..."
			body = text[4:][strings.Index(text[4:], "\n")+1:]
		} else {
			body = text
		}
	} else {
		body = text
	}

	// 扫描附属文件
	if !isFlatFile {
		supportingFiles = scanSupportingFiles(dirPath)
	}

	return fm, strings.TrimSpace(body), dirPath, supportingFiles, nil
}

// scanSupportingFiles 扫描 dirPath 下所有非 SKILL.md 的常规文件。
func scanSupportingFiles(dirPath string) []string {
	var files []string
	_ = filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != dirPath {
			// 进入子目录
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dirPath, path)
		if err != nil {
			return nil
		}
		if rel == "SKILL.md" {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files
}

// ---------------------------------------------------------------------------
// 变量替换
// ---------------------------------------------------------------------------

func (l *Loader) substituteVariables(body, args string, argsList, namedArgs []string, dirPath string) string {
	// 替换顺序（对标 Claude Code）：
	// 1. $ARGUMENTS[N] 和 $N（先替换索引形式，避免被 $ARGUMENTS 整体替换误伤）
	// 2. 命名参数 $name
	// 3. $ARGUMENTS
	// 4. ${CLAUDE_SESSION_ID} / ${WAVELOOM_SESSION_ID}
	// 5. ${CLAUDE_SKILL_DIR} / ${WAVELOOM_SKILL_DIR}
	// 6. ${CLAUDE_EFFORT} / ${WAVELOOM_EFFORT}
	// 7. \$ → $（恢复字面 $）

	// 1. 索引参数
	for i, val := range argsList {
		// $N
		placeholder := fmt.Sprintf("$%d", i)
		body = strings.ReplaceAll(body, placeholder, val)
		// $ARGUMENTS[N]
		indexed := fmt.Sprintf("$ARGUMENTS[%d]", i)
		body = strings.ReplaceAll(body, indexed, val)
	}

	// 2. 命名参数（对标 Claude Code：未绑定 → 空字符串）
	for i, name := range namedArgs {
		val := ""
		if i < len(argsList) {
			val = argsList[i]
		}
		body = strings.ReplaceAll(body, "$"+name, val)
	}

	// 3. $ARGUMENTS
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)

	// 4. Session ID
	sid := l.SessionID
	body = strings.ReplaceAll(body, "${CLAUDE_SESSION_ID}", sid)
	body = strings.ReplaceAll(body, "${WAVELOOM_SESSION_ID}", sid)

	// 5. Skill dir
	body = strings.ReplaceAll(body, "${CLAUDE_SKILL_DIR}", dirPath)
	body = strings.ReplaceAll(body, "${WAVELOOM_SKILL_DIR}", dirPath)

	// 6. Effort
	body = strings.ReplaceAll(body, "${CLAUDE_EFFORT}", l.Effort)
	body = strings.ReplaceAll(body, "${WAVELOOM_EFFORT}", l.Effort)

	// 7. 转义 \$
	body = strings.ReplaceAll(body, `\$`, "$")

	return body
}

// ---------------------------------------------------------------------------
// 动态注入
// ---------------------------------------------------------------------------

// inlineCmdRegex 匹配行内动态注入 !`command`
var inlineCmdRegex = regexp.MustCompile(`^(\s*)!` + "`([^`]+)`")

// executeDynamicInjections 执行 body 中的 !`cmd` 和 ```! block。
func (l *Loader) executeDynamicInjections(body, dirPath string) string {
	// 1. 处理多行代码块 ```! ... ```
	body = replaceMultilineInjections(body, dirPath)

	// 2. 处理行内 !`command`
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if matches := inlineCmdRegex.FindStringSubmatch(line); matches != nil {
			indent := matches[1]
			cmd := matches[2]
			output := runCommand(cmd, dirPath)
			// 替换整行
			if output != "" {
				lines[i] = indent + output
			}
		}
	}

	return strings.Join(lines, "\n")
}

// multilineInjectionRegex 匹配 ```! ... ```
var multilineInjectionRegex = regexp.MustCompile("(?s)```!\n(.*?)```")

func replaceMultilineInjections(body, dirPath string) string {
	return multilineInjectionRegex.ReplaceAllStringFunc(body, func(match string) string {
		// 提取 ```! 和 ``` 之间的内容
		inner := match[5 : len(match)-3] // 去掉 ```!\n 和 结尾 ```
		inner = strings.TrimSuffix(inner, "\n")
		return runCommand(inner, dirPath)
	})
}

// runCommand 执行 shell 命令，30s 超时。
func runCommand(command, dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("[command timed out after 30s: %s]", command)
		}
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		result := string(output)
		if result == "" {
			result = err.Error()
		}
		return fmt.Sprintf("%s\n[command exited with code %d]", result, exitCode)
	}

	return strings.TrimSpace(string(output))
}

// ---------------------------------------------------------------------------
// FormatSkillListing
// ---------------------------------------------------------------------------

// FormatSkillListing 格式化所有 skill 的描述列表，注入到 system prompt。
func (l *Loader) FormatSkillListing() string {
	infos, err := l.List()
	if err != nil || len(infos) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n## Available Skills\n\n")
	b.WriteString("| Skill | Description |\n")
	b.WriteString("|-------|-------------|\n")

	for _, info := range infos {
		if !info.ModelInvocable {
			continue // disable-model-invocation 的 skill 不注入 system prompt
		}
		desc := info.Description
		if desc == "" {
			desc = "-"
		}
		// 替换表格中的 |
		desc = strings.ReplaceAll(desc, "|", "\\|")
		b.WriteString(fmt.Sprintf("| /%s | %s |\n", info.Name, desc))
	}

	b.WriteString("\nTo invoke a skill, either type /skill-name [arguments] to invoke directly,\n")
	b.WriteString("or call the skill tool with the skill name and arguments.\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Shell 风格参数解析
// ---------------------------------------------------------------------------

// shellSplit 按 shell 引号规则解析参数。
func shellSplit(args string) []string {
	if args == "" {
		return nil
	}

	var result []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(args); i++ {
		ch := args[i]
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

// argsPlaceholder 返回用于 command picker 显示的参数占位符。
// 若 argumentHint 非空则直接返回，否则从 arguments 列表生成 fallback。
// 注意：picker 会在外部包裹 []，因此此处返回原始值不带方括号。
func argsPlaceholder(argumentHint string, arguments any) string {
	if argumentHint != "" {
		return argumentHint
	}
	argsList := parseArgumentsList(arguments)
	if len(argsList) == 0 {
		return ""
	}
	return strings.Join(argsList, " ")
}

// parseArgumentsList 解析 frontmatter arguments 字段。
func parseArgumentsList(val any) []string {
	switch v := val.(type) {
	case string:
		return shellSplit(v)
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

// findProjectRoot 从 cwd 向上查找包含 .git 的目录。
func findProjectRoot(cwd string) string {
	dir, _ := filepath.Abs(cwd)
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Reload 重新扫描 skill 目录。
func (l *Loader) Reload() ([]SkillInfo, error) {
	return l.List()
}
