// Package skill 实现 Waveloom 的 Skill System —— 可复用提示词扩展系统。
//
// 加载 SKILL.md 格式（YAML frontmatter + Markdown body），
// 支持 .claude/skills/、.waveloom/skills/ 双路径发现，
// 以及 .claude/commands/、.waveloom/commands/ 扁平文件兼容。
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/permission"

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
	AllowedTools          []string `yaml:"allowed-tools"`
	Paths                 []string `yaml:"paths"` // 条件激活路径（gitignore-style glob）
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
	Guard     permission.Guard // 权限守门人（nil = skill 中 shell 命令免校验直接执行）
	// conditionalSkills 存储有条件 paths 的 skill（未被激活前不出现在 List 中）
	conditionalSkills map[string]*LoadedSkill
	// activatedConditionalNames 记录已激活的条件 skill 名称（跨 List 调用持久）
	activatedConditionalNames map[string]bool
}

// NewLoader 创建一个新的 Loader。
// guard 可选为 nil —— nil 时 skill 中的 !`cmd` 直接执行（不推荐）。
func NewLoader(cwd, homeDir, sessionID, effort string, guard permission.Guard) *Loader {
	return &Loader{
		CWD:                       cwd,
		HomeDir:                   homeDir,
		SessionID:                 sessionID,
		Effort:                    effort,
		Guard:                     guard,
		conditionalSkills:         make(map[string]*LoadedSkill),
		activatedConditionalNames: make(map[string]bool),
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
		skillName := entry.Name()

		// 检查是否为条件 skill（有 paths frontmatter）
		if l.isConditional(skillFile) {
			// 已激活 → 正常加载；未激活 → 存储并跳过
			if l.activatedConditionalNames[skillName] {
				info, ok := l.parseSkillInfo(skillFile, skillName, false)
				if ok && !seen[info.Name] {
					seen[info.Name] = true
					infos = append(infos, info)
				}
			} else {
				l.storeConditional(skillFile, skillName)
			}
			continue
		}

		info, ok := l.parseSkillInfo(skillFile, skillName, false)
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

		// 检查是否为条件 skill
		if l.isConditional(skillFile) {
			if l.activatedConditionalNames[name] {
				info, ok := l.parseSkillInfo(skillFile, name, true)
				if ok && !seen[info.Name] {
					seen[info.Name] = true
					infos = append(infos, info)
				}
			} else {
				l.storeConditional(skillFile, name)
			}
			continue
		}

		info, ok := l.parseSkillInfo(skillFile, name, true)
		if !ok {
			continue
		}
		seen[info.Name] = true
		infos = append(infos, info)
	}
	return infos
}

// isConditional 检查 SKILL.md 是否有 paths frontmatter（条件 skill）。
func (l *Loader) isConditional(filePath string) bool {
	fm, _, _, _, err := parseSKILLmd(filePath, false)
	if err != nil {
		return false
	}
	return len(fm.Paths) > 0
}

// storeConditional 将条件 skill 存入 conditionalSkills map（供后续文件匹配激活）。
// name 为 skill 目录名（或 commands 扁平文件名），用于 ActivateForPaths 匹配后的 Load。
func (l *Loader) storeConditional(filePath, name string) {
	// 已激活则不再存储
	if l.activatedConditionalNames[name] {
		return
	}
	// 暂存文件路径和名称，延迟到激活时再完整 Load
	l.conditionalSkills[name] = &LoadedSkill{
		Info: SkillInfo{
			Name: name,
		},
		// 用 DirPath 暂存文件路径（Load 时使用）
		DirPath: filePath,
	}
}

// ActivateForPaths 检查条件 skill 的 paths 是否匹配给定的文件路径列表，
// 匹配成功则激活该 skill（后续 List() 将包含它）。
// 返回新激活的 skill 名称列表。
func (l *Loader) ActivateForPaths(filePaths []string) []string {
	if len(l.conditionalSkills) == 0 || len(filePaths) == 0 {
		return nil
	}

	var activated []string
	for name, entry := range l.conditionalSkills {
		if entry == nil {
			continue
		}
		// 解析 paths frontmatter
		fm, _, _, _, err := parseSKILLmd(entry.DirPath, false)
		if err != nil || len(fm.Paths) == 0 {
			continue
		}
		if matchAnyPath(filePaths, fm.Paths) {
			l.activatedConditionalNames[name] = true
			delete(l.conditionalSkills, name)
			activated = append(activated, name)
		}
	}
	return activated
}

