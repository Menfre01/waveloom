package main

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// pickerScanDoneMsg 文件扫描完成消息（异步）。
type pickerScanDoneMsg struct {
	items []pickerItem
	gen   int // 扫描代数，用于丢弃过期结果
}

// pickerItem 表示文件选择器中的一个候选项。
type pickerItem struct {
	Path    string // 相对于 cwd 的路径
	IsDir   bool   // 是否为目录
	Display string // 渲染用的显示文本（目录带 / 后缀）
}

// fileItem 是文件选择器列表项，实现 list.DefaultItem 接口。
type fileItem struct {
	path    string
	display string
	isDir   bool
}

func (i fileItem) Title() string       { return i.display }
func (i fileItem) Description() string { return "" }
func (i fileItem) FilterValue() string { return i.display }

// handlePickerKey 处理文件选择器活跃时的按键。返回 (handled, cmd)。
// ↑/↓ 由 bubbles/list 组件处理；Enter/Tab/Esc 在此拦截。
func (m *model) handlePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()

	switch keyStr {
	case "up", "down":
		// ↑↓ 导航由 bubbles/list 组件处理
		var cmd tea.Cmd
		m.pickerList, cmd = m.pickerList.Update(msg)
		return true, cmd

	case "esc":
		m.closePicker()
		return true, nil

	case "enter":
		idx := m.pickerList.Index()
		if idx >= 0 && idx < len(m.pickerItems) {
			m.commitPickerSelection(idx)
		}
		m.closePicker()
		return true, nil

	case "tab":
		idx := m.pickerList.Index()
		if idx >= 0 && idx < len(m.pickerItems) {
			m.completePickerFilter(idx)
			// Tab 补全可能进入子目录 → 异步重新扫描磁盘
			m.pickerFilter = resolveTilde(extractFilterAfterAt(m.input.Value()))
			m.pickerLastScannedBase = "" // 强制重新扫描
			m.scanFilesAsync()
			m.pickerItems = fuzzyFilter(m.pickerFilter, m.pickerAllItems)
			m.buildPickerList()
			m.pickerLastValue = m.input.Value()
		}
		return true, nil

	default:
		// 可打印字符 → 传给 input，Update() 中会触发 re-filter
		return false, nil
	}
}

// closePicker 关闭文件选择器。
func (m *model) closePicker() {
	m.pickerVisible = false
	m.pickerScanning = false
	m.pickerDismissValue = m.input.Value()
	m.pickerLastValue = ""
	m.pickerLastScannedBase = ""
	m.pickerAllItems = nil
}

// closePickable 关闭文件选择器和命令选择器（overlay 激活前调用，避免弹层叠加）。
func (m *model) closePickable() {
	m.closePicker()
	m.closeCommandPicker()
}

// overlayAnimTick 返回下一个 overlay 动画帧的 tick 命令（~50ms）。
func (m *model) overlayAnimTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return overlayAnimTickMsg{}
	})
}

// activateOverlay 激活覆盖层，关闭 picker 并启动淡入动画。
func (m *model) activateOverlay(o Overlay) tea.Cmd {
	m.closePickable()
	m.overlay = o
	m.overlayAnimFrame = 0
	return m.overlayAnimTick()
}

// completePickerFilter 将选中路径补全到 @ 过滤器，保持选择器打开。
// 用户可继续输入以进一步缩小范围（fzf 风格 Tab 补全）。
func (m *model) completePickerFilter(idx int) {
	if idx < 0 || idx >= len(m.pickerItems) {
		return
	}
	selected := m.pickerItems[idx].Path
	value := m.input.Value()
	atIdx := strings.LastIndex(value, "@")
	if atIdx < 0 {
		return
	}
	// 替换 @ 及其后的内容为 @{selectedPath}
	// 目录自动追加 /，方便继续向下过滤（如 @cmd/waveloom/ 后可继续输入 m 匹配 main.go）
	newValue := value[:atIdx] + "@" + selected
	if m.pickerItems[idx].IsDir && !strings.HasSuffix(selected, "/") {
		newValue += "/"
	}
	m.input.SetValue(newValue)
	m.input.CursorEnd()
}

