package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// PathToURI 将文件系统绝对路径转为 LSP file:// URI。
// Path segments are URL-escaped to handle spaces and special characters.
func PathToURI(path string) DocumentURI {
	abs := filepath.Clean(path)
	abs = filepath.ToSlash(abs)

	// Split into segments and URL-escape each one
	parts := strings.Split(abs, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	escaped := strings.Join(parts, "/")

	if !strings.HasPrefix(escaped, "/") {
		escaped = "/" + escaped
	}
	return DocumentURI("file://" + escaped)
}

// URIToPath 将 LSP file:// URI 转为文件系统绝对路径。
// Path segments are URL-unescaped.
func URIToPath(uri DocumentURI) string {
	u, err := url.Parse(string(uri))
	if err != nil {
		return string(uri)
	}
	p := u.Path
	// Unescape path segments (url.Parse already does partial unescaping)
	if unescaped, err := url.PathUnescape(p); err == nil {
		p = unescaped
	}
	return p
}
