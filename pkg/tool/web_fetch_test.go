package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebFetchSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Hello, world!"))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/test.txt",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !strings.Contains(result.Content, "Hello, world!") {
		t.Errorf("expected content to contain 'Hello, world!', got %q", result.Content)
	}
	if result.Meta.ByteCount == 0 {
		t.Error("ByteCount should be > 0")
	}
}

func TestWebFetchHTMLStripped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Title</h1><p>Paragraph</p></body></html>"))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/page.html",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	// HTML tags should be stripped
	if strings.Contains(result.Content, "<html>") || strings.Contains(result.Content, "<body>") {
		t.Errorf("HTML tags should be stripped, got %q", result.Content)
	}
	// Should contain the text content
	if !strings.Contains(result.Content, "Title") || !strings.Contains(result.Content, "Paragraph") {
		t.Errorf("expected content to contain 'Title' and 'Paragraph', got %q", result.Content)
	}
}

func TestWebFetchJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"waveloom","version":"0.1.0"}`))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/api.json",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !strings.Contains(result.Content, "waveloom") {
		t.Errorf("expected content to contain 'waveloom', got %q", result.Content)
	}
}

func TestWebFetchHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/missing",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for 404 response")
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("expected Recoverable error, got %v", result.Error.Class)
	}
	if result.Error.Kind != ErrKindCommandFailed {
		t.Errorf("expected ErrKindCommandFailed, got %v", result.Error.Kind)
	}
}

func TestWebFetchInvalidURL(t *testing.T) {
	tool := &WebFetch{}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: "not-a-valid-url",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for invalid URL")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("expected ErrKindInvalidArgs, got %v", result.Error.Kind)
	}
}

func TestWebFetchTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 阻塞直到客户端超时断开，通过 ctx 感知取消避免 httptest.Server.Close 阻塞
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL:       server.URL + "/slow",
		TimeoutMs: 50,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected timeout error")
	}
	if result.Error.Kind != ErrKindTimeout {
		t.Errorf("expected ErrKindTimeout, got %v", result.Error.Kind)
	}
}

func TestWebFetchBinaryContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/image.png",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for binary content type")
	}
	if result.Error.Kind != ErrKindBinaryFile {
		t.Errorf("expected ErrKindBinaryFile, got %v", result.Error.Kind)
	}
}

func TestWebFetchSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Write 100KB of data
		data := strings.Repeat("a", 100*1024)
		_, _ = w.Write([]byte(data))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL:     server.URL + "/large.txt",
		MaxSize: 1024, // limit to 1KB
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	// Should be truncated
	if strings.Count(result.Content, "truncated") == 0 {
		t.Error("expected truncated indicator in content")
	}
	if !strings.Contains(result.Content, "a") {
		t.Error("expected some content")
	}
}

func TestWebFetchNameDescription(t *testing.T) {
	tool := &WebFetch{}
	if tool.Name() != "web_fetch" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_fetch")
	}
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if tool.ConcurrentSafe() != true {
		t.Error("ConcurrentSafe() should be true")
	}
}

func TestWebFetchSchemaValid(t *testing.T) {
	tool := &WebFetch{}
	schema := tool.Schema()
	if len(schema) == 0 {
		t.Error("Schema() should not be empty")
	}
	// Should be valid JSON
	if !strings.Contains(string(schema), "url") {
		t.Error("Schema should contain 'url'")
	}
}

// ── SSRF 防护测试 ──

func TestWebFetchLoopbackRejected(t *testing.T) {
	tool := &WebFetch{}

	tests := []struct {
		name string
		url  string
	}{
		{"IPv4 loopback", "http://127.0.0.1:8080/"},
		{"IPv4 loopback alt", "http://127.0.0.2/test"},
		{"IPv6 loopback", "http://[::1]:8080/"},
		{"localhost", "http://localhost/admin"},
		{"localhost with port", "http://localhost:3000/api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), WebFetchParams{URL: tt.url})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Error == nil {
				t.Fatalf("expected error for %s, got nil", tt.url)
			}
			if result.Error.Kind != ErrKindInvalidArgs {
				t.Errorf("expected ErrKindInvalidArgs, got %v", result.Error.Kind)
			}
		})
	}
}

func TestWebFetchPrivateRejected(t *testing.T) {
	tool := &WebFetch{}

	tests := []string{
		"http://10.0.0.1/api",
		"http://172.16.0.1/",
		"http://192.168.1.1/admin",
		"http://169.254.169.254/latest/meta-data/",
	}
	for _, u := range tests {
		t.Run(u, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), WebFetchParams{URL: u})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Error == nil {
				t.Fatalf("expected error for %s, got nil", u)
			}
			if result.Error.Kind != ErrKindInvalidArgs {
				t.Errorf("expected ErrKindInvalidArgs, got %v", result.Error.Kind)
			}
		})
	}
}

func TestWebFetchRedirectToPrivateRejected(t *testing.T) {
	// 第一个请求返回重定向到内网地址，应被 CheckRedirect 拦截
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://127.0.0.1/secret", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true} // 初始 URL 在 loopback，但 CheckRedirect 仍会拦截
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/redirect",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for redirect to loopback")
	}
}

func TestWebFetchRedirectToPublicAllowed(t *testing.T) {
	// 重定向到同服务器的公共地址应被允许
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("redirected content"))
	}))
	defer server.Close()

	tool := &WebFetch{
		skipHostCheck: true,
		httpClient:    server.Client(), // 使用 httptest 内置 client（绕过 SSRF CheckRedirect）
	}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/redirect",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error for public redirect: %v", result.Error)
	}
	if !strings.Contains(result.Content, "redirected content") {
		t.Errorf("expected 'redirected content', got %q", result.Content)
	}
}

func TestWebFetchUnresolvableHostRejected(t *testing.T) {
	tool := &WebFetch{}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: "http://this-host-definitely-does-not-exist.invalid/",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for unresolvable host")
	}
}

// ── context 取消测试 ──

func TestWebFetchContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Hello, world!"))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	tool := &WebFetch{skipHostCheck: true}
	_, err := tool.Execute(ctx, WebFetchParams{
		URL: server.URL + "/test.txt",
	})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestWebFetchContextCancelledDuringRead(t *testing.T) {
	// 服务端先写入少量数据建立响应，然后阻塞等待 context 取消。
	// 客户端分块读取时在后续 read 上阻塞，context 取消后应退出。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// 写入初始数据块，让客户端进入 body 读取循环
		_, _ = w.Write([]byte("initial chunk\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 阻塞直到客户端 context 取消
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	// 启动 goroutine 在短延迟后取消 context
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	tool := &WebFetch{skipHostCheck: true}
	_, err := tool.Execute(ctx, WebFetchParams{
		URL: server.URL + "/stream",
	})
	if err == nil {
		t.Fatal("expected error with cancelled context during streaming read")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

// ── 新增功能测试 ──

func TestWebFetchMissingContentType(t *testing.T) {
	// 服务端不设置 Content-Type 头，应容错按 text/plain 处理
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 显式不设置 Content-Type
		_, _ = w.Write([]byte("plain text without content-type"))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/no-ct",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !strings.Contains(result.Content, "plain text without content-type") {
		t.Errorf("expected content to contain body, got %q", result.Content)
	}
}

func TestWebFetchHTMLEntitiesDecoded(t *testing.T) {
	// HTML 实体（&amp; &lt; &gt; &quot; &#39;）应在 stripHTML 后被解码
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><p>a &amp; b &lt; c &gt; d &quot;e&quot; f&#39;</p></body></html>`))
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL: server.URL + "/entities.html",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// 原始实体不应出现在输出中
	for _, entity := range []string{"&amp;", "&lt;", "&gt;", "&quot;", "&#39;"} {
		if strings.Contains(result.Content, entity) {
			t.Errorf("HTML entity %q should have been decoded, got %q", entity, result.Content)
		}
	}
	// 解码后的字符应出现
	for _, want := range []string{"&", "<", ">", `"`, "'"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("expected decoded character %q in output, got %q", want, result.Content)
		}
	}
}

func TestWebFetchBodyReadTimeout(t *testing.T) {
	// 服务端先发送 headers 和部分 body，然后阻塞。
	// 客户端应在 body 读取阶段超时，返回已读取的部分内容。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// 写入初始数据并 flush，让客户端进入 body 读取阶段
		_, _ = w.Write([]byte("partial content before timeout\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 阻塞直到客户端超时断开，通过 ctx 感知取消
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	tool := &WebFetch{skipHostCheck: true}
	result, err := tool.Execute(context.Background(), WebFetchParams{
		URL:       server.URL + "/slow-body",
		TimeoutMs: 100,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// 应返回成功（部分内容），而非错误
	if result.Error != nil {
		t.Fatalf("expected partial content on body read timeout, got error: %v", result.Error)
	}
	// 应包含已读取的部分内容
	if !strings.Contains(result.Content, "partial content before timeout") {
		t.Errorf("expected partial content in output, got %q", result.Content)
	}
	// 应包含超时截断标记
	if !strings.Contains(result.Content, "[truncated:") {
		t.Errorf("expected timeout truncated marker in output, got %q", result.Content)
	}
}
