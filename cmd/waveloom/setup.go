package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// setupState
// ---------------------------------------------------------------------------

type setupState struct {
	theme      string
	locale     string
	prov       string
	model      string
	subModel   string
	baseURL    string
	apiKey     string
	configPath string
	lc         *Messages
}

// ---------------------------------------------------------------------------
// setupModel
// ---------------------------------------------------------------------------

type setupModel struct {
	state       *setupState
	step        int
	form        *huh.Form
	formInitCmd tea.Cmd
	width       int
	height      int
	showBanner  bool
	quitting    bool
}

const totalSteps = 5

func newSetupModel(loc Locale) *setupModel {
	lc := messagesFor(loc)
	return &setupModel{
		state: &setupState{
			theme:    "auto",
			locale:   string(loc),
			prov:     "deepseek",
			model:    "deepseek-v4-pro",
			subModel: "deepseek-v4-flash",
			baseURL:  "https://api.deepseek.com",
			lc:       lc,
		},
	}
}

func (m *setupModel) Init() tea.Cmd {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".waveloom", "settings.json")
	_ = os.MkdirAll(filepath.Dir(configPath), 0o755)
	if settings, err := llm.LoadSettingsIfExists(configPath); err == nil && settings != nil && settings.APIKey != "" {
		m.showBanner = true
	}
	m.buildForm()
	initCmd := m.formInitCmd
	m.formInitCmd = nil
	return initCmd
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		return m, tea.Quit
	}

	// 全局 Ctrl+C 退出 / ESC 回退
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "esc":
			if m.step > 0 {
				m.step--
				m.buildForm()
				initCmd := m.formInitCmd
				m.formInitCmd = nil
				return m, initCmd
			}
			m.quitting = true
			return m, tea.Quit
		}
	}

	// 终端 resize → 记录尺寸，如果变化超过阈值则重建 form
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		prevW := m.width
		m.width = ws.Width
		m.height = ws.Height
		if prevW == 0 || abs(ws.Width-prevW) > 4 {
			m.buildForm()
			initCmd := m.formInitCmd
			m.formInitCmd = nil
			return m, tea.Batch(initCmd, func() tea.Msg {
				return tea.WindowSizeMsg{Width: ws.Width, Height: ws.Height}
			})
		}
	}

	// 全部消息路由到 huh form
	f, cmd := m.form.Update(msg)
	m.form = f.(*huh.Form)

	// 处理遗留 init 命令
	if m.formInitCmd != nil {
		cmd = tea.Batch(cmd, m.formInitCmd)
		m.formInitCmd = nil
	}

	// 状态转换
	switch m.form.State {
	case huh.StateCompleted:
		m.handleStepComplete()
		if m.step < 6 {
			m.buildForm()
			initCmd := m.formInitCmd
			m.formInitCmd = nil
			return m, initCmd
		}
		m.quitting = true
		return m, tea.Quit
	}

	return m, cmd
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (m *setupModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	var parts []string

	// Logo
	parts = append(parts, renderSetupLogo(m.width))

	// Banner
	if m.showBanner {
		parts = append(parts, renderBannerLine(m.state.lc))
	}

	// Form（左缩进 4 列）
	formView := lipgloss.NewStyle().PaddingLeft(4).Render(m.form.View())
	parts = append(parts, formView)

	// 底部快捷键提示
	parts = append(parts, renderSetupHelp(m.state.lc))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, parts...))
	v.AltScreen = true
	return v
}

// ---------------------------------------------------------------------------
// 步骤完成
// ---------------------------------------------------------------------------

