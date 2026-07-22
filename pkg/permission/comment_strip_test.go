package permission

import "testing"

func TestStripCommentLines_NoComment(t *testing.T) {
	got := StripCommentLines("echo hello")
	if got != "echo hello" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestStripCommentLines_StripsComment(t *testing.T) {
	got := StripCommentLines("echo hello\n# this is a comment\nls -la")
	if got != "echo hello\nls -la" {
		t.Errorf("expected 'echo hello\\nls -la', got %q", got)
	}
}

func TestStripCommentLines_PreservesQuotedHash(t *testing.T) {
	// 引号内的 # 不是注释
	got := StripCommentLines("echo '# not a comment'\nls")
	if got != "echo '# not a comment'\nls" {
		t.Errorf("quoted # should be preserved, got %q", got)
	}
}

func TestStripCommentLines_CommentWithQuoteInsideStillStripped(t *testing.T) {
	// 注释行内的引号不影响——整行仍被剥离
	got := StripCommentLines("echo hello\n# ' comment with quote\nls")
	if got != "echo hello\nls" {
		t.Errorf("comment with quote should still be stripped, got %q", got)
	}
}

func TestStripCommentLines_MultiLineQuotedHash(t *testing.T) {
	// 跨多行的引号，内部的 # 不是注释
	got := StripCommentLines("echo 'line1\n#line2'\nls")
	if got != "echo 'line1\n#line2'\nls" {
		t.Errorf("multiline quoted # should be preserved, got %q", got)
	}
}

func TestStripCommentLines_OnlyComments(t *testing.T) {
	got := StripCommentLines("# just a comment")
	if got != "" {
		t.Errorf("only comments should yield empty, got %q", got)
	}
}
