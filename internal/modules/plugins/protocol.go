package plugins

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// protocol is the version of the plugin protocol that JEC speaks: the
// requests carry it, and the answers have to.
const protocol = 1

// Actions: what a request asks a plugin to do.
const (
	actionDescribe   = "describe"
	actionTest       = "test"
	actionDeploy     = "deploy"
	actionNotify     = "notify"
	actionDNSPresent = "dns_present"
	actionDNSCleanUp = "dns_cleanup"
	// A DNS solver that adds records of any type gets these too.
	actionDNSAddRecord    = "dns_add_record"
	actionDNSRemoveRecord = "dns_remove_record"
	actionFilePut         = "file_put"
	actionFileRemove      = "file_remove"
)

// request is what JEC writes on a plugin's standard input.
type request struct {
	Protocol int    `json:"protocol"`
	Action   string `json:"action"`
	// Type is the plugin's own ID of the type, as it described it.
	Type        string            `json:"type,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Secrets     map[string]string `json:"secrets,omitempty"`
	Certificate *certificate      `json:"certificate,omitempty"`
	Previous    []string          `json:"previous,omitempty"`
	Note        *contracts.Note   `json:"note,omitempty"`
	Record      *record           `json:"record,omitempty"`
	DNSRecord   *dnsRecord        `json:"dns_record,omitempty"`
	// Ref is what dns_add_record answered, for dns_remove_record.
	Ref  string `json:"ref,omitempty"`
	File *file  `json:"file,omitempty"`
}

// certificate is the version a deployment installs, as webhooks get it.
type certificate struct {
	ID          int64     `json:"id"`
	Domains     []string  `json:"domains"`
	Serial      string    `json:"serial"`
	Fingerprint string    `json:"fingerprint"` // SHA-256 of the leaf, in hex
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Cert        string    `json:"cert"`
	Chain       string    `json:"chain"`
	Fullchain   string    `json:"fullchain"`
	Privkey     string    `json:"privkey"`
}

// record is the TXT record of a DNS-01 challenge.
type record struct {
	Domain string `json:"domain"`
	FQDN   string `json:"fqdn"`
	Value  string `json:"value"`
}

// dnsRecord is a record of any type, as a vendor's validation asks for:
// its full name, without a final dot, its type, TXT or CNAME, and its
// value.
type dnsRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// file is a file that a domain's web server serves.
type file struct {
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// answer is what every answer of a plugin holds.
type answer struct {
	Protocol int  `json:"protocol"`
	OK       bool `json:"ok"`
	// Error says what failed, when OK is false; Retry asks JEC to try again
	// later, after RetryAfter seconds when that is set.
	Error      string    `json:"error"`
	Retry      bool      `json:"retry"`
	RetryAfter int       `json:"retry_after"`
	Log        []logLine `json:"log"`
	// Message is what a test found.
	Message string `json:"message"`
	// Ref and Removed are what a deployment did, as contracts.DeployResult.
	Ref     string   `json:"ref"`
	Removed []string `json:"removed"`
}

// logLine is a line a plugin adds to the log of an issuance or deployment.
type logLine struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// description is the answer to describe.
type description struct {
	answer
	Name        text       `json:"name"`
	Description text       `json:"description"`
	Types       []typeSpec `json:"types"`
}

// typeSpec is a provider type a plugin offers.
type typeSpec struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Challenge   string `json:"challenge"`
	Name        text   `json:"name"`
	Description text   `json:"description"`
	Testable    bool   `json:"testable"`
	// Records marks a DNS solver that adds records of any type too.
	Records bool        `json:"records"`
	Fields  []fieldSpec `json:"fields"`
	// Wait is how long a DNS record may take to be seen, and how often to
	// look, in seconds.
	Wait *struct {
		Timeout  int `json:"timeout"`
		Interval int `json:"interval"`
	} `json:"wait"`
}

// fieldSpec is a setting of a type, as providers.Field.
type fieldSpec struct {
	Key      string          `json:"key"`
	Kind     string          `json:"kind"`
	Label    text            `json:"label"`
	Help     text            `json:"help"`
	Required bool            `json:"required"`
	Secret   bool            `json:"secret"`
	Advanced bool            `json:"advanced"`
	Default  string          `json:"default"`
	Options  []optionSpec    `json:"options"`
	When     *providers.When `json:"when"`
}

// optionSpec is a choice of a select field.
type optionSpec struct {
	Value string `json:"value"`
	Label text   `json:"label"`
}

// text is a text of a plugin: one string for every language, or strings
// by language tag, as {"en-US": "Name", "zh-CN": "..."}. The string for
// every language is under "".
type text map[string]string

var errText = errors.New("a text is a string, or an object of strings by language tag")

func (t *text) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = text{"": s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return errText
	}
	*t = m
	return nil
}

// empty reports whether t has no text.
func (t text) empty() bool {
	for _, s := range t {
		if strings.TrimSpace(s) != "" {
			return false
		}
	}
	return true
}

// in returns the text for language tag: its own, then that of the same
// language, the one for every language, the English one, or the first by
// tag.
func (t text) in(tag language.Tag) string {
	base, _ := tag.Base()
	tags := make([]string, 0, len(t))
	for k, s := range t {
		if s != "" {
			tags = append(tags, k)
		}
	}
	slices.Sort(tags)
	var same, english string
	for _, k := range tags {
		parsed, err := language.Parse(k)
		if k == "" || err != nil {
			continue
		}
		if parsed == tag {
			return t[k]
		}
		b, _ := parsed.Base()
		switch {
		case b == base && same == "":
			same = t[k]
		case b.String() == "en" && english == "":
			english = t[k]
		}
	}
	switch {
	case same != "":
		return same
	case t[""] != "":
		return t[""]
	case english != "":
		return english
	case len(tags) > 0:
		return t[tags[0]]
	}
	return ""
}
