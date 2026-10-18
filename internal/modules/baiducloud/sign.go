package baiducloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// expireSeconds is how long a signature holds.
const expireSeconds = 1800

// sign signs req, whose body is body, with the key pair at now, as the
// BCE API asks (bce-auth-v1): over the method, the path, the query and the
// headers host, content-type, content-length and those of x-bce-, which it
// sets. The SDK's signer does the same, but logs the signature.
func sign(req *http.Request, ak, sk string, body []byte, now time.Time) {
	date := now.UTC().Format("2006-01-02T15:04:05Z")
	req.Header.Set("X-Bce-Date", date)
	headers := map[string]string{
		"host":           req.URL.Host,
		"content-length": strconv.Itoa(len(body)),
		"content-type":   req.Header.Get("Content-Type"),
	}
	for k, v := range req.Header {
		if k := strings.ToLower(k); strings.HasPrefix(k, "x-bce-") && k != "x-bce-request-id" && len(v) > 0 {
			headers[k] = v[0]
		}
	}
	var canonical, signed []string
	for k, v := range headers {
		if v = strings.TrimSpace(v); v != "" {
			canonical = append(canonical, encode(k, true)+":"+encode(v, true))
			signed = append(signed, k)
		}
	}
	slices.Sort(canonical)
	slices.Sort(signed)
	prefix := fmt.Sprintf("bce-auth-v1/%s/%s/%d", ak, date, expireSeconds)
	request := strings.Join([]string{req.Method, "/" + encode(strings.TrimPrefix(req.URL.Path, "/"), false), query(req.URL.Query()), strings.Join(canonical, "\n")}, "\n")
	signature := hmacHex(hmacHex(sk, prefix), request)
	req.Header.Set("Authorization", prefix+"/"+strings.Join(signed, ";")+"/"+signature)
}

// query is the canonical query of values: each pair encoded, sorted.
func query(values url.Values) string {
	var out []string
	for k, vs := range values {
		if strings.EqualFold(k, "authorization") {
			continue
		}
		for _, v := range vs {
			out = append(out, encode(k, true)+"="+encode(v, true))
		}
	}
	slices.Sort(out)
	return strings.Join(out, "&")
}

// encode percent-encodes s but its unreserved characters, and the slashes
// unless slash is set.
func encode(s string, slash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~', c == '/' && !slash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hmacHex(key, data string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}
