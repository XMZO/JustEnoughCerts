package i18n

import (
	"strings"
	"testing"
)

const sampleFile = `{
  "_meta": {
    "name": "English",
    "dir": "ltr",
    "completeness": 100
  },
  "certs": {
    "list": {
      "empty": "No certificates yet",
      "title": "Certificates"
    }
  },
  "common": {
    "action": {
      "save": "Save <now>"
    }
  }
}
`

func TestReadFile(t *testing.T) {
	f, err := ReadFile("en-US.json", []byte(sampleFile))
	if err != nil {
		t.Fatal(err)
	}
	if f.Tag != BaseLocale || f.Meta != (Meta{Name: "English", Dir: "ltr", Completeness: 100}) {
		t.Errorf("Tag = %s, Meta = %+v", f.Tag, f.Meta)
	}
	want := map[Key]string{
		"certs.list.empty":   "No certificates yet",
		"certs.list.title":   "Certificates",
		"common.action.save": "Save <now>",
	}
	if len(f.Messages) != len(want) {
		t.Fatalf("Messages = %v", f.Messages)
	}
	for k, v := range want {
		if f.Messages[k] != v {
			t.Errorf("Messages[%s] = %q, want %q", k, f.Messages[k], v)
		}
	}

	// Encode is canonical: the sample is already in that form.
	out, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != sampleFile {
		t.Errorf("Encode() =\n%s\nwant\n%s", out, sampleFile)
	}
}

func TestReadFileErrors(t *testing.T) {
	meta := `"_meta": {"name": "x", "dir": "ltr", "completeness": 100}`
	tests := map[string]struct{ name, data string }{
		"duplicate top-level key": {"en-US.json", `{` + meta + `, "a": {"b": {"c": "x"}}, "a": {"d": {"e": "y"}}}`},
		"duplicate nested key":    {"en-US.json", `{` + meta + `, "a": {"b": {"c": "x", "c": "y"}}}`},
		"duplicate meta":          {"en-US.json", `{` + meta + `, ` + meta + `}`},
		"number leaf":             {"en-US.json", `{` + meta + `, "a": {"b": {"c": 1}}}`},
		"array leaf":              {"en-US.json", `{` + meta + `, "a": {"b": {"c": ["x"]}}}`},
		"empty object":            {"en-US.json", `{` + meta + `, "a": {}}`},
		"missing meta":            {"en-US.json", `{"a": {"b": {"c": "x"}}}`},
		"unknown meta field":      {"en-US.json", `{"_meta": {"name": "x", "dir": "ltr", "completeness": 1, "extra": 1}}`},
		"trailing data":           {"en-US.json", `{` + meta + `} {}`},
		"not an object":           {"en-US.json", `["x"]`},
		"lowercase region":        {"zh-cn.json", `{` + meta + `}`},
		"not a tag":               {"english.json", `{` + meta + `}`},
	}
	for name, tt := range tests {
		if _, err := ReadFile(tt.name, []byte(tt.data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestEncodeConflict(t *testing.T) {
	for _, keys := range [][]Key{{"a.b", "a.b.c"}, {"a.b.c", "a.b"}} {
		f := &File{Tag: BaseLocale, Messages: map[Key]string{}}
		for _, k := range keys {
			f.Messages[k] = "x"
		}
		if _, err := f.Encode(); err == nil || !strings.Contains(err.Error(), "both a message and a group") {
			t.Errorf("Encode(%v) = %v, want a conflict error", keys, err)
		}
	}
}
