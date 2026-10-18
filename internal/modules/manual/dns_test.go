package manual

import (
	"testing"
	"time"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

func TestSolver(t *testing.T) {
	for minutes, want := range map[string]time.Duration{
		"":      defaultMinutes * time.Minute,
		"5":     5 * time.Minute,
		"90":    maxMinutes * time.Minute,
		"0":     defaultMinutes * time.Minute,
		"never": defaultMinutes * time.Minute,
	} {
		p, err := factory(t.Context(), providers.Instance{Config: map[string]string{"minutes": minutes}})
		if err != nil {
			t.Fatal(err)
		}
		s := p.(contracts.Solver)
		if wait, every := s.Wait(); wait != want || every != interval {
			t.Errorf("%q: %v every %v", minutes, wait, every)
		}
		ch := contracts.Challenge{Domain: "example.com", Token: "t", KeyAuth: "t.k"}
		if s.Present(t.Context(), ch) != nil || s.CleanUp(t.Context(), ch) != nil || s.Close() != nil {
			t.Error("a solver that changes nothing failed")
		}
	}
}