func (m *setupModel) handleStepComplete() {
	switch m.step {
	case 0:
		m.state.theme = m.form.GetString("theme")
		// 立即应用主题，setup 自身配色跟随变化
		switch m.state.theme {
		case "light":
			applyTheme(lightPalette)
		case "dark":
			applyTheme(darkPalette)
		case "darkcolorblind":
			applyTheme(darkColorBlindPalette)
		case "lightcolorblind":
			applyTheme(lightColorBlindPalette)
		case "auto":
			if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
				applyTheme(darkPalette)
			} else {
				applyTheme(lightPalette)
			}
		default:
			applyTheme(darkPalette)
		}
		m.step++
	case 1:
		m.state.locale = m.form.GetString("locale")
		m.state.lc = messagesFor(Locale(m.state.locale))
		m.step++
	case 2:
		m.state.prov = m.form.GetString("provider")
		switch m.state.prov {
		case "openai":
			m.state.model = "gpt-4o"
			m.state.subModel = "gpt-4o-mini"
			m.state.baseURL = "https://api.openai.com/v1"
		case "deepseek":
			m.state.model = "deepseek-v4-pro"
			m.state.subModel = "deepseek-v4-flash"
			m.state.baseURL = "https://api.deepseek.com"
		default:
			// 自定义 provider — 模型和 baseURL 由用户自行填写
			m.state.model = ""
			m.state.subModel = ""
			m.state.baseURL = ""
		}
		m.step++
	case 3:
		m.state.apiKey = m.form.GetString("apiKey")
		if m.state.apiKey == "" {
			fmt.Println("  " + m.state.lc.SetupAPIKeyEmptyWarn)
			os.Exit(1)
		}
		m.step++
	case 4:
		model := m.form.GetString("model")
		if model != "" {
			m.state.model = model
		}
		subModel := m.form.GetString("subModel")
		if subModel != "" {
			m.state.subModel = subModel
		}
		baseURL := m.form.GetString("baseURL")
		if baseURL != "" {
			m.state.baseURL = baseURL
		}
		m.step++
	case 5:
		choice := m.form.GetString("saveChoice")
		if choice == "save" {
			m.saveAndFinish()
			m.step++
		} else {
			// Back → 回到 Step 4
			m.step = 4
		}
	}
}

// ---------------------------------------------------------------------------
// buildForm
// ---------------------------------------------------------------------------

