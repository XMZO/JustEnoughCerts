// Package useragent names the browser and system of a User-Agent header,
// as "Chrome 141 · Windows", for lists of sign-ins and their notes. It
// knows the common ones only; others come out as the header's first word.
package useragent

import (
	"regexp"
	"strings"
)

// browsers are tried in order: Edge and Opera say Chrome too, Chrome says
// Safari.
var browsers = []struct {
	name string
	re   *regexp.Regexp
}{
	{"Edge", regexp.MustCompile(`Edg(?:e|A|iOS)?/(\d+)`)},
	{"Opera", regexp.MustCompile(`OPR/(\d+)`)},
	{"Samsung Internet", regexp.MustCompile(`SamsungBrowser/(\d+)`)},
	{"Firefox", regexp.MustCompile(`(?:Firefox|FxiOS)/(\d+)`)},
	{"Chrome", regexp.MustCompile(`(?:Chrome|CriOS)/(\d+)`)},
	{"Safari", regexp.MustCompile(`Version/(\d+)[.\d]* (?:Mobile/\S+ )?Safari/`)},
	{"curl", regexp.MustCompile(`^curl/(\d+)`)},
}

// systems are tried in order: iOS and Android say other names too.
var systems = []struct{ name, mark string }{
	{"iOS", "iPhone"},
	{"iPadOS", "iPad"},
	{"Android", "Android"},
	{"Windows", "Windows"},
	{"macOS", "Macintosh"},
	{"ChromeOS", "CrOS"},
	{"Linux", "Linux"},
}

// Describe names the browser, with its major version, and the system of
// ua; "" for an empty one.
func Describe(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	var parts []string
	for _, b := range browsers {
		if m := b.re.FindStringSubmatch(ua); m != nil {
			parts = append(parts, b.name+" "+m[1])
			break
		}
	}
	for _, s := range systems {
		if strings.Contains(ua, s.mark) {
			parts = append(parts, s.name)
			break
		}
	}
	if len(parts) == 0 {
		first, _, _ := strings.Cut(ua, " ")
		if len(first) > 40 {
			first = first[:40]
		}
		return first
	}
	return strings.Join(parts, " · ")
}
