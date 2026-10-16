package i18n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"golang.org/x/text/language"
)

// Meta is the "_meta" section at the top of a locale file.
type Meta struct {
	// Name is the language's name in itself, e.g. "Deutsch".
	Name string `json:"name"`
	// Dir is the text direction, "ltr" or "rtl".
	Dir string `json:"dir"`
	// Completeness is the share of the base locale's keys this file
	// translates, in percent. "go run ./tools/i18n fix" keeps it current.
	Completeness int `json:"completeness"`
}

// File is a locale file: messages nested by key segment, plus Meta.
type File struct {
	Tag      language.Tag
	Meta     Meta
	Messages map[Key]string // by full dotted key
}

const metaKey = "_meta"

// ReadDir reads every *.json locale file at the root of fsys.
func ReadDir(fsys fs.FS) ([]*File, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	files := make([]*File, 0, len(names))
	var errs []error
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		f, err := ReadFile(name, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		files = append(files, f)
	}
	return files, errors.Join(errs...)
}

// ReadFile parses the locale file called name, such as "zh-CN.json". Keys
// may not repeat, every leaf must be a string and every object must have
// at least one entry.
func ReadFile(name string, data []byte) (*File, error) {
	base := strings.TrimSuffix(path.Base(name), ".json")
	tag, err := language.Parse(base)
	if err != nil || tag.String() != base {
		return nil, fmt.Errorf("%s: the file name must be a canonical BCP 47 tag such as zh-CN.json", name)
	}
	f := &File{Tag: tag, Messages: map[Key]string{}}
	r := &fileReader{dec: json.NewDecoder(bytes.NewReader(data)), file: f}
	if err := r.read(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return f, nil
}

type fileReader struct {
	dec  *json.Decoder
	file *File
}

func (r *fileReader) delim(want json.Delim) error {
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("%q expected", want)
	}
	return nil
}

func (r *fileReader) read() error {
	if err := r.delim('{'); err != nil {
		return err
	}
	seen := map[string]bool{}
	for r.dec.More() {
		key, err := r.key(seen, "")
		if err != nil {
			return err
		}
		if key == metaKey {
			if err := r.meta(); err != nil {
				return err
			}
			continue
		}
		if err := r.node(key); err != nil {
			return err
		}
	}
	if err := r.delim('}'); err != nil {
		return err
	}
	if _, err := r.dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the top-level object")
	}
	if !seen[metaKey] {
		return errors.New(`"_meta" is missing`)
	}
	return nil
}

func (r *fileReader) key(seen map[string]bool, prefix string) (string, error) {
	tok, err := r.dec.Token()
	if err != nil {
		return "", err
	}
	key, _ := tok.(string)
	if seen[key] {
		return "", fmt.Errorf("key %s%s appears twice", prefix, key)
	}
	seen[key] = true
	return key, nil
}

func (r *fileReader) meta() error {
	var raw json.RawMessage
	if err := r.dec.Decode(&raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r.file.Meta); err != nil {
		return fmt.Errorf("_meta: %w", err)
	}
	return nil
}

func (r *fileReader) node(key string) error {
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	switch tok := tok.(type) {
	case string:
		r.file.Messages[Key(key)] = tok
		return nil
	case json.Delim:
		if tok != '{' {
			break
		}
		seen := map[string]bool{}
		for r.dec.More() {
			sub, err := r.key(seen, key+".")
			if err != nil {
				return err
			}
			if err := r.node(key + "." + sub); err != nil {
				return err
			}
		}
		if len(seen) == 0 {
			return fmt.Errorf("%s is an empty object", key)
		}
		return r.delim('}')
	}
	return fmt.Errorf("%s must be a string or an object", key)
}

// Encode returns f in canonical form: keys nested by segment and sorted,
// "_meta" first, two-space indents and a final newline.
func (f *File) Encode() ([]byte, error) {
	root := map[string]any{}
	for key, msg := range f.Messages {
		segs := strings.Split(string(key), ".")
		node := root
		for i, seg := range segs {
			if i == len(segs)-1 {
				if _, taken := node[seg]; taken {
					return nil, fmt.Errorf("%s is both a message and a group", key)
				}
				node[seg] = msg
				break
			}
			child, ok := node[seg].(map[string]any)
			if !ok {
				if _, taken := node[seg]; taken {
					return nil, fmt.Errorf("%s is both a message and a group", strings.Join(segs[:i+1], "."))
				}
				child = map[string]any{}
				node[seg] = child
			}
			node = child
		}
	}
	// "_meta" sorts before every key, which starts with a lowercase letter.
	root[metaKey] = f.Meta
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