func (m *setupModel) buildForm() {
	lc := m.state.lc
	theme := setupHuhTheme()
	formWidth := m.width - 8
	if formWidth < 40 {
		formWidth = 40
	}
	if formWidth > 72 {
		formWidth = 72
	}

	switch m.step {
	case 0:
		sel := huh.NewSelect[string]().
			Key("theme").
			Title(fmt.Sprintf(lc.SetupStepTheme, 1, totalSteps)).
			Options(
				huh.NewOption(lc.PickerThemeAuto, "auto"),
				huh.NewOption("Dark", "dark"),
				huh.NewOption("Light", "light"),
				huh.NewOption("Dark CB", "darkcolorblind"),
				huh.NewOption("Light CB", "lightcolorblind"),
			).
			Value(&m.state.theme)
		m.form = huh.NewForm(huh.NewGroup(sel)).
			WithTheme(theme).WithWidth(formWidth).WithShowHelp(false)

	case 1:
		localeVal := m.state.locale
		sel := huh.NewSelect[string]().
			Key("locale").
			Title(fmt.Sprintf(lc.SetupStepLocale, 2, totalSteps)).
			Options(
				huh.NewOption(m.state.lc.SetupLocaleZhCNLabel, "zh-CN"),
				huh.NewOption(m.state.lc.SetupLocaleEnUSLabel, "en-US"),
			).
			Value(&localeVal)
		m.form = huh.NewForm(huh.NewGroup(sel)).
			WithTheme(theme).WithWidth(formWidth).WithShowHelp(false)

	case 2:
		provVal := m.state.prov
		sel := huh.NewSelect[string]().
			Key("provider").
			Title(fmt.Sprintf(lc.SetupStepProvider, 3, totalSteps)).
			Options(
				huh.NewOption("DeepSeek  (Recommended)", "deepseek"),
				huh.NewOption("OpenAI", "openai"),
				huh.NewOption(lc.SetupProviderOther, "other"),
			).
			Value(&provVal)
		m.form = huh.NewForm(huh.NewGroup(sel)).
			WithTheme(theme).WithWidth(formWidth).WithShowHelp(false)

	case 3:
		apiKeyVal := m.state.apiKey
		apiDesc := fmt.Sprintf("https://platform.%s.com/api_keys", m.state.prov)
		inp := huh.NewInput().
			Key("apiKey").
			Title(fmt.Sprintf(lc.SetupStepAPIKey, 4, totalSteps)).
			Description(apiDesc).
			Placeholder("sk-...").
			EchoMode(huh.EchoModePassword).
			Value(&apiKeyVal)
		m.form = huh.NewForm(huh.NewGroup(inp)).
			WithTheme(theme).WithWidth(formWidth).WithShowHelp(false)

	case 4:
		defaultModel := m.state.model
		modelVal := defaultModel
		defaultSubModel := m.state.subModel
		subModelVal := defaultSubModel
		defaultBaseURL := m.state.baseURL
		baseURLVal := defaultBaseURL
		desc := ""
		subDesc := ""
		baseURLDesc := lc.SetupBaseURLDesc
		switch m.state.prov {
		case "deepseek":
			desc = "deepseek-v4-pro (Recommended) / deepseek-v4-flash"
			subDesc = fmt.Sprintf(lc.SetupSubModelDesc, "deepseek-v4-flash")
		case "openai":
			desc = "gpt-4o (Recommended) / gpt-4o-mini"
			subDesc = fmt.Sprintf(lc.SetupSubModelDesc, "gpt-4o-mini")
		}
		modelInp := huh.NewInput().
			Key("model").
			Title(fmt.Sprintf(lc.SetupStepModel, 5, totalSteps)).
			Description(desc).
			Placeholder(defaultModel).
			Value(&modelVal)
		subInp := huh.NewInput().
			Key("subModel").
			Title(fmt.Sprintf(lc.SetupStepSubModel, 5, totalSteps)).
			Description(subDesc).
			Placeholder(defaultSubModel).
			Value(&subModelVal)
		baseURLInp := huh.NewInput().
			Key("baseURL").
			Title(fmt.Sprintf(lc.SetupStepBaseURL, 5, totalSteps)).
			Description(baseURLDesc).
			Placeholder(defaultBaseURL).
			Value(&baseURLVal)
		m.form = huh.NewForm(huh.NewGroup(modelInp, subInp, baseURLInp)).
			WithTheme(theme).WithWidth(formWidth).WithShowHelp(false)

	case 5:
		note := huh.NewNote().
			Title(lc.SetupConfirmTitle).
			Description(renderSummary(m.state))
		saveChoice := "save"
		sel := huh.NewSelect[string]().
			Key("saveChoice").
			Title(lc.SetupConfirmPrompt).
			Options(
				huh.NewOption(lc.SetupConfirmSave, "save"),
				huh.NewOption(lc.SetupConfirmBack, "back"),
			).
			Value(&saveChoice)
		m.form = huh.NewForm(huh.NewGroup(note, sel)).
			WithTheme(theme).WithWidth(formWidth).WithShowHelp(false)
	}

	m.formInitCmd = m.form.Init()
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

func renderSetupLogo(width int) string {
	var sb strings.Builder
	if width >= 80 {
		for i, line := range asciiArt {
			s := lipgloss.NewStyle().
				Foreground(colorLogoGradient[i]).
				Bold(true).
				Width(width).
				Align(lipgloss.Center).
				Render(line)
			sb.WriteString(s)
			sb.WriteString("\n")
		}
	} else {
		logoLine := lipgloss.NewStyle().
			Foreground(colorLogoGradient[0]).
			Bold(true).
			Width(width).
			Align(lipgloss.Center).
			Render("WAVELOOM")
		sb.WriteString(logoLine)
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderBannerLine(lc *Messages) string {
	return lipgloss.NewStyle().
		Foreground(colorAccentGold).
		Padding(0, 2).
		Render("  " + lc.SetupOverwriteWarn)
}

func renderSetupHelp(lc *Messages) string {
	return lipgloss.NewStyle().
		Foreground(colorMuted).
		Padding(0, 4).
		Render(lc.SetupHelpHint)
}

func renderSummary(s *setupState) string {
	lc := s.lc
	lines := []string{
		fmt.Sprintf("%s:  %s", lc.SetupSummaryTheme, s.theme),
		fmt.Sprintf("%s:  %s", lc.SetupSummaryLanguage, s.locale),
		fmt.Sprintf("%s:  %s", lc.SetupSummaryProvider, s.prov),
		fmt.Sprintf("%s:  %s", lc.SetupSummaryModel, s.model),
		fmt.Sprintf("%s:  %s", lc.SetupSummarySubModel, s.subModel),
		fmt.Sprintf("%s:  %s", lc.SetupSummaryBaseURL, s.baseURL),
		fmt.Sprintf("%s:  %s", lc.SetupSummaryAPIKey, maskKey(s.apiKey)),
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// saveAndFinish
// ---------------------------------------------------------------------------

func (m *setupModel) saveAndFinish() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configPath := filepath.Join(homeDir, ".waveloom", "settings.json")
	m.state.configPath = configPath
	_ = os.MkdirAll(filepath.Dir(configPath), 0o755)

	settings := &llm.LLMSettings{
		APIKey:   m.state.apiKey,
		Provider: m.state.prov,
		Model:    m.state.model,
		SubModel: m.state.subModel,
		BaseURL:  m.state.baseURL,
		Timeout:  "600s",
	}
	if m.state.prov == "deepseek" {
		settings.ExtraParams = map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": "max",
		}
	}
	_ = writeFullSetup(configPath, settings, m.state.locale, m.state.theme)
}

// ---------------------------------------------------------------------------
// setupHuhTheme
// ---------------------------------------------------------------------------

func setupHuhTheme() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		t := huh.ThemeBase(isDark)
		t.Focused.Title = t.Focused.Title.Foreground(colorHeaderAccent)
		t.Focused.Description = t.Focused.Description.Foreground(colorFooterFg)
		t.Focused.Base = lipgloss.NewStyle().
			PaddingLeft(1).
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorHeaderAccent)
		t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(colorOK).SetString("▌ ")
		t.Blurred.SelectSelector = lipgloss.NewStyle().Foreground(colorMuted).SetString("  ")
		t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colorOK)
		t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colorOK)
		t.Blurred.Title = t.Blurred.Title.Foreground(colorHeaderFg)
		t.Blurred.Description = t.Blurred.Description.Foreground(colorFooterFg)
		t.Blurred.Base = t.Blurred.Base.BorderForeground(lipgloss.Color("#444444"))
		return t
	})
}

