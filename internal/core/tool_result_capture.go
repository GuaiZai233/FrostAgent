package core

import "context"

type rawToolResultKey struct{}

// WithRawToolResultReceiver observes the logical response before presentation.
func WithRawToolResultReceiver(ctx context.Context, receive func(string)) context.Context {
	return context.WithValue(ctx, rawToolResultKey{}, receive)
}

func CaptureRawToolResult(ctx context.Context, raw string) {
	if receive, ok := ctx.Value(rawToolResultKey{}).(func(string)); ok {
		receive(raw)
	}
}
