package domain

import "context"

// The request correlation id rides the context for the same reason Caller does
// (see caller.go): the one consumer that needs it — the AI budget guard's
// `llm_call` accounting log — sits several layers below the gin middleware that
// mints it, and threading a string through every gateway signature for a log
// field would not be worth the churn.
//
// The key is an unexported struct type, NOT the bare string "request_id" the
// router used before R3. Context keys are compared by (type, value), so a plain
// string key shares ONE namespace with every package linked into the process:
// any dependency that stores its own "request_id" either shadows ours or is
// shadowed by it, and whichever side loses silently reads a value it never
// wrote. That is staticcheck SA1029, and it is why the router's WithValue call
// now goes through WithRequestID instead of writing the string key directly.
type requestIDCtxKey struct{}

// WithRequestID carries the request's correlation id — the same value stamped on
// the X-Request-ID response header and on the zap access-log line — down the call
// chain. Set once by the root router middleware, for every request.
//
// An empty id is ignored so a caller cannot blank an id already on the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// RequestIDFromContext returns the correlation id, or "" when the context did not
// originate from an HTTP request (automation, worker, seed) — callers substitute
// their own placeholder rather than being handed a fake id here.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDCtxKey{}).(string)
	return id
}
