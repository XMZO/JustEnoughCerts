package parse

import (
	"context"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// Parser reads one kind of registry answer.
type Parser interface {
	Name() string
	Supports(raw model.RawResponse) bool
	Priority() int
	Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error)
}

// Registry picks for each answer the parser that reads it.
type Registry struct {
	parsers []Parser
}

// NewRegistry returns a registry of the parsers; of those with the same
// priority, the first one wins.
func NewRegistry(parsers ...Parser) *Registry {
	return &Registry{parsers: parsers}
}

// Parse reads raw with the parser of the highest priority that supports it.
// With none, the result is unknown and says so in a warning.
func (r *Registry) Parse(ctx context.Context, raw model.RawResponse, q model.NormalizedQuery) (*model.PartialResult, error) {
	var selected Parser
	for _, parser := range r.parsers {
		if !parser.Supports(raw) {
			continue
		}
		if selected == nil || parser.Priority() > selected.Priority() {
			selected = parser
		}
	}
	if selected == nil {
		return &model.PartialResult{
			Source:   raw.Source,
			Status:   model.StatusUnknown,
			Warnings: []string{"no parser available for raw response"},
		}, nil
	}
	return selected.Parse(ctx, raw, q)
}