// commitPickerSelection 将选中路径回填到 textinput，关闭选择器。
func (m *model) commitPickerSelection(idx int) {
	if idx < 0 || idx >= len(m.pickerItems) {
		return
	}
	selected := m.pickerItems[idx].Path
	value := m.input.Value()
	atIdx := strings.LastIndex(value, "@")
	if atIdx < 0 {
		return
	}
	// 替换 @ 及其后的内容为 @{selectedPath} （追加空格，与 / 命令选择器行为一致）
	newValue := value[:atIdx] + "@" + selected + " "
	m.input.SetValue(newValue)
	// 光标移到末尾
	m.input.CursorEnd()
}

// renderPickerDropdown 渲染文件选择器下拉列表。
func (m *model) renderPickerDropdown(contentWidth int) string {
	// REGRESSION: 空 items 时返回 "" → 下拉面板完全不可见，用户在大目录中输入 @ 后
	// 看到的是"没反应"，实际上扫描异步进行中但无任何视觉反馈。
	// 修复：扫描中显示 spinner，无结果显示空状态，均不返回空字符串。
	// 不可单测：依赖 TUI 模型状态 + lipgloss 样式 + spinner 组件。
	// 扫描中 → 显示加载状态
	if m.pickerScanning && len(m.pickerItems) == 0 {
		spinner := m.spinner.View()
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorHeaderAccent).
			Padding(1, 1).
			Width(contentWidth)
		return boxStyle.Render(spinner + " " + m.msg().PickerScanning)
	}

	// 扫描完成但无结果 → 显示空状态
	if len(m.pickerItems) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(colorMuted)
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorHeaderAccent).
			Padding(1, 1).
			Width(contentWidth)
		return boxStyle.Render(emptyStyle.Render(m.msg().PickerNoResults))
	}

	// 同步 list 宽度
	m.pickerList.SetSize(contentWidth-4, m.pickerList.Height())

	// 带边框的下拉面板
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 1).
		Width(contentWidth)

	return boxStyle.Render(m.pickerList.View())
}

// shouldActivatePicker 检测输入框当前内容是否触发文件选择器。
// 条件: 最后一个 @ 在行首或空格之后，且 @ 之后无空格。
func shouldActivatePicker(value string) bool {
	idx := strings.LastIndex(value, "@")
	if idx < 0 {
		return false
	}
	// @ 前必须是行首或空格
	if idx > 0 && value[idx-1] != ' ' {
		return false
	}
	// @ 之后不能已经包含空格（路径已完成，避免重新触发）
	afterAt := value[idx+1:]
	return !strings.Contains(afterAt, " ")
}

// extractFilterAfterAt 提取最后一个 @ 之后的文本作为过滤条件。
func extractFilterAfterAt(value string) string {
	idx := strings.LastIndex(value, "@")
	if idx < 0 {
		return ""
	}
	return value[idx+1:]
}

// updatePickerFilter 根据当前输入重新过滤文件列表，并异步重扫磁盘。
func (m *model) updatePickerFilter() {
	m.pickerFilter = resolveTilde(extractFilterAfterAt(m.input.Value()))
	// 立即用现有 allItems 做内存过滤，提供即时反馈
	m.pickerItems = fuzzyFilter(m.pickerFilter, m.pickerAllItems)
	m.buildPickerList()
	// 异步重扫磁盘，确保 pickerAllItems 覆盖新 filter
	m.scanFilesAsync()
}

