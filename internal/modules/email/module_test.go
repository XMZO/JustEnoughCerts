package email

import (
	"testing"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
)

func TestManifest(t *testing.T) {
	if _, err := module.NewSet(New()); err != nil {
		t.Fatal(err)
	}
}
