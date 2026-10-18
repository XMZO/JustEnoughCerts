package aliyun

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// OSS signs its requests with its own version 4 (OSS4-HMAC-SHA256), apart
// from the other services, as its documentation spells out: a canonical
// request of the method, the resource, the query, the headers JEC sets
// and the unsigned payload; a key derived from the secret by date, region
// and service.

// unsignedPayload is the hash of a payload left out of the signature.
const unsignedPayload = "UNSIGNED-PAYLOAD"

// signOSS signs req for the resource (/bucket/ or /bucket/object) in
// region, at now, with the AccessKey.
func signOSS(req *http.Request, resource, region, id, secret string, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	day := stamp[:8]
	req.Header.Set("x-oss-date", stamp)
	req.Header.Set("x-oss-content-sha256", unsignedPayload)
	scope := day + "/" + region + "/oss/aliyun_v4_request"
	sum := sha256.Sum256([]byte(canonicalOSS(req, resource, nil)))
	toSign := "OSS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := hmacSHA256([]byte("aliyun_v4"+secret), day)
	for _, part := range []string{region, "oss", "aliyun_v4_request"} {
		key = hmacSHA256(key, part)
	}
	req.Header.Set("Authorization", fmt.Sprintf("OSS4-HMAC-SHA256 Credential=%s/%s, Signature=%s", id, scope, hex.EncodeToString(hmacSHA256(key, toSign))))
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// canonicalOSS is the canonical request of req for resource: the method,
// the escaped resource, the sorted query, the signed headers (Content-Type,
// Content-MD5, those of OSS and the additional ones, in lower case), the
// additional headers' names, and the payload's hash.
func canonicalOSS(req *http.Request, resource string, additional []string) string {
	var query []string
	for name, values := range req.URL.Query() {
		for _, v := range values {
			if v == "" {
				query = append(query, escapeOSS(name, false))
			} else {
				query = append(query, escapeOSS(name, false)+"="+escapeOSS(v, false))
			}
		}
	}
	slices.Sort(query)
	var headers []string
	for name := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-oss-") || lower == "content-type" || lower == "content-md5" || slices.Contains(additional, lower) {
			headers = append(headers, lower+":"+strings.TrimSpace(req.Header.Get(name))+"\n")
		}
	}
	slices.Sort(headers)
	additional = slices.Sorted(slices.Values(additional))
	return strings.Join([]string{
		req.Method, escapeOSS(resource, true), strings.Join(query, "&"), strings.Join(headers, ""), strings.Join(additional, ";"),
		req.Header.Get("x-oss-content-sha256"),
	}, "\n")
}

// escapeOSS escapes s as the canonical request has it: all but the
// unreserved characters, and slashes in a path.
func escapeOSS(s string, path bool) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '.', c == '_', c == '~', c == '/' && path:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ossQuery makes the raw query of OSS's sub-resources, such as cname and
// comp=add, which have no value or one.
func ossQuery(params map[string]string) string {
	var parts []string
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range params {
			if !yield(k) {
				return
			}
		}
	}) {
		if params[k] == "" {
			parts = append(parts, url.QueryEscape(k))
		} else {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(params[k]))
		}
	}
	return strings.Join(parts, "&")
}
