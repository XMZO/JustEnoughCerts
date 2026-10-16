package example

import (
	"testing"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// Tests may make up keys, for fixtures and assertions.
func TestFixture(t *testing.T) {
	if i18n.T("made.up.key") == "" {
		t.Fatal("empty")
	}
}
