// Package web serves the web UI that "pnpm build" leaves in web/dist and
// that the Go build embeds.
//
// Files are sent precompressed when the client accepts it, hashed assets
// are cached for good, and any path that is not a file gets index.html so
// the UI's router can handle it. index.html gets a <base href> with the
// base path, which makes one build work at the root and under a sub-path.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"html"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// ErrNotBuilt means the binary was built without the web UI.
var ErrNotBuilt = errors.New("the web UI is not built into this binary: run pnpm -C web build before go build")

// Handler serves the embedded UI under base, "" for the root.
func Handler(base string) (http.Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return NewHandler(sub, base)
}

// encodings are the precompressed variants, best first, by file suffix.
var encodings = []struct{ name, suffix string }{{"br", ".br"}, {"gzip", ".gz"}}

// types are fixed here so the result does not depend on the host's MIME
// database (the Windows registry has been known to call .js text/plain).
var types = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".woff2":       "font/woff2",
}

type variant struct {
	data []byte
	etag string
}

type file struct {
	ctype    string
	variants map[string]variant // by Content-Encoding, "" for none
}

type handler struct {
	base  string
	index *file
	files map[string]*file // by path below the base
}

var baseTag = regexp.MustCompile(`<base href="[^"]*"\s*/?>`)

// NewHandler serves the UI in fsys under base.
func NewHandler(fsys fs.FS, base string) (http.Handler, error) {
	h := &handler{base: strings.TrimRight(base, "/"), files: map[string]*file{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path.Base(p), ".") {
			return err
		}
		enc, name := "", p
		for _, e := range encodings {
			if strings.HasSuffix(p, e.suffix) {
				enc, name = e.name, strings.TrimSuffix(p, e.suffix)
			}
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		f := h.files[name]
		if f == nil {
			ctype, ok := types[path.Ext(name)]
			if !ok {
				ctype = mime.TypeByExtension(path.Ext(name))
			}
			f = &file{ctype: ctype, variants: map[string]variant{}}
			h.files[name] = f
		}
		f.variants[enc] = variant{data: data}
		return nil
	})
	if err != nil {
		return nil, err
	}
	index, ok := h.files["index.html"]
	if !ok {
		return nil, ErrNotBuilt
	}
	// The rewritten page replaces the build's; precompressed copies of it
	// would still hold the old base.
	page := baseTag.ReplaceAll(index.variants[""].data, []byte(`<base href="`+html.EscapeString(h.base+"/")+`">`))
	index.variants = map[string]variant{"": {data: page}}
	for name, f := range h.files {
		if _, ok := f.variants[""]; !ok {
			return nil, errors.New("web: " + name + " has precompressed copies but no original")
		}
		for enc, v := range f.variants {
			sum := sha256.Sum256(v.data)
			v.etag = `"` + base64.RawURLEncoding.EncodeToString(sum[:12]) + `"`
			f.variants[enc] = v
		}
	}
	h.index = index
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	p, ok := strings.CutPrefix(r.URL.Path, h.base+"/")
	if !ok {
		if r.URL.Path == h.base {
			http.Redirect(w, r, h.base+"/", http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
		return
	}
	// The security headers, the Content-Security-Policy among them, are the
	// server's, for every answer (internal/kernel/httpserver).
	hdr := w.Header()
	f, found := h.files[p]
	switch {
	case found && p != "index.html":
		if strings.HasPrefix(p, "assets/") {
			hdr.Set("Cache-Control", "public, max-age=31536000, immutable") // names carry a content hash
		} else {
			hdr.Set("Cache-Control", "no-cache")
		}
	case path.Ext(p) == "" || p == "index.html":
		f = h.index // a route of the UI
		hdr.Set("Cache-Control", "no-cache")
	default:
		http.NotFound(w, r)
		return
	}

	v := f.variants[""]
	for _, e := range encodings {
		if alt, ok := f.variants[e.name]; ok && accepts(r.Header.Get("Accept-Encoding"), e.name) {
			hdr.Set("Content-Encoding", e.name)
			v = alt
			break
		}
	}
	hdr.Set("Vary", "Accept-Encoding")
	hdr.Set("ETag", v.etag)
	if f.ctype != "" {
		hdr.Set("Content-Type", f.ctype)
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, v.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Length", strconv.Itoa(len(v.data)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(v.data)
}

// accepts reports whether an Accept-Encoding header allows enc: listed
// with a q-value above zero.
func accepts(header, enc string) bool {
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), enc) {
			continue
		}
		q := 1.0
		for param := range strings.SplitSeq(params, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(param), "q="); ok {
				if parsed, err := strconv.ParseFloat(v, 64); err == nil {
					q = parsed
				}
			}
		}
		return q > 0
	}
	return false
}
