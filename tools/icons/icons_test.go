package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The icons in web/public are what the tool draws from the favicon now.
func TestIconsUpToDate(t *testing.T) {
	dir := filepath.Join("..", "..", dir)
	icons, err := draw(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range icons {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is not what the favicon draws (%v): run go run ./tools/icons", name, err)
		}
	}
}

func TestCells(t *testing.T) {
	grid, err := cells(`<svg viewBox="0 0 16 16"><path d="M1 1h3v3H1zM2 2h1v1h-1z"/></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	// A square with a hole, by the even-odd rule.
	if !grid[1][1] || grid[2][2] || !grid[3][3] || grid[0][0] {
		t.Errorf("grid %v", grid[:4])
	}
	for _, svg := range []string{
		`<svg viewBox="0 0 32 32"><path d="M1 1h3v3H1z"/></svg>`,
		`<svg viewBox="0 0 16 16"><path d="M1 1h3v3H1zc1 1 2 2"/></svg>`,
		`<svg viewBox="0 0 16 16"><path d="M1 1h3v3H2z"/></svg>`,
		`<svg viewBox="0 0 16 16"><path d="M14 1h3v3H14z"/></svg>`,
	} {
		if _, err := cells(svg); err == nil {
			t.Errorf("read %s", svg)
		}
	}
}
