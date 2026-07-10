package filehistory

import "context"

type (
	fileHistoryKey  struct{}
	sessionDirKey   struct{}
	messageIDCtxKey struct{}
)

// WithFileHistory injects FileHistoryState into ctx.
func WithFileHistory(ctx context.Context, fh *FileHistoryState) context.Context {
	return context.WithValue(ctx, fileHistoryKey{}, fh)
}

// FromContext extracts FileHistoryState from ctx.
func FromContext(ctx context.Context) *FileHistoryState {
	fh, _ := ctx.Value(fileHistoryKey{}).(*FileHistoryState)
	return fh
}

// WithSessionDir injects session directory path into ctx for filehistory backup location.
func WithSessionDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, sessionDirKey{}, dir)
}

// SessionDirFromContext extracts session directory from ctx.
func SessionDirFromContext(ctx context.Context) string {
	s, _ := ctx.Value(sessionDirKey{}).(string)
	return s
}

// WithMessageID injects the current user message ID into ctx for filehistory tracking.
func WithMessageID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, messageIDCtxKey{}, id)
}

// MessageIDFromContext extracts the user message ID from ctx.
func MessageIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(messageIDCtxKey{}).(string)
	return s
}
