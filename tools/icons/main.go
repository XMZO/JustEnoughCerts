// Command icons draws the PNG icons of the web app out of its favicon, a
// padlock of square cells on a 16 by 16 grid, so that they keep its look
// to the pixel: those a browser shows as they are, the one a phone may
// crop to its own shape (maskable), and Apple's touch icon. Run it from
// the repository's root after changing web/public/favicon.svg; a test
// checks that the icons in web/public are what it draws.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// dir is where the favicon and the icons are, from the repository's root.
const dir = "web/public"

func main() {
	if err := run(dir); err != nil {
		fmt.Fprintln(os.Stderr, "icons:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	icons, err := draw(dir)
	if err != nil {
		return err
	}
	for name, data := range icons {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil { //nolint:gosec // G306: a file of the repository
			return err
		}
	}
	return nil
}

// An icon: its size, its background, nil for none, and the color and the
// size of the cells of the padlock.
type icon struct {
	size       int
	background color.Color
	fill       color.Color
	cell       int
}

// The colors: the favicon's green, and the brand's with white on it, as
// the design's brand and on-brand tokens are.
var (
	green = color.RGBA{0x3c, 0x9a, 0x3f, 0xff}
	brand = color.RGBA{0x2b, 0x7a, 0x33, 0xff}
	white = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// icons are the files drawn. The padlock of a maskable icon keeps to the
// circle in the middle that every crop shows, of 80% of the width.
var icons = map[string]icon{
	"icon-192.png":          {size: 192, fill: green, cell: 12},
	"icon-512.png":          {size: 512, fill: green, cell: 32},
	"icon-maskable-512.png": {size: 512, background: brand, fill: white, cell: 20},
	"apple-touch-icon.png":  {size: 180, background: brand, fill: white, cell: 8},
}

// draw returns the icons, by file name, drawn from the favicon in dir.
func draw(dir string) (map[string][]byte, error) {
	svg, err := os.ReadFile(filepath.Join(dir, "favicon.svg"))
	if err != nil {
		return nil, err
	}
	grid, err := cells(string(svg))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for name, ic := range icons {
		data, err := render(grid, ic)
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

// rect matches a rectangle of the favicon's path: M x y h w v h, then
// back to x by H x or h -w, z.
var rect = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v(\d+)(?:H(\d+)|h-(\d+))z`)

// cells reads which cells of the 16 by 16 grid the favicon fills: those
// inside an odd number of its rectangles, as its even-odd rule has it.
func cells(svg string) ([16][16]bool, error) {
	var grid [16][16]bool
	m := regexp.MustCompile(` d="([^"]+)"`).FindStringSubmatch(svg)
	if m == nil || !strings.Contains(svg, `viewBox="0 0 16 16"`) {
		return grid, errors.New("the favicon is no path on a 16 by 16 grid")
	}
	rects := rect.FindAllStringSubmatch(m[1], -1)
	if len(rects) == 0 || len(strings.Join(rect.FindAllString(m[1], -1), "")) != len(m[1]) {
		return grid, errors.New("the favicon's path is not made of rectangles alone")
	}
	for _, r := range rects {
		n := make([]int, 6)
		for i := range n {
			n[i], _ = strconv.Atoi(r[i+1])
		}
		x, y, w, h := n[0], n[1], n[2], n[3]
		back := r[5] != "" && n[4] == x || r[6] != "" && n[5] == w
		if !back || x+w > 16 || y+h > 16 {
			return grid, fmt.Errorf("the favicon's rectangle %s is out of the grid", r[0])
		}
		for cy := y; cy < y+h; cy++ {
			for cx := x; cx < x+w; cx++ {
				grid[cy][cx] = !grid[cy][cx]
			}
		}
	}
	return grid, nil
}

// render draws the filled cells of grid as ic says, the padlock in the
// middle, as a PNG.
func render(grid [16][16]bool, ic icon) ([]byte, error) {
	minX, minY, maxX, maxY := 16, 16, -1, -1
	for y := range 16 {
		for x := range 16 {
			if grid[y][x] {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		return nil, errors.New("the favicon is empty")
	}
	w, h := (maxX-minX+1)*ic.cell, (maxY-minY+1)*ic.cell
	if w > ic.size || h > ic.size {
		return nil, fmt.Errorf("the padlock does not fit an icon of %d", ic.size)
	}
	palette := color.Palette{color.Transparent, ic.fill}
	if ic.background != nil {
		palette[0] = ic.background
	}
	img := image.NewPaletted(image.Rect(0, 0, ic.size, ic.size), palette)
	left, top := (ic.size-w)/2, (ic.size-h)/2
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if !grid[y][x] {
				continue
			}
			for py := range ic.cell {
				for px := range ic.cell {
					img.SetColorIndex(left+(x-minX)*ic.cell+px, top+(y-minY)*ic.cell+py, 1)
				}
			}
		}
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