// scanFilesAsync 发起异步磁盘扫描，与 startPickerScan 行为一致。
// 若 filepath.Base(filter) 未变化则跳过（现有 allItems 已覆盖），
// 否则等待 150ms 防抖后再执行扫描。
func (m *model) scanFilesAsync() {
	filter := m.pickerFilter
	base := filepath.Base(filter)

	// base 未变化 → 现有 allItems 已覆盖更具体的同 base 过滤，无需重扫
	if base == m.pickerLastScannedBase && len(m.pickerAllItems) > 0 {
		return
	}
	m.pickerLastScannedBase = base

	// 取消上一次未完成的扫描
	if m.pickerScanCancel != nil {
		m.pickerScanCancel()
		m.pickerScanCancel = nil
	}

	m.pickerScanning = true
	m.pickerScanGen++
	gen := m.pickerScanGen
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.pickerScanCancel = cancel

	go func() {
		defer cancel()
		// 150ms 防抖：等待用户停止输入后再扫描
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		// 若在此期间有新扫描发起，代数已递增，跳过本次
		if m.pickerScanGen != gen {
			return
		}
		items := doScanFilesWithContext(ctx, m.registry, m.cwd, filter)
		if m.program != nil {
			m.program.Send(pickerScanDoneMsg{items: items, gen: gen})
		}
	}()
}

// activatePicker 首次激活文件选择器，异步扫描磁盘。
func (m *model) activatePicker() {
	m.pickerVisible = true
	m.pickerFilter = resolveTilde(extractFilterAfterAt(m.input.Value()))
	m.pickerLastValue = m.input.Value()
	m.pickerScanning = true

	// 立即用空列表占位，避免 View() 中 nil list
	m.pickerItems = nil
	m.buildPickerList()
}

// startPickerScan 返回一个 tea.Cmd，在 goroutine 中扫描文件并回传结果。
// 在调用时捕获 filter 和 generation，避免竞态。
func (m *model) startPickerScan() tea.Cmd {
	filter := m.pickerFilter
	m.pickerLastScannedBase = filepath.Base(filter)

	// 取消上一次未完成的扫描
	if m.pickerScanCancel != nil {
		m.pickerScanCancel()
		m.pickerScanCancel = nil
	}

	m.pickerScanGen++
	gen := m.pickerScanGen
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.pickerScanCancel = cancel

	return func() tea.Msg {
		defer cancel()
		items := doScanFilesWithContext(ctx, m.registry, m.cwd, filter)
		return pickerScanDoneMsg{items: items, gen: gen}
	}
}

// buildPickerList 从 pickerItems 更新 bubbles/list 组件。
// 首次调用时创建新 list，后续调用复用已有 list 仅更新 items。
func (m *model) buildPickerList() {
	items := make([]list.Item, len(m.pickerItems))
	for i, item := range m.pickerItems {
		items[i] = fileItem{
			path:    item.Path,
			display: item.Display,
			isDir:   item.IsDir,
		}
	}

	maxHeight := len(items)
	if maxHeight > 5 {
		maxHeight = 5
	}
	if maxHeight < 1 {
		maxHeight = 1
	}

	// 复用已有 list，仅更新 items + height
	if m.pickerList.Items() != nil {
		m.pickerList.SetItems(items)
		m.pickerList.SetSize(0, maxHeight)
		return
	}

	// 首次创建
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()

	l := list.New(items, delegate, 0, maxHeight)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()

	m.pickerList = l
	m.pickerDelegate = &delegate
}

// doScanFilesWithContext 传递 context 用于超时/取消。
func doScanFilesWithContext(ctx context.Context, registry tool.Registry, cwd, filter string) []pickerItem {
	filter = resolveTilde(filter)

	if filepath.IsAbs(filter) {
		return doScanAbsolute(ctx, registry, cwd, filter)
	}
	return doScanRelative(ctx, registry, cwd, filter)
}

