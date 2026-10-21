// Package reach tells why a server could not be reached, from the error
// of a connection, for a word people understand rather than Go's.
package reach

import "strings"

// The causes Cause tells.
const (
	DNS     = "dns"     // the name resolves to no address
	Connect = "connect" // nothing takes the connection
	TLS     = "tls"     // a connection, but no TLS answer that would do
)

// Cause tells why err, the text of a connection's error, kept the server
// out of reach: DNS, Connect or, else, TLS.
func Cause(err string) string {
	switch {
	case strings.Contains(err, "no such host"), strings.Contains(err, "lookup ") && strings.Contains(err, "server misbehaving"):
		return DNS
	case strings.Contains(err, "connection refused"), strings.Contains(err, "i/o timeout"), strings.Contains(err, "no route to host"),
		strings.Contains(err, "network is unreachable"), strings.Contains(err, "connection reset"):
		return Connect
	}
	return TLS
}
