package lookup

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// zoneReply is how a fake name server answers.
type zoneReply struct {
	// existing, when set, is the only name the zone knows; the others get
	// NXDOMAIN.
	existing    string
	rcode       dnsmessage.RCode
	nsOwner     string
	nameservers []string
	soaOwner    string
}

// fakeDelegation has the name servers of zones answer replies; their
// addresses are made up and named after them.
func fakeDelegation(t *testing.T, zones map[string][]string, replies map[string]zoneReply) delegation {
	t.Helper()
	var mu sync.Mutex
	hostOf := map[string]string{}
	next := 1
	return delegation{
		lookupNS: func(_ context.Context, name string) ([]*net.NS, error) {
			hosts, ok := zones[strings.TrimSuffix(name, ".")]
			if !ok {
				return nil, errors.New("no such host")
			}
			var out []*net.NS
			for _, h := range hosts {
				out = append(out, &net.NS{Host: h + "."})
			}
			return out, nil
		},
		lookupHost: func(_ context.Context, host string) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			addr := net.IPv4(192, 0, 2, byte(next)).String()
			next++
			hostOf[addr] = host
			return []string{addr}, nil
		},
		exchange: func(_ context.Context, server string, query []byte) ([]byte, error) {
			addr, _, _ := net.SplitHostPort(server)
			mu.Lock()
			host := hostOf[addr]
			mu.Unlock()
			reply, ok := replies[host]
			if !ok {
				return nil, errors.New("timeout")
			}
			return buildReply(t, query, reply), nil
		},
	}
}

func buildReply(t *testing.T, query []byte, reply zoneReply) []byte {
	t.Helper()
	var q dnsmessage.Message
	if err := q.Unpack(query); err != nil {
		t.Fatal(err)
	}
	if q.RecursionDesired {
		t.Fatal("asked for recursion")
	}
	if reply.existing != "" && len(q.Questions) == 1 && q.Questions[0].Name.String() != reply.existing+"." {
		reply = zoneReply{rcode: dnsmessage.RCodeNameError, soaOwner: reply.soaOwner}
	}
	msg := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RCode: reply.rcode}, Questions: q.Questions}
	for _, ns := range reply.nameservers {
		msg.Authorities = append(msg.Authorities, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(reply.nsOwner + "."), Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET, TTL: 3600},
			Body:   &dnsmessage.NSResource{NS: dnsmessage.MustNewName(ns + ".")},
		})
	}
	if reply.soaOwner != "" {
		msg.Authorities = append(msg.Authorities, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(reply.soaOwner + "."), Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET, TTL: 1200},
			Body: &dnsmessage.SOAResource{
				NS:   dnsmessage.MustNewName("ns1.nic." + reply.soaOwner + "."),
				MBox: dnsmessage.MustNewName("hostmaster.nic." + reply.soaOwner + "."),
			},
		})
	}
	body, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDelegation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		zones   map[string][]string
		replies map[string]zoneReply
		domain  string
		status  model.ResultStatus
		zone    string
		ns      []string
	}{
		{
			name:    "delegated: registered",
			zones:   map[string][]string{"uy": {"a.nic.uy"}},
			replies: map[string]zoneReply{"a.nic.uy": {nsOwner: "ka.uy", nameservers: []string{"sloan.ns.cloudflare.com", "jeff.ns.cloudflare.com"}}},
			domain:  "ka.uy", status: model.StatusRegistered, zone: "uy", ns: []string{"jeff.ns.cloudflare.com", "sloan.ns.cloudflare.com"},
		},
		{
			name:    "NXDOMAIN: not registered",
			zones:   map[string][]string{"al": {"nsx.nic.al"}},
			replies: map[string]zoneReply{"nsx.nic.al": {rcode: dnsmessage.RCodeNameError, soaOwner: "al"}},
			domain:  "free-name.al", status: model.StatusUnregistered, zone: "al",
		},
		{
			// com.uy has no name servers of its own as a resolver sees it,
			// so the check starts at uy and is referred down.
			name:  "a referral to a second-level zone",
			zones: map[string][]string{"uy": {"a.nic.uy"}},
			replies: map[string]zoneReply{
				"a.nic.uy":  {nsOwner: "com.uy", nameservers: []string{"ns.com.uy"}},
				"ns.com.uy": {nsOwner: "google.com.uy", nameservers: []string{"ns1.google.com"}},
			},
			domain: "google.com.uy", status: model.StatusRegistered, zone: "com.uy", ns: []string{"ns1.google.com"},
		},
		{
			name:    "a name with no delegation: unknown",
			zones:   map[string][]string{"al": {"nsx.nic.al"}},
			replies: map[string]zoneReply{"nsx.nic.al": {existing: "gov.al", soaOwner: "al"}},
			domain:  "gov.al", status: model.StatusUnknown, zone: "al",
		},
		{
			name:    "another name server when one does not answer",
			zones:   map[string][]string{"uy": {"dead.nic.uy", "a.nic.uy"}},
			replies: map[string]zoneReply{"a.nic.uy": {nsOwner: "ka.uy", nameservers: []string{"ns1.example.net"}}},
			domain:  "ka.uy", status: model.StatusRegistered, zone: "uy", ns: []string{"ns1.example.net"},
		},
		{
			// Every name gets NOERROR with only the SOA, as in a wildcard zone.
			name:    "a wildcard zone without the delegation: not registered",
			zones:   map[string][]string{"ph": {"1.ns.ph"}},
			replies: map[string]zoneReply{"1.ns.ph": {soaOwner: "com.ph"}},
			domain:  "free-name.com.ph", status: model.StatusUnregistered, zone: "ph",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fakeDelegation(t, tt.zones, tt.replies).check(t.Context(), tt.domain)
			if err != nil {
				t.Fatal(err)
			}
			if got.status != tt.status || got.zone != tt.zone || !slices.Equal(got.nameservers, tt.ns) {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestDelegationUnreachable(t *testing.T) {
	if _, err := fakeDelegation(t, map[string][]string{"uy": {"dead.nic.uy"}}, nil).check(t.Context(), "ka.uy"); err == nil || !strings.Contains(err.Error(), "dead.nic.uy: timeout") {
		t.Errorf("err = %v", err)
	}
}

// TestDelegationHedges: a server that never answers costs a moment, not
// the whole time of a query, in either order of the servers.
func TestDelegationHedges(t *testing.T) {
	d := fakeDelegation(t,
		map[string][]string{"es": {"silent.nic.es", "fast.nic.es"}},
		map[string]zoneReply{"fast.nic.es": {nsOwner: "google.es", nameservers: []string{"ns1.google.com"}}},
	)
	fast := d.exchange
	d.exchange = func(ctx context.Context, server string, query []byte) ([]byte, error) {
		body, err := fast(ctx, server, query)
		if err != nil {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return body, nil
	}
	for range 5 {
		start := time.Now()
		got, err := d.check(t.Context(), "google.es")
		if err != nil || got.status != model.StatusRegistered {
			t.Fatalf("got %+v, %v", got, err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("waited %s for a silent server", elapsed)
		}
	}
}