// ---------------------------------------------------------------------------
// 入口
// ---------------------------------------------------------------------------

func runSetup(loc Locale) {
	// 跟随终端背景色自动选择初始主题
	if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
		applyTheme(darkPalette)
	} else {
		applyTheme(lightPalette)
	}
	m := newSetupModel(loc)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		os.Exit(1)
	}
	lc := m.state.lc
	fmt.Printf("\n  %s\n\n", lc.SetupDoneTitle)
	fmt.Printf("  %s\n", fmt.Sprintf(lc.SetupDoneConfigSaved, m.state.configPath))
	fmt.Printf("  %s: %s\n", lc.SetupSummaryLanguage, m.state.locale)
	fmt.Printf("  %s: %s\n", lc.SetupSummaryTheme, m.state.theme)
	fmt.Printf("  %s: %s\n", lc.SetupSummaryProvider, m.state.prov)
	fmt.Printf("  %s: %s\n", lc.SetupSummaryModel, m.state.model)
	fmt.Printf("  %s: %s\n", lc.SetupSummarySubModel, m.state.subModel)
	fmt.Printf("  %s: %s\n", lc.SetupSummaryBaseURL, m.state.baseURL)
	fmt.Printf("\n  %s\n\n", lc.SetupDoneReady)
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func maskKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

func writeFullSetup(path string, llmSettings *llm.LLMSettings, locale, theme string) error {
	full := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &full)
	}
	if llmSettings != nil {
		b, err := json.Marshal(llmSettings)
		if err != nil {
			return err
		}
		var llmMap map[string]any
		_ = json.Unmarshal(b, &llmMap)
		full["llm"] = llmMap
	}
	if locale != "" {
		full["locale"] = locale
	}
	if theme != "" {
		full["theme"] = theme
	}
	out, err := json.MarshalIndent(full, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func needsSetup() bool {
	projectPath := filepath.Join(".waveloom", "settings.json")
	if s, _ := llm.LoadSettingsIfExists(projectPath); s != nil && s.APIKey != "" {
		return false
	}
	homeDir, _ := os.UserHomeDir()
	globalPath := filepath.Join(homeDir, ".waveloom", "settings.json")
	if s, _ := llm.LoadSettingsIfExists(globalPath); s != nil && s.APIKey != "" {
		return false
	}
	if os.Getenv("LLM_API_KEY") != "" {
		return false
	}
	return true
}
