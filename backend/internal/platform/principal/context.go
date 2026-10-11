// Package principal carries a verified caller without depending on its authentication method.
package principal

import "context"

type Kind string

const (
	Admin  Kind = "admin"
	APIKey Kind = "api_key"
)

type Identity struct {
	Kind Kind
	ID   string
}

// ActorID preserves existing administrator replay namespaces and isolates each API key.
func (p Identity) ActorID() string {
	if p.Kind == APIKey {
		return "api_key:" + p.ID
	}
	return p.ID
}

type contextKey struct{}

func WithContext(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, identity)
}

func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	return identity, ok && identity.ID != "" && (identity.Kind == Admin || identity.Kind == APIKey)
}