// doScanRelative 相对路径扫描：基于 cwd，深度扫描项目内部，浅层列出父目录兄弟。
func doScanRelative(ctx context.Context, registry tool.Registry, cwd, filter string) []pickerItem {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	searchRoot := "."
	searchDir := cwd
	dirPrefix := extractDirPrefix(filter)
	if dirPrefix != "" && dirPrefix != "." {
		resolved := dirPrefix
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(cwd, resolved)
		}
		resolved = filepath.Clean(resolved)
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			searchRoot = resolved
			searchDir = resolved
		}
	}

	// 父目录顶层 → 浅层列出兄弟；其他 → 浅层扫描（5 层），
	// 用户输入更多路径分量后 searchRoot 自然收窄，无需深层扫描。
	maxDepth := 5
	excludeCWD := false
	if searchRoot == filepath.Dir(cwd) {
		maxDepth = 2
		excludeCWD = true
	}

	doSearch := func(namePattern string) (files []string) {
		// 使用 filepath.WalkDir 替代 find 命令：
		// - 跨平台兼容（Windows 无 find）
		// - 无外部依赖、无 shell 转义、无 MaxShellLines 截断
		// - WalkDirFunc 内直接 prune / 深度控制 / 名称过滤

		// 根目录绝对化，用于深度计算
		absRoot, err := filepath.Abs(filepath.Join(searchDir, searchRoot))
		if err != nil {
			absRoot = filepath.Join(searchDir, searchRoot)
		}
		absRoot = filepath.Clean(absRoot)
		// 计算根目录的路径分量数，用于深度判断
		rootDepth := len(strings.Split(absRoot, string(filepath.Separator)))

		var found []string
		walkCtx, walkCancel := context.WithTimeout(ctx, 8*time.Second)
		defer walkCancel()

		walkFn := func(path string, d os.DirEntry, walkErr error) error {
			select {
			case <-walkCtx.Done():
				return walkCtx.Err()
			default:
			}

			if walkErr != nil {
				return nil // 跳过无法访问的目录
			}

			// 深度控制
			currentDepth := len(strings.Split(path, string(filepath.Separator))) - rootDepth
			if currentDepth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// prune: 跳过 .git / node_modules 整树
			base := d.Name()
			if d.IsDir() && (base == ".git" || base == "node_modules") {
				return filepath.SkipDir
			}

			// 隐藏目录/文件跳过（非 . 或 ..）
			if strings.HasPrefix(base, ".") && base != "." && base != ".." {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// excludeCWD: 跳过 CWD 下的所有条目
			if excludeCWD {
				cwdAbs, _ := filepath.Abs(cwd)
				if strings.HasPrefix(path+string(filepath.Separator), cwdAbs+string(filepath.Separator)) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}

			if !d.IsDir() {
				// 名称过滤（只对文件，目录由目录推断生成）
				if namePattern != "" && namePattern != "*" {
					matched, _ := filepath.Match(namePattern, base)
					if !matched {
						return nil
					}
				}
				// 转为相对路径（相对于 CWD，确保 ../ 等前缀在 relativizePaths 中正确处理）
				rel, err := filepath.Rel(cwd, path)
				if err != nil {
					rel = path
				}
				found = append(found, rel)
			}

			return nil
		}

		_ = filepath.WalkDir(searchDir, walkFn)
		// WalkDir 可能因 context 超时返回 error，found 中已有部分结果可用
		sort.Strings(found)
		return relativizePaths(found, cwd)
	}

	files := doSearch("*")

	seenDirs := make(map[string]bool)
	var items []pickerItem

	for _, file := range files {
		if isHiddenOrBinary(file) {
			continue
		}
		items = append(items, pickerItem{
			Path:    file,
			IsDir:   false,
			Display: file,
		})

		dir := filepath.Dir(file)
		for dir != "" && filepath.Dir(dir) != dir {
			if seenDirs[dir] {
				break
			}
			if isHiddenOrBinary(dir) {
				break
			}
			seenDirs[dir] = true
			items = append(items, pickerItem{
				Path:    dir,
				IsDir:   true,
				Display: dir + "/",
			})
			dir = filepath.Dir(dir)
		}
	}

	// 父目录扫描时 CWD 内文件被 excludeCWD 排除，
	// 但 CWD 目录本身应作为候选项，支持 @../wav 模糊匹配 ../waveloom/
	// 插入到 items 开头，避免被 500 条截断丢弃
	if excludeCWD {
		cwdRel, _ := filepath.Rel(searchRoot, cwd)
		if cwdRel != "." && cwdRel != "" {
			cwdDisplay := filepath.Join("..", cwdRel) + "/"
			if !isHiddenOrBinary(cwdDisplay) {
				items = append([]pickerItem{{
					Path:    filepath.Join("..", cwdRel),
					IsDir:   true,
					Display: cwdDisplay,
				}}, items...)
			}
		}
	}

	// 当 dirPrefix 经由 .. 解析回 CWD 时（如 ../waveloom/ → CWD），
	// 显示路径需加上 dirPrefix 前缀，与 filter 保持一致，
	// 否则 filter("../waveloom/A") 无法匹配 display("AGENTS.md")。
	if searchRoot == cwd && dirPrefix != "" && dirPrefix != "." {
		prefix := dirPrefix
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		for i := range items {
			items[i].Path = prefix + items[i].Path
			items[i].Display = prefix + items[i].Display
		}
	}

	if len(items) == 0 {
		return nil
	}

	// REGRESSION: 500 条截断在 sortPickerItems 之前。
	// 4205 个目录中 waveloom/ 排在 ~4000 位，被截断提前丢弃，
	// fuzzyFilter 从未收到该条目 → 永远搜不到字母序靠后的目录。
	// 修复：去掉 500 截断，靠 fuzzyFilter（上限 20 条）自然限流。
	// 不可单测：依赖真实目录结构，条目数随 CWD 变化。
	sortPickerItems(items)
	return items
}

