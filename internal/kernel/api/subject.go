package api

import "context"

// subjectKey is the key of the subject of a request in its context.
type subjectKey struct{}

// WithSubject returns ctx with room for the subject of the request it
// carries, which Subject sets and SubjectOf reads.
func WithSubject(ctx context.Context) context.Context {
	return context.WithValue(ctx, subjectKey{}, new(string))
}

// Subject names what the operation of ctx acts on, as a certificate's main
// domain or a provider's name, for the audit log; nothing happens outside
// a request. The last name given stays.
func Subject(ctx context.Context, name string) {
	if p, ok := ctx.Value(subjectKey{}).(*string); ok && name != "" {
		*p = name
	}
}

// SubjectOf returns the subject Subject gave the request of ctx.
func SubjectOf(ctx context.Context) string {
	if p, ok := ctx.Value(subjectKey{}).(*string); ok {
		return *p
	}
	return ""
}
