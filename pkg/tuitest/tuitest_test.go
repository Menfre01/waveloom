package tuitest

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// minimalModel is the smallest valid tea.Model for tuitest verification.
type minimalModel struct {
	text string
}

func (m minimalModel) Init() tea.Cmd       { return nil }
func (m minimalModel) View() tea.View       { return tea.NewView(m.text) }
func (m minimalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.Code == 'q' {
			return m, tea.Quit
		}
		m.text += string(msg.Code)
	case tea.QuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

func TestNewTestModel_TypeAndOutput(t *testing.T) {
	m := minimalModel{}
	tm := NewTestModel(t, m)

	tm.Type("hello")
	// Wait for the renderer to flush before quitting.
	WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(StripANSI(string(b)), "hello")
	})
	tm.Quit()
	tm.WaitFinished(t)

	out := tm.OutputString()
	plain := StripANSI(out)
	if !strings.Contains(plain, "hello") {
		t.Errorf("expected plain output to contain 'hello', got: %s", plain)
	}
}

func TestWaitFor_FindsContent(t *testing.T) {
	m := minimalModel{}
	tm := NewTestModel(t, m)

	tm.Type("world")

	WaitFor(t, tm.Output(), func(b []byte) bool {
		plain := StripANSI(string(b))
		return strings.Contains(plain, "world")
	})
}

func TestFinalModel_ReturnsModel(t *testing.T) {
	m := minimalModel{}
	tm := NewTestModel(t, m)

	tm.Type("x")
	tm.Quit()

	final := tm.FinalModel(t)
	fm, ok := final.(minimalModel)
	if !ok {
		t.Fatalf("expected minimalModel, got %T", final)
	}
	if fm.text != "x" {
		t.Errorf("expected text 'x', got %q", fm.text)
	}
}
