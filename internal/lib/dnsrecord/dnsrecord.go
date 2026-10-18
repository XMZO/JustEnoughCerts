// Package dnsrecord adds and removes DNS records of any type through the
// APIs of DNS providers, for validations that are not ACME's (PLAN.md
// 5.3.2 ②): a vendor's TXT, a CA's CNAME. lego's providers only add the
// TXT record of an ACME challenge, so the providers here are JEC's own,
// on the SDKs lego brings along; they take a provider's settings by lego's
// names of them, so that one configured instance serves both. Package
// dnscheck tells when a record shows on the authoritative servers of its
// zone.
package dnsrecord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/XMZO/JustEnoughCerts/internal/lib/dnscheck"
)

// Record is a record to add, at its full name, without a final dot.
type Record struct {
	Name  string
	Type  string // TXT or CNAME
	Value string
}

// API adds and removes records through a DNS provider.
type API interface {
	// Add adds r and returns what Remove needs to remove it.
	Add(ctx context.Context, r Record) (id string, err error)
	Remove(ctx context.Context, r Record, id string) error
}

// Codes are the codes of lego's providers JEC adds records of any type
// through.
var Codes = []string{"alidns", "cloudflare", "huaweicloud", "tencentcloud"}

// ErrUnsupported is the error of Open for a provider JEC adds no records
// through, or settings it cannot use, as an ECS role of Alibaba Cloud.
var ErrUnsupported = errors.New("dnsrecord: no records of any type through this provider")

// Open returns the API of lego's provider code, with its settings by
// lego's names of them, such as TENCENTCLOUD_SECRET_ID.
func Open(code string, settings map[string]string) (API, error) {
	switch code {
	case "tencentcloud":
		return openTencent(settings)
	case "alidns":
		return openAliyun(settings)
	case "cloudflare":
		return openCloudflare(settings)
	case "huaweicloud":
		return openHuawei(settings)
	}
	return nil, ErrUnsupported
}

// findZone returns the zone name belongs to, as its SOA says, without a
// final dot; tests replace it.
var findZone = dnscheck.Zone

// split returns the zone of name and the name within it: "@" for the zone
// itself.
func split(ctx context.Context, name string) (zone, sub string, err error) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if zone, err = findZone(ctx, name); err != nil {
		return "", "", err
	}
	if name == zone {
		return zone, "@", nil
	}
	sub, ok := strings.CutSuffix(name, "."+zone)
	if !ok {
		return "", "", fmt.Errorf("dnsrecord: %s is not in its zone %s", name, zone)
	}
	return zone, sub, nil
}

// ttl reads a TTL setting of a day at most, or gives def.
func ttl(settings map[string]string, key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(settings[key])); err == nil && n > 0 && n <= 86400 {
		return n
	}
	return def
}

// first returns the first of keys set in settings.
func first(settings map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(settings[k]); v != "" {
			return v
		}
	}
	return ""
}

// valid checks that r is a record JEC adds.
func valid(r Record) error {
	if r.Name == "" || r.Value == "" || !slices.Contains([]string{"TXT", "CNAME"}, r.Type) {
		return fmt.Errorf("dnsrecord: no %q record at %q", r.Type, r.Name)
	}
	return nil
}
