package diagnostics

import "context"

type contextKey struct{}

func Context(ctx context.Context, l *Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, l)
}
func FromContext(ctx context.Context) *Logger { l, _ := ctx.Value(contextKey{}).(*Logger); return l }
