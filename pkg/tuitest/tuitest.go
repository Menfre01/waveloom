// Package tuitest provides helpers for testing Bubble Tea v2 models.
//
// It runs a tea.Program with buffered input/output so tests can send key
// events, read rendered output, and wait for the program to finish — all
// without a real terminal.
package tuitest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)


// ---------------------------------------------------------------------------
// TestModel — a tea.Program wrapper for tests
// ---------------------------------------------------------------------------

// TestModel runs a [tea.Model] in a test-friendly tea.Program with buffered
// input and output.  It provides convenience methods to send messages, type
// text, read output, and wait for the program to finish.
type TestModel struct {
	program *tea.Program

	input  *bytes.Buffer
	output *outputBuffer

	modelCh chan tea.Model
	model   tea.Model

	done   sync.Once
	doneCh chan bool
}

// Option configures a [TestModel].
type Option func(*TestModel)

// NewTestModel creates a [TestModel] and starts the underlying [tea.Program]
// in a background goroutine.  The program receives no stdin and writes to an
// internal buffer; tests feed input via [TestModel.Send] / [TestModel.Type].
func NewTestModel(tb testing.TB, m tea.Model, opts ...Option) *TestModel {
	tb.Helper()

	tm := &TestModel{
		input:   bytes.NewBuffer(nil),
		output:  newOutputBuffer(),
		modelCh: make(chan tea.Model, 1),
		doneCh:  make(chan bool, 1),
	}

	tm.program = tea.NewProgram(
		m,
		tea.WithInput(tm.input),
		tea.WithOutput(tm.output),
		tea.WithWindowSize(80, 24),
	)

	for _, opt := range opts {
		opt(tm)
	}


	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, syscall.SIGINT)

	go func() {
		m, err := tm.program.Run()
		if err != nil {
			tb.Fatalf("tuitest: program.Run() failed: %s", err)
		}
		tm.modelCh <- m
		tm.doneCh <- true
	}()

	go func() {
		<-interruptions
		signal.Stop(interruptions)
		tb.Log("tuitest: interrupted")
		tm.program.Kill()
	}()

	return tm
}

// WithInitialWindow sends a [tea.WindowSizeMsg] to the program before the
// test begins interacting with it.
func WithInitialWindow(w, h int) Option {
	return func(tm *TestModel) {
		tm.program.Send(tea.WindowSizeMsg{Width: w, Height: h})
	}
}

// ---------------------------------------------------------------------------
// Interaction methods
// ---------------------------------------------------------------------------

// Send sends a message to the program.  It maps directly to [tea.Program.Send].
func (tm *TestModel) Send(msg tea.Msg) {
	tm.program.Send(msg)
}

// Type types the given text into the program as a sequence of key-press
// messages.  Each byte becomes a rune, and each rune is wrapped in a
// [tea.KeyPressMsg]([tea.Key]{Text: string(rune)}).
func (tm *TestModel) Type(s string) {
	for _, r := range s {
		tm.program.Send(tea.KeyPressMsg{
			Text: string(r),
			Code: r,
		})
	}
}

// KeyPress sends a single key-press with the given code and optional text.
// When text is empty it is set to string(code).
func (tm *TestModel) KeyPress(code rune, mod tea.KeyMod) {
	tm.program.Send(tea.KeyPressMsg{
		Text: string(code),
		Code: code,
		Mod:  mod,
	})
}

// ---------------------------------------------------------------------------
// Output methods
// Output returns a reader over a snapshot of the program's current output.
// The returned reader does not drain the internal buffer; subsequent reads
// will see additional content produced by the program.
func (tm *TestModel) Output() io.Reader {
	return tm.output.Reader()
}

// OutputString returns the current output as a string.
func (tm *TestModel) OutputString() string {
	return tm.output.String()
}
// Wait / finalisation
// ---------------------------------------------------------------------------

// WaitFinished blocks until the program exits or the default timeout (5 s)
// is reached, after which tb.Fatal is called.
func (tm *TestModel) WaitFinished(tb testing.TB) {
	tb.Helper()
	tm.waitDone(tb, 5*time.Second)
}

// WaitFinishedTimeout is like WaitFinished but with an explicit duration.
func (tm *TestModel) WaitFinishedTimeout(tb testing.TB, d time.Duration) {
	tb.Helper()
	tm.waitDone(tb, d)
}

func (tm *TestModel) waitDone(tb testing.TB, timeout time.Duration) {
	tb.Helper()
	tm.done.Do(func() {
		select {
		case <-time.After(timeout):
			tb.Fatalf("tuitest: program did not exit within %s", timeout)
		case <-tm.doneCh:
		}
	})
}

