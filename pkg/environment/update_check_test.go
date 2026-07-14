package environment

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckForUpdate_DevVersion(t *testing.T) {
	info, err := CheckForUpdate(context.Background(), "dev")
	if err != nil {
		t.Fatalf("dev version should not error: %v", err)
	}
	if info != nil {
		t.Errorf("dev version should return nil, got %+v", info)
	}
}

func TestCheckForUpdate_EmptyVersion(t *testing.T) {
	info, err := CheckForUpdate(context.Background(), "")
	if err != nil {
		t.Fatalf("empty version should not error: %v", err)
	}
	if info != nil {
		t.Errorf("empty version should return nil, got %+v", info)
	}
}

func TestCheckLatestRelease_NewVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/releases/tag/v1.0.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	info, err := checkLatestRelease(context.Background(), server.URL, "v0.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
		return
		return
	}
	if !info.UpdateAvailable {
		t.Error("expected UpdateAvailable=true for v1.0.0 > v0.1.0")
	}
	if info.LatestVersion != "v1.0.0" {
		t.Errorf("latest version: got %q, want v1.0.0", info.LatestVersion)
	}
}

func TestCheckLatestRelease_SameVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/releases/tag/v0.1.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	info, err := checkLatestRelease(context.Background(), server.URL, "v0.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
		return
		return
	}
	if info.UpdateAvailable {
		t.Error("expected UpdateAvailable=false for same version")
	}
}

func TestCheckLatestRelease_NotRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	_, err := checkLatestRelease(context.Background(), server.URL, "v0.1.0")
	if err == nil {
		t.Error("expected error for 200 (not a redirect)")
	}
}

func TestCheckLatestRelease_EmptyLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	info, err := checkLatestRelease(context.Background(), server.URL, "v0.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
		return
		return
	}
	if info.LatestVersion != "" {
		t.Errorf("expected empty latest version, got %q", info.LatestVersion)
	}
	if info.UpdateAvailable {
		t.Error("expected UpdateAvailable=false for empty tag")
	}
}

func TestCheckLatestRelease_TagWithoutVPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/releases/tag/0.2.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	info, err := checkLatestRelease(context.Background(), server.URL, "v0.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
		return
		return
	}
	if !info.UpdateAvailable {
		t.Error("expected UpdateAvailable=true for 0.2.0 > v0.1.0")
	}
	if info.LatestVersion != "0.2.0" {
		t.Errorf("latest version: got %q, want 0.2.0", info.LatestVersion)
	}
}

func TestCheckForUpdate_Network(t *testing.T) {
	// 真实 GitHub releases/latest 重定向调用，无需 API 认证、不受限流。
	info, err := CheckForUpdate(context.Background(), "v0.0.0")
	if err != nil {
		t.Skipf("network not available: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil info")
		return
		return
	}
	if info.CurrentVersion != "v0.0.0" {
		t.Errorf("current version: got %q, want v0.0.0", info.CurrentVersion)
	}
	if info.LatestVersion == "" {
		t.Error("latest version should not be empty")
	}
	t.Logf("latest: %s, update available: %v", info.LatestVersion, info.UpdateAvailable)
}

func TestUpdateCache_SetGet(t *testing.T) {
	var cache UpdateCache

	info, done := cache.Get()
	if done {
		t.Error("expected done=false before Set")
	}
	if info != nil {
		t.Error("expected nil before Set")
	}

	expected := &UpdateInfo{LatestVersion: "v1.0.0", UpdateAvailable: true}
	cache.Set(expected)

	info, done = cache.Get()
	if !done {
		t.Error("expected done=true after Set")
	}
	if info != expected {
		t.Errorf("got %+v, want %+v", info, expected)
	}

	cache.Set(nil)
	info, done = cache.Get()
	if !done {
		t.Error("expected done=true after Set(nil)")
	}
	if info != nil {
		t.Errorf("expected nil after Set(nil), got %+v", info)
	}
}
