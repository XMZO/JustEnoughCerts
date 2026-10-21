package reach

import "testing"

func TestCause(t *testing.T) {
	for err, want := range map[string]string{
		"dial tcp: lookup nowhere.example on 127.0.0.11:53: no such host":  DNS,
		"dial tcp: lookup a.example: server misbehaving":                   DNS,
		"dial tcp 192.0.2.1:443: connect: connection refused":              Connect,
		"dial tcp 192.0.2.1:443: i/o timeout":                              Connect,
		"read tcp 192.0.2.1:443: read: connection reset by peer":           Connect,
		"tls: failed to verify certificate: x509: certificate has expired": TLS,
		"remote error: tls: internal error":                                TLS,
	} {
		if got := Cause(err); got != want {
			t.Errorf("%q: %s, want %s", err, got, want)
		}
	}
}