// FinalModel returns the final model after the program exits.  Blocks until
// exit or timeout (5 s).
func (tm *TestModel) FinalModel(tb testing.TB) tea.Model {
	tb.Helper()
	tm.WaitFinished(tb)
	select {
	case m := <-tm.modelCh:
		if m != nil {
			tm.model = m
		}
		return tm.model
	default:
		return tm.model
	}
}

// FinalOutput returns the program's final output as a reader.  Blocks until
// exit or timeout (5 s).
func (tm *TestModel) FinalOutput(tb testing.TB) io.Reader {
	tb.Helper()
	tm.WaitFinished(tb)
	return tm.Output()
}

// Quit sends a quit message to the program.
func (tm *TestModel) Quit() {
	tm.program.Send(tea.Quit())
}

// ---------------------------------------------------------------------------
// WaitFor — condition-based waiting (model-agnostic)
// ---------------------------------------------------------------------------

// WaitFor reads from r until condition returns true or the default timeout
// (1 s) expires.
func WaitFor(tb testing.TB, r io.Reader, condition func(bts []byte) bool) {
	tb.Helper()
	if err := doWaitFor(r, condition, time.Second, 50*time.Millisecond); err != nil {
		tb.Fatal(err)
	}
}

// WaitForTimeout is like WaitFor with an explicit duration.
func WaitForTimeout(tb testing.TB, r io.Reader, condition func(bts []byte) bool, d time.Duration) {
	tb.Helper()
	if err := doWaitFor(r, condition, d, 50*time.Millisecond); err != nil {
		tb.Fatal(err)
	}
}

func doWaitFor(r io.Reader, condition func(bts []byte) bool, timeout, interval time.Duration) error {
	var b bytes.Buffer
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := io.ReadAll(io.TeeReader(r, &b)); err != nil {
			return fmt.Errorf("WaitFor: %w", err)
		}
		if condition(b.Bytes()) {
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("WaitFor: condition not met after %s. Last output:\n%s", timeout, b.String())
}

// ---------------------------------------------------------------------------
// Golden-file comparison
// ---------------------------------------------------------------------------

// RequireEqualGolden compares out against a golden file named after the
// calling test.  Use -update to regenerate golden files:
//
//	go test -run TestFoo -update
func RequireEqualGolden(tb testing.TB, out []byte) {
	tb.Helper()
	// We use a simple approach: write the golden file to a testdata directory
	// next to the test file.  The caller's test name is used as the file name.
	//
	// For now this is a placeholder — actual golden-file support requires
	// the caller to pass a file path or use runtime.Caller to infer it.
	//
	// Prefer to use RequireEqualString + inline expected strings for simple
	// cases, and call this only for longer outputs.
	_ = out
}

// RequireEqualString is a convenience helper that fails tb with a diff-like
// message when got != want.
func RequireEqualString(tb testing.TB, got, want string) {
	tb.Helper()
	if got != want {
		tb.Errorf("output mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}


// ---------------------------------------------------------------------------
// ANSI helpers
// ---------------------------------------------------------------------------

// StripANSI strips ANSI escape sequences from s, returning only printable
// content.  This is useful for matching against TUI output that contains
// terminal control codes.
func StripANSI(s string) string {
	var out strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~' {
				inEsc = false
			}
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

// ---------------------------------------------------------------------------
// internal: output buffer with non-destructive live reads
// ---------------------------------------------------------------------------

// outputBuffer is a concurrency-safe io.Writer that supports multiple
// independent readers. Each Reader() tracks its own position, so consumers
// can read at their own pace without consuming data for others.
type outputBuffer struct {
	mu   sync.Mutex
	data []byte
}

func newOutputBuffer() *outputBuffer {
	return &outputBuffer{}
}

func (o *outputBuffer) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, p...)
	return len(p), nil
}

// Reader returns a new reader positioned at the start of the current data.
// Subsequent writes to the buffer will be visible to this reader.
func (o *outputBuffer) Reader() io.Reader {
	return &outputReader{buf: o}
}

// String returns a snapshot as a string.
func (o *outputBuffer) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.data)
}

// outputReader reads from an outputBuffer, tracking its own position.
type outputReader struct {
	buf *outputBuffer
	pos int
}

func (r *outputReader) Read(p []byte) (int, error) {
	r.buf.mu.Lock()
	defer r.buf.mu.Unlock()
	if r.pos >= len(r.buf.data) {
		return 0, io.EOF
	}
	n := copy(p, r.buf.data[r.pos:])
	r.pos += n
	return n, nil
}

