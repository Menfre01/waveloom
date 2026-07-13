package hashline

import "context"

type storeKey struct{}

// WithStore injects a SnapshotStore into ctx.
func WithStore(ctx context.Context, store *SnapshotStore) context.Context {
	return context.WithValue(ctx, storeKey{}, store)
}

// StoreFromContext extracts the SnapshotStore from ctx.
// Returns nil if not found (edit_file_hashline will reject with an error)
// or if ctx is nil.
func StoreFromContext(ctx context.Context) *SnapshotStore {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(storeKey{}).(*SnapshotStore)
	return s
}