// doScanAbsolute 绝对路径扫描：展示绝对路径，深度随导航层级递增。
func doScanAbsolute(ctx context.Context, registry tool.Registry, cwd, filter string) []pickerItem {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// 提取目录前缀作为搜索起点
	dirPrefix := extractDirPrefix(filter)
	if dirPrefix == "" || dirPrefix == "." {
		return nil
	}
	resolved := dirPrefix
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil
	}
	searchRoot := resolved

	relFilter := strings.TrimPrefix(filter, searchRoot)
	relFilter = strings.Trim(relFilter, "/")

	// REGRESSION: 深度策略对模糊匹配（如 @/Use → searchRoot=/）使用 extraLevels+2，
	// 根目录扫描 6+ 层极易超时，导致间歇性搜索不到。
	// 修复：完整目录（relFilter=""）深度 5，模糊匹配（relFilter 非空）深度 1。
	// 不可单测：依赖真实文件系统，扫描耗时随系统负载变化。
	maxDepth := 5
	if relFilter != "" {
		maxDepth = 1
	}
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 12 {
		maxDepth = 12
	}

	// 搜索策略：
	// - 有部分名称（如 Workben）→ -name 'Workben*' 精搜，防截断
	// - 仅目录前缀（如 ~/）→ 全量扫描，供浏览
	namePattern := "*"
	if relFilter != "" {
		baseName := filepath.Base(relFilter)
		if baseName != "" && filepath.Dir(baseName) != baseName {
			namePattern = baseName + "*"
		}
	}

	doSearch := func(typeFilter string) (files []string) {
		// 使用 filepath.WalkDir 替代 find：跨平台兼容，无外部依赖。

		absRoot := filepath.Clean(searchRoot)
		rootDepth := len(strings.Split(absRoot, string(filepath.Separator)))

		var results []string
		walkCtx, walkCancel := context.WithTimeout(ctx, 8*time.Second)
		defer walkCancel()

		walkFn := func(path string, d os.DirEntry, walkErr error) error {
			select {
			case <-walkCtx.Done():
				return walkCtx.Err()
			default:
			}

			if walkErr != nil {
				return nil
			}

			currentDepth := len(strings.Split(path, string(filepath.Separator))) - rootDepth
			if currentDepth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			base := d.Name()
			// prune .git / node_modules
			if d.IsDir() && (base == ".git" || base == "node_modules") {
				return filepath.SkipDir
			}

			// 隐藏目录跳过
			if strings.HasPrefix(base, ".") && base != "." && base != ".." {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// 类型过滤
			if typeFilter == "d" && !d.IsDir() {
				return nil
			}
			if typeFilter == "f" && d.IsDir() {
				return nil
			}

			// 名称过滤
			if namePattern != "*" {
				matched, _ := filepath.Match(namePattern, base)
				if !matched {
					return nil
				}
			}

			results = append(results, path)
			return nil
		}

		_ = filepath.WalkDir(absRoot, walkFn)
		sort.Strings(results)
		return results
	}

	dirFiles := doSearch("d")
	regFiles := doSearch("f")

	if len(dirFiles) == 0 && len(regFiles) == 0 {
		return nil
	}

	seenDirs := make(map[string]bool)
	var items []pickerItem

	// 目录条目（find -type d 已确认类型，无需 os.Stat）
	for _, entry := range dirFiles {
		if seenDirs[entry] {
			continue
		}
		seenDirs[entry] = true
		items = append(items, pickerItem{
			Path:    entry,
			IsDir:   true,
			Display: entry + "/",
		})

		// 提取父目录链
		dir := filepath.Dir(entry)
		for dir != searchRoot && filepath.Dir(dir) != dir && dir != "" && strings.HasPrefix(dir, searchRoot) {
			if seenDirs[dir] {
				break
			}
			seenDirs[dir] = true
			items = append(items, pickerItem{
				Path:    dir,
				IsDir:   true,
				Display: dir + "/",
			})
			dir = filepath.Dir(dir)
		}
	}

	// 文件条目（find -type f 已确认类型）
	for _, entry := range regFiles {
		if isHiddenOrBinary(entry) {
			continue
		}
		items = append(items, pickerItem{
			Path:    entry,
			IsDir:   false,
			Display: entry,
		})

		// 提取父目录链
		dir := filepath.Dir(entry)
		for dir != searchRoot && filepath.Dir(dir) != dir && dir != "" && strings.HasPrefix(dir, searchRoot) {
			if seenDirs[dir] {
				break
			}
			if isHiddenOrBinary(dir) {
				break
			}
			seenDirs[dir] = true
			items = append(items, pickerItem{
				Path:    dir,
				IsDir:   true,
				Display: dir + "/",
			})
			dir = filepath.Dir(dir)
		}
	}

	sortPickerItems(items)
	return items
}

