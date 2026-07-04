package permission

import (
	"os"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		// Permission tests rely heavily on file system behavior (chmod, permissions, etc.)
		// that differs significantly on Windows, causing timeouts and false failures.
		// Skip the entire package on Windows to avoid CI timeout.
		os.Exit(0)
	}
	os.Exit(m.Run())
}