// matchAnyPath 检查 filePaths 中是否有匹配 glob patterns 中任意一个的路径。
func matchAnyPath(filePaths []string, patterns []string) bool {
	for _, fp := range filePaths {
		for _, pattern := range patterns {
			// 尝试直接 glob 匹配
			if matched, _ := filepath.Match(pattern, fp); matched {
				return true
			}
			// 尝试匹配路径的 base 部分
			if matched, _ := filepath.Match(pattern, filepath.Base(fp)); matched {
				return true
			}
			// 尝试对相对路径匹配（去掉前导路径段逐一尝试）
			parts := strings.Split(fp, string(filepath.Separator))
			for i := range parts {
				suffix := strings.Join(parts[i:], string(filepath.Separator))
				if matched, _ := filepath.Match(pattern, suffix); matched {
					return true
				}
			}
		}
	}
	return false
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

	var lastErr error
	for _, c := range uniqueCandidates {
		loaded, err := l.loadFromFile(c.path, name, args, c.isFlatFile)
		if err == nil {
			return loaded, nil
		}
		// 记录第一个非"文件不存在"级别的错误，用于透传诊断信息
		if lastErr == nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

func (l *Loader) loadFromFile(filePath, skillName, args string, isFlatFile bool) (*LoadedSkill, error) {
	fm, body, dirPath, supportingFiles, err := parseSKILLmd(filePath, isFlatFile)
	if err != nil {
		return nil, err
	}

	// 清除上一轮 skill 的 Guard 白名单，然后注册本轮的白名单
	l.clearGuardSkillWhitelist()
	patterns := permission.ParseAllowedBashPatterns(fm.AllowedTools)
	if len(patterns) > 0 {
		l.setGuardSkillWhitelist(patterns)
	}

	// 注入命令安全校验：解析阶段 lint，不通过则拒绝加载（fail fast）
	if err := l.lintInjections(body, patterns); err != nil {
		l.clearGuardSkillWhitelist()
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

	var substituted bool
	body, substituted = l.substituteVariables(body, args, argsList, namedArgs, dirPath)

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
	// 且 args 非空时自动追加。
	if args != "" && !substituted {
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
// 代码片段保护
// ---------------------------------------------------------------------------

// codeSpanPlaceholder 是用于代码片段保护的占位符前缀。
// 使用 \x00 确保不与正常 Markdown 文本冲突。
const codeSpanPlaceholder = "\x00WVCSPAN"

// escapedDollarPlaceholder 用于保护转义的 \$，防止变量替换时误匹配 $N。
const escapedDollarPlaceholder = "\x00WVCDOLLAR\x00"

// fencedCodeBlockRegex 匹配整段围栏代码块（``` 起止）。
var fencedCodeBlockRegex = regexp.MustCompile("(?s)```.*?```")

// inlineCodeSpanRegex 匹配行内代码片段（`...`，不含换行）。
var inlineCodeSpanRegex = regexp.MustCompile("`[^`\n]+`")

// indexArgRegex 匹配 $ARGUMENTS[N] 形式的索引参数占位符。
var indexArgRegex = regexp.MustCompile(`\$ARGUMENTS\[(\d+)\]`)

// indexShorthandRegex 匹配 $N 形式的索引参数占位符。
// Go RE2 不支持 lookahead，使用 \b 词边界替代：
// $1 xyz → 匹配（1 和空格之间有边界）
// $1abc  → 不匹配（1 和 a 之间无边界）
// $1     → 匹配（行尾边界）
var indexShorthandRegex = regexp.MustCompile(`\$(\d+)\b`)

// protectCodeSpans 将 body 中的围栏代码块和行内代码片段替换为占位符，
// 避免变量替换时误改代码示例中的 $ARGUMENTS 等文本。
// 返回受保护后的 body 和占位符 → 原文的映射。
func protectCodeSpans(body string) (string, map[string]string) {
	protected := make(map[string]string)
	counter := 0
	next := func() string {
		counter++
		return fmt.Sprintf("%s_%d\x00", codeSpanPlaceholder, counter)
	}

	// 先保护围栏代码块（长匹配优先）
	body = fencedCodeBlockRegex.ReplaceAllStringFunc(body, func(match string) string {
		ph := next()
		protected[ph] = match
		return ph
	})

	// 再保护行内代码片段
	body = inlineCodeSpanRegex.ReplaceAllStringFunc(body, func(match string) string {
		ph := next()
		protected[ph] = match
		return ph
	})

	return body, protected
}

// restoreCodeSpans 将占位符还原为原始代码片段。
func restoreCodeSpans(body string, protected map[string]string) string {
	for ph, original := range protected {
		body = strings.ReplaceAll(body, ph, original)
	}
	return body
}

// indexFromBrackets 从 $ARGUMENTS[N] 匹配中提取索引 N。
func indexFromBrackets(match string) int {
	// match 格式: $ARGUMENTS[N]
	parts := strings.TrimPrefix(match, "$ARGUMENTS[")
	parts = strings.TrimSuffix(parts, "]")
	n, err := strconv.Atoi(parts)
	if err != nil {
		return -1
	}
	return n
}

// indexFromShorthand 从 $N 匹配中提取索引 N。
func indexFromShorthand(match string) int {
	// match 格式: $N
	n, err := strconv.Atoi(match[1:])
	if err != nil {
		return -1
	}
	return n
}

// isWordChar 判断字符是否为单词字符（字母、数字、下划线）。
// 对标 \w 正则元字符行为。
func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// ---------------------------------------------------------------------------
// 变量替换
// ---------------------------------------------------------------------------

func (l *Loader) substituteVariables(body, args string, argsList, namedArgs []string, dirPath string) (string, bool) {
	// 替换顺序（对标 Claude Code argumentSubstitution.ts）：
	// 1. 命名参数 $name（先于索引参数，避免 $name 被 $N 误匹配）
	// 2. $ARGUMENTS[N]
	// 3. $N
	// 4. $ARGUMENTS
	// 5. ${CLAUDE_SESSION_ID} / ${WAVELOOM_SESSION_ID}
	// 6. ${CLAUDE_SKILL_DIR} / ${WAVELOOM_SKILL_DIR}
	// 7. ${CLAUDE_EFFORT} / ${WAVELOOM_EFFORT}
	// 8. \$ → $（恢复字面 $）
	//
	// 代码片段（围栏块和行内 code span）中的变量不替换。
	// \$ 转义序列中的 $ 不参与变量替换（先保护，后还原）。
	//
	// 关键行为（对标 Claude Code）：
	// - $ARGUMENTS[N] 和 $N 使用全局正则替换；越界索引 → 替换为空字符串
	// - 无参时（argsList 为空）所有 $N / $ARGUMENTS[N] / $ARGUMENTS → 空字符串
	// - 返回 substituted 指示是否有占位符被实际替换（用于 auto-append 判定）

	var substituted bool

	// 保护代码片段和转义 $
	body, protected := protectCodeSpans(body)

	// 保护 \$ → 占位符，防止变量替换误伤 $N
	body = strings.ReplaceAll(body, `\$`, escapedDollarPlaceholder)

	// 1. 命名参数
	for i, name := range namedArgs {
		val := ""
		if i < len(argsList) {
			val = argsList[i]
		}
		// $name 但不是 $name[...] 或 $nameXxx（word boundary）
		// Go RE2 不支持 lookahead，手动检查匹配后字符。
		re := regexp.MustCompile(`\$` + regexp.QuoteMeta(name))
		matches := re.FindAllStringIndex(body, -1)
		for m := len(matches) - 1; m >= 0; m-- {
			start, end := matches[m][0], matches[m][1]
			// 检查 $name 后是否紧跟 [ 或单词字符（对标 Claude Code）：
			// $name[0] → 不认为 $name 是占位符
			// $nameXxx → 不认为 $name 是占位符
			if end < len(body) {
				next := body[end]
				if next == '[' || isWordChar(next) {
					continue
				}
			}
			body = body[:start] + val + body[end:]
			substituted = true
		}
	}

	// 2. $ARGUMENTS[N] → 越界索引替换为空字符串
	if indexArgRegex.MatchString(body) {
		substituted = true
	}
	body = indexArgRegex.ReplaceAllStringFunc(body, func(match string) string {
		idx := indexFromBrackets(match)
		if idx >= 0 && idx < len(argsList) {
			return argsList[idx]
		}
		return ""
	})

	// 3. $N（shorthand，等价 $ARGUMENTS[N]）→ 越界索引替换为空字符串
	if indexShorthandRegex.MatchString(body) {
		substituted = true
	}
	body = indexShorthandRegex.ReplaceAllStringFunc(body, func(match string) string {
		idx := indexFromShorthand(match)
		if idx >= 0 && idx < len(argsList) {
			return argsList[idx]
		}
		return ""
	})

	// 4. $ARGUMENTS
	if strings.Contains(body, "$ARGUMENTS") {
		substituted = true
	}
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)

	// 5. Session ID
	sid := l.SessionID
	body = strings.ReplaceAll(body, "${CLAUDE_SESSION_ID}", sid)
	body = strings.ReplaceAll(body, "${WAVELOOM_SESSION_ID}", sid)

	// 6. Skill dir
	body = strings.ReplaceAll(body, "${CLAUDE_SKILL_DIR}", dirPath)
	body = strings.ReplaceAll(body, "${WAVELOOM_SKILL_DIR}", dirPath)

	// 7. Effort
	body = strings.ReplaceAll(body, "${CLAUDE_EFFORT}", l.Effort)
	body = strings.ReplaceAll(body, "${WAVELOOM_EFFORT}", l.Effort)

	// 8. 转义 \$：将占位符还原为字面 $
	body = strings.ReplaceAll(body, escapedDollarPlaceholder, "$")

	// 还原代码片段
	body = restoreCodeSpans(body, protected)

	return body, substituted
}

// ---------------------------------------------------------------------------
// 动态注入
// ---------------------------------------------------------------------------

// inlineCmdRegex 匹配行内动态注入 !`command`
// Go RE2 不支持 lookbehind，改用捕获前缀 (^|\s) 再在替换时拼接
var inlineCmdRegex = regexp.MustCompile(`(^|\s)!` + "`([^`]+)`")

// multilineInjectionRegex 匹配 ```! ... ```
var multilineInjectionRegex = regexp.MustCompile("(?s)```!\n(.*?)```")

// extractInjectionCommands 提取 body 中所有动态注入命令。
// 行内注入返回完整命令字符串；多行注入按行拆分，每行作为一个独立命令。
func extractInjectionCommands(body string) []string {
	var commands []string

	// 1. 多行注入：每行作为一个命令
	for _, match := range multilineInjectionRegex.FindAllStringSubmatch(body, -1) {
		if len(match) > 1 {
			for _, line := range strings.Split(match[1], "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					commands = append(commands, line)
				}
			}
		}
	}

	// 2. 行内注入：完整命令
	for _, line := range strings.Split(body, "\n") {
		if matches := inlineCmdRegex.FindStringSubmatch(line); matches != nil {
			cmd := strings.TrimSpace(matches[2])
			if cmd != "" {
				commands = append(commands, cmd)
			}
		}
	}

	return commands
}

// lintInjections 校验 body 中所有注入命令是否被 allowed-tools 白名单覆盖。
// Guard 为 nil 时跳过校验（开发/测试模式）；白名单为空时拒绝所有注入。
func (l *Loader) lintInjections(body string, patterns []string) error {
	if l.Guard == nil {
		return nil
	}

	commands := extractInjectionCommands(body)
	if len(commands) == 0 {
		return nil
	}

	if len(patterns) == 0 {
		return fmt.Errorf("skill contains dynamic injection but has no allowed-tools Bash whitelist; add e.g. allowed-tools: [\"Bash(echo *)\"]")
	}

	for _, cmd := range commands {
		matched := false
		for _, pattern := range patterns {
			if permission.MatchBashPattern(cmd, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("injection command %q is not covered by allowed-tools Bash whitelist; add Bash(<pattern>) covering this command to allowed-tools", cmd)
		}
	}

	return nil
}

// executeDynamicInjections 执行 body 中的 !`cmd` 和 ```! block。
func (l *Loader) executeDynamicInjections(body, dirPath string) string {
	// 1. 处理多行代码块 ```! ... ```
	body = l.replaceMultilineInjections(body, dirPath)

	// 2. 处理行内 !`command`
	// Go RE2 不支持 lookbehind，改用捕获前缀并在 ReplaceAllStringFunc 中拼接。
	// 使用 ReplaceAllStringFunc 而非整行替换，确保只替换匹配部分，保留行内其他文本。
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = inlineCmdRegex.ReplaceAllStringFunc(line, func(match string) string {
			submatches := inlineCmdRegex.FindStringSubmatch(match)
			if submatches == nil {
				return match
			}
			prefix := submatches[1] // "" (行首匹配) 或空白字符
			cmd := submatches[2]
			output := l.runCommand(cmd, dirPath)
			if output == "" {
				return match
			}
			return prefix + output
		})
	}

	return strings.Join(lines, "\n")
}

func (l *Loader) replaceMultilineInjections(body, dirPath string) string {
	return multilineInjectionRegex.ReplaceAllStringFunc(body, func(match string) string {
		// 提取 ```! 和 ``` 之间的内容
		inner := match[5 : len(match)-3] // 去掉 ```!\n 和 结尾 ```
		inner = strings.TrimSuffix(inner, "\n")
		return l.runCommand(inner, dirPath)
	})
}

// runCommand 执行 shell 命令，30s 超时。
// 若 l.Guard 非 nil，通过 Guard.Check 验证权限（skill 白名单在 shellSafetyCheck 中优先放行）。
// Guard 为 nil 时直接执行（测试 / 无权限场景）。
func (l *Loader) runCommand(command, dir string) string {
	if l.Guard != nil {
		shellInput, _ := json.Marshal(map[string]string{"command": command})
		result := l.Guard.Check(context.Background(), "bash", shellInput)
		if result.Decision != permission.DecisionAllow {
			reason := result.Message
			if reason == "" {
				reason = string(result.Decision)
			}
			return fmt.Sprintf("[skill command denied: %s]", reason)
		}
	}

	return l.execShell(command, dir)
}

// execShell 执行 shell 命令并返回输出。
// 优先使用 bash（兼容 skill 脚本中的 bash 专有特性如 pipefail/local），
// 不可用时回退到 sh。
func (l *Loader) execShell(command, dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	shellBin := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shellBin = "sh"
	}
	cmd := exec.CommandContext(ctx, shellBin, "-c", command)
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
// Guard skill whitelist helpers
// ---------------------------------------------------------------------------

// skillBashWhitelister 是 Guard 可选实现的接口，用于注册 skill 级 Bash 白名单。
type skillBashWhitelister interface {
	SetSkillBashWhitelist(patterns []string)
	ClearSkillBashWhitelist()
}

// setGuardSkillWhitelist 将 skill 白名单注册到 Guard（若 Guard 支持）。
// 注册后，shellSafetyCheck 会在高危拦截前优先放行白名单命令。
func (l *Loader) setGuardSkillWhitelist(patterns []string) {
	if sw, ok := l.Guard.(skillBashWhitelister); ok {
		sw.SetSkillBashWhitelist(patterns)
	}
}

// clearGuardSkillWhitelist 清空 Guard 中注册的 skill 白名单。
func (l *Loader) clearGuardSkillWhitelist() {
	if sw, ok := l.Guard.(skillBashWhitelister); ok {
		sw.ClearSkillBashWhitelist()
	}
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
		fmt.Fprintf(&b, "| /%s | %s |\n", info.Name, desc)
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
