package audit

import "context"

type entryKey struct{}

// WithEntry carries trusted actor/request metadata from an adapter to an audited application command.
func WithEntry(ctx context.Context, entry Entry) context.Context {
	return context.WithValue(ctx, entryKey{}, entry)
}
func EntryFromContext(ctx context.Context) (Entry, bool) {
	entry, ok := ctx.Value(entryKey{}).(Entry)
	return entry, ok
}
