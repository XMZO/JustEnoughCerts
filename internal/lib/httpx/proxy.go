package httpx

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// ProxyKey is the key of the setting ProxyField makes.
const ProxyKey = "proxy"

// ProxyField is the setting of a proxy (PLAN.md 11), for the types of
// providers that reach a service on the internet to add to theirs: for
// networks that cannot reach it directly, as from mainland China.
func ProxyField() providers.Field {
	return providers.Field{Key: ProxyKey, Label: i18n.HttpxFieldProxy, Help: i18n.HttpxFieldProxyHelp, Kind: providers.Text, Advanced: true}
}

// ParseProxy reads the URL of an HTTP, HTTPS or SOCKS5 proxy, such as
// http://127.0.0.1:7890.
func ParseProxy(v string) (*url.URL, error) {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
		return nil, ErrProxy
	}
	return u, nil
}

// ClientFor is Client with the proxy that config sets, as ProxyField names
// it. A client through a proxy is not PublicOnly, as the proxy is the one
// that dials. A proxy that is none fails for good, with a Problem.
func ClientFor(o Options, config map[string]string) (*http.Client, error) {
	if p := strings.TrimSpace(config[ProxyKey]); p != "" {
		o.Proxy, o.PublicOnly = p, false
	}
	c, err := Client(o)
	if errors.Is(err, ErrProxy) {
		return nil, ProxyProblem(err)
	}
	return c, err
}

// ProxyProblem is the error of a proxy that is no URL of one.
func ProxyProblem(err error) error {
	return retry.Permanent(&contracts.Problem{Key: i18n.HttpxErrorProxy, Err: err})
}