// extractDirPrefix 从 filter 中提取可能的外部目录前缀。
// 若 filter 为绝对路径、~ 或 . 开头，返回其目录部分作为搜索起点。
// 绝对路径和 ~ 直接使用；相对路径（./、../）基于 cwd 解析。
func extractDirPrefix(filter string) string {
	if filter == "" {
		return ""
	}
	if filter[0] == '~' || filter[0] == '.' || filepath.IsAbs(filter) {
		normalized := filepath.ToSlash(filter)
		if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
			return normalized[:idx+1]
		}
		return filter
	}
	return ""
}

// resolveTilde 展开 filter 中的 ~ 和 ~user 前缀为实际 home 目录路径。
// ~ → 当前用户 home，~user → 指定用户 home。
func resolveTilde(filter string) string {
	if !strings.HasPrefix(filter, "~") {
		return filter
	}
	end := strings.Index(filter, "/")
	tildePart := filter
	suffix := ""
	if end >= 0 {
		tildePart = filter[:end]
		suffix = filter[end:]
	}

	var homeDir string
	if tildePart == "~" {
		homeDir, _ = os.UserHomeDir()
	} else {
		username := tildePart[1:]
		if u, err := user.Lookup(username); err == nil {
			homeDir = u.HomeDir
		}
	}
	if homeDir == "" {
		return filter
	}
	return homeDir + suffix
}

// relativizePaths 将绝对路径或 ./ 前缀路径转换为相对于 cwd 的路径。
// find 在外部目录搜索时输出绝对路径，需要转回 cwd 相对路径以支持模糊过滤和 @ 引用。
func relativizePaths(paths []string, cwd string) []string {
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 转绝对路径
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		// 转 cwd 相对路径
		rel, err := filepath.Rel(cwd, abs)
		if err != nil {
			rel = abs
		}
		result = append(result, rel)
	}
	return result
}

