package cryptox

import (
	"bytes"
	"crypto/x509"
)

// Pairs are the bundles found among several files, as exported by another
// tool: each private key with the certificate it belongs to and its chain
// from all the certificates found, and the leaves no key belongs to.
type Pairs struct {
	Bundles []*Bundle
	Alone   []*x509.Certificate
}

// FindPairs reads files of PEM certificates and keys, or a single DER
// certificate each, in any mix, and pairs them. A file that cannot be read
// is told of by its index in bad, with the reason; the others still count.
func FindPairs(files [][]byte) (p Pairs, bad map[int]error) {
	var certs []*x509.Certificate
	var keys []*Bundle
	for i, data := range files {
		c, k, err := parsePEM(data)
		if err == nil && len(c) == 0 && len(k) == 0 && len(bytes.TrimSpace(data)) > 0 {
			var one *x509.Certificate
			if one, err = x509.ParseCertificate(data); err == nil {
				c = []*x509.Certificate{one}
			} else {
				err = ErrNoCertificate
			}
		}
		if err != nil {
			if bad == nil {
				bad = map[int]error{}
			}
			bad[i] = err
			continue
		}
		certs = append(certs, c...)
		for _, key := range k {
			keys = append(keys, &Bundle{Key: key})
		}
	}
	paired := map[*x509.Certificate]bool{}
	seen := map[string]bool{}
	for _, k := range keys {
		b, err := NewBundle(certs, k.Key)
		if err != nil || seen[Fingerprint(b.Leaf)] {
			continue // a key of no certificate here, or a copy
		}
		seen[Fingerprint(b.Leaf)] = true
		paired[b.Leaf] = true
		p.Bundles = append(p.Bundles, b)
	}
	for _, c := range certs {
		if !c.IsCA && !paired[c] && !seen[Fingerprint(c)] {
			seen[Fingerprint(c)] = true
			p.Alone = append(p.Alone, c)
		}
	}
	return p, bad
}