// isHiddenOrBinary 检查路径是否应被过滤。
func isHiddenOrBinary(path string) bool {
	// 检查每个路径段是否以 . 开头（隐藏文件/目录），排除 . 和 ..（合法路径导航）
	parts := strings.Split(path, string(filepath.Separator))
	for _, p := range parts {
		if strings.HasPrefix(p, ".") && p != "." && p != ".." {
			return true
		}
		// 常见巨型目录
		switch p {
		case "node_modules", "__pycache__", "vendor", "dist", "build":
			return true
		}
	}

	// 二进制文件扩展名
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".o", ".a", ".class", ".pyc", ".jar",
		".war", ".zip", ".tar", ".gz", ".bz2", ".7z", ".rar", ".png", ".jpg",
		".jpeg", ".gif", ".ico", ".pdf", ".woff", ".woff2", ".ttf", ".eot", ".wasm":
		return true
	}
	return false
}

// sortPickerItems 排序候选项：目录在前，文件在后，字母序。
func sortPickerItems(items []pickerItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Display < items[j].Display
	})
}

// fuzzyFilter 对候选项执行模糊过滤（按路径分量最小前缀匹配）。
func fuzzyFilter(filter string, items []pickerItem) []pickerItem {
	if filter == "" {
		// 无过滤时返回前 20 项
		if len(items) > 20 {
			return items[:20]
		}
		return items
	}

	filter = strings.ToLower(filter)

	// 分类：按分量前缀匹配、子串匹配、其他
	// 每组内按匹配位置升序（越左越优先），位置相同按字母序
	var prefix, substr, other []pickerItem
	for _, item := range items {
		display := strings.ToLower(item.Display)
		switch {
		case pathPrefixMatch(filter, display):
			prefix = append(prefix, item)
		case strings.Contains(display, filter):
			substr = append(substr, item)
		default:
			other = append(other, item)
		}
	}

	sortByMatchPos(filter, prefix)
	sortByMatchPos(filter, substr)

	result := append(prefix, substr...)
	result = append(result, other...)

	if len(result) > 20 {
		return result[:20]
	}
	return result
}

// pathPrefixMatch 按路径分量检查 filter 是否为 display 的最小前缀匹配。
// filter 的每个 / 分隔分量都必须为 display 对应分量的前缀。
// 例：spec/reference 匹配 specs/reference-context.md，
//
//	因为 spec ≤ specs 且 reference ≤ reference-context.md。
func pathPrefixMatch(filter, display string) bool {
	// 归一化路径分隔符为 /，确保 Windows 下 filepath.WalkDir 返回的 \ 与用户输入的 / 可比对。
	filterParts := strings.Split(filepath.ToSlash(filter), "/")
	displayParts := strings.Split(filepath.ToSlash(display), "/")

	if len(filterParts) > len(displayParts) {
		return false
	}

	for i, fp := range filterParts {
		if i >= len(displayParts) {
			return false
		}
		if !strings.HasPrefix(displayParts[i], fp) {
			return false
		}
	}
	return true
}

// sortByMatchPos 按 filter 在 Display 中的首次出现位置升序排列，
// 位置越靠左越优先；无法作为连续子串匹配的（Index 返回 -1）排在最后；
// 位置相同时按 Display 字母序。
func sortByMatchPos(filter string, items []pickerItem) {
	sort.Slice(items, func(i, j int) bool {
		di := strings.ToLower(items[i].Display)
		dj := strings.ToLower(items[j].Display)
		pi := strings.Index(di, filter)
		pj := strings.Index(dj, filter)
		// -1 排在所有有效位置之后
		if pi < 0 {
			pi = 1 << 30
		}
		if pj < 0 {
			pj = 1 << 30
		}
		if pi != pj {
			return pi < pj
		}
		return di < dj
	})
}
