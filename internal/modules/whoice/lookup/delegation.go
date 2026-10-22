package lookup

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/XMZO/JustEnoughCerts/internal/modules/whoice/model"
)

// The delegation of a name in its parent zone, for registries with no
// lookup that answers (.al publishes none, whois.nic.org.uy takes no
// connections): asked of the parent zone's own servers, without
// recursion, a delegation means the name is registered and NXDOMAIN that
// the registry has no such name. It tells no end; it follows Whoice.
const (
	maxServers      = 6
	nsLookupTimeout = 1500 * time.Millisecond
	hedgeDelay      = 250 * time.Millisecond
	maxReferrals    = 3
	perQueryTimeout = 2 * time.Second
)

// exchangeFunc sends one DNS message to server ("ip:53") and returns the
// answer.
type exchangeFunc func(ctx context.Context, server string, query []byte) ([]byte, error)

type delegation struct {
	lookupNS   func(ctx context.Context, name string) ([]*net.NS, error)
	lookupHost func(ctx context.Context, host string) ([]string, error)
	exchange   exchangeFunc
}

func newDelegation() delegation {
	return delegation{lookupNS: net.DefaultResolver.LookupNS, lookupHost: net.DefaultResolver.LookupHost, exchange: exchange}
}

// delegated is what the parent zone says of a name.
type delegated struct {
	zone        string
	server      string
	status      model.ResultStatus
	nameservers []string
	// wildcard is set when the "not registered" rests on the zone
	// answering every name the same way.
	wildcard bool
}

// check looks up the delegation of domain in its parent zone.
func (c delegation) check(ctx context.Context, domain string) (*delegated, error) {
	if !strings.Contains(domain, ".") {
		return nil, fmt.Errorf("the delegation of %q: no domain below a TLD", domain)
	}
	zone, hosts, err := c.parentZone(ctx, domain)
	if err != nil {
		return nil, err
	}
	for range maxReferrals + 1 {
		reply, server, err := c.ask(ctx, domain, hosts)
		if err != nil {
			return nil, fmt.Errorf("the name servers of %s: %w", zone, err)
		}
		result, next, nextHosts := interpret(domain, zone, server, reply)
		if result != nil {
			if result.status == model.StatusUnknown {
				c.resolveWildcard(ctx, result, domain, hosts)
			}
			return result, nil
		}
		// A referral to a zone between the parent and the domain (uy to
		// com.uy): follow it.
		zone, hosts = next, nextHosts
	}
	return nil, fmt.Errorf("the delegation of %s: referrals go on below %s", domain, zone)
}

// resolveWildcard tells a "name exists, no delegation" answer apart. A zone
// with a wildcard (*.com.ph points every name at a parking page) answers so
// for every name; if a random sibling gets the same answer, and since
// registered names are always delegated, no delegation means not
// registered.
func (c delegation) resolveWildcard(ctx context.Context, result *delegated, domain string, hosts []string) {
	parent := domain[strings.Index(domain, ".")+1:]
	probe := fmt.Sprintf("jec-probe-%08x.%s", rand.Uint32(), parent) //nolint:gosec // a name no one registered, not a secret
	reply, _, err := c.ask(ctx, probe, hosts)
	if err != nil || reply.RCode != dnsmessage.RCodeSuccess {
		return
	}
	if probed, _, _ := interpret(probe, result.zone, "", reply); probed == nil || probed.status != model.StatusUnknown {
		return
	}
	result.status, result.wildcard = model.StatusUnregistered, true
}

func (c delegation) parentZone(ctx context.Context, domain string) (string, []string, error) {
	labels := strings.Split(domain, ".")
	for i := 1; i < len(labels); i++ {
		zone := strings.Join(labels[i:], ".")
		records, err := c.lookupNS(ctx, zone+".")
		if err != nil || len(records) == 0 {
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			continue
		}
		var hosts []string
		for _, r := range records {
			if host := canonical(r.Host); host != "" {
				hosts = append(hosts, host)
			}
		}
		if len(hosts) > 0 {
			return zone, hosts, nil
		}
	}
	return "", nil, fmt.Errorf("no name servers of the parent zone of %s", domain)
}

// ask queries up to maxServers of the zone's name servers in random order
// and returns the first authoritative answer. Every server's address is
// looked up at once and they are asked as their addresses come, the next
// one if an answer takes more than hedgeDelay: some registries list
// servers that do not resolve or do not answer (.kh), or drop packets
// over IPv4 (a.nic.es), which must not use up the time of the good ones.
func (c delegation) ask(ctx context.Context, domain string, hosts []string) (*dnsmessage.Message, string, error) {
	hosts = slices.Clone(hosts)
	rand.Shuffle(len(hosts), func(i, j int) { hosts[i], hosts[j] = hosts[j], hosts[i] }) //nolint:gosec // spreads queries, no secret
	if len(hosts) > maxServers {
		hosts = hosts[:maxServers]
	}
	query, id, err := buildQuery(domain)
	if err != nil {
		return nil, "", err
	}
	type resolved struct {
		host  string
		addrs []string
		err   error
	}
	results := make(chan resolved, len(hosts))
	for _, host := range hosts {
		go func() {
			lookupCtx, cancel := context.WithTimeout(ctx, nsLookupTimeout)
			defer cancel()
			addrs, err := c.lookupHostRetry(lookupCtx, host)
			results <- resolved{host: host, addrs: addrs, err: err}
		}()
	}
	type answer struct {
		msg    *dnsmessage.Message
		server string
		err    error
	}
	hedgeCtx, cancelHedge := context.WithCancel(ctx)
	defer cancelHedge()
	answers := make(chan answer, len(hosts))
	var errs []string
	inFlight, resolvedLeft := 0, len(hosts)
	var pending []resolved
	send := func(item resolved) {
		inFlight++
		go func() {
			addr := preferIPv4(item.addrs)
			tryCtx, cancel := context.WithTimeout(hedgeCtx, perQueryTimeout)
			defer cancel()
			body, err := c.exchange(tryCtx, net.JoinHostPort(addr, "53"), query)
			if err != nil {
				answers <- answer{err: fmt.Errorf("%s: %w", item.host, err)}
				return
			}
			var msg dnsmessage.Message
			if err := msg.Unpack(body); err != nil || msg.ID != id {
				answers <- answer{err: fmt.Errorf("%s: malformed answer", item.host)}
				return
			}
			if msg.RCode != dnsmessage.RCodeSuccess && msg.RCode != dnsmessage.RCodeNameError {
				answers <- answer{err: fmt.Errorf("%s: %s", item.host, msg.RCode)}
				return
			}
			answers <- answer{msg: &msg, server: item.host}
		}()
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for inFlight > 0 || resolvedLeft > 0 || len(pending) > 0 {
		if inFlight == 0 && len(pending) > 0 {
			send(pending[0])
			pending = pending[1:]
			timer.Reset(hedgeDelay)
		}
		var resolvedCh chan resolved
		if resolvedLeft > 0 {
			resolvedCh = results
		}
		select {
		case item := <-resolvedCh:
			resolvedLeft--
			if item.err != nil || len(item.addrs) == 0 {
				errs = append(errs, item.host+": no address")
				continue
			}
			pending = append(pending, item)
		case got := <-answers:
			inFlight--
			if got.err == nil {
				return got.msg, got.server, nil
			}
			errs = append(errs, got.err.Error())
		case <-timer.C:
			if len(pending) > 0 {
				send(pending[0])
				pending = pending[1:]
				timer.Reset(hedgeDelay)
			}
		case <-ctx.Done():
			return nil, "", errors.New(strings.Join(append(errs, ctx.Err().Error()), "; "))
		}
	}
	if len(errs) == 0 {
		return nil, "", errors.New("no name servers to ask")
	}
	return nil, "", errors.New(strings.Join(errs, "; "))
}

// interpret turns an answer into a result, or into a referral to follow.
func interpret(domain, zone, server string, msg *dnsmessage.Message) (*delegated, string, []string) {
	var nameservers, referralHosts []string
	var referralZone string
	for _, section := range [][]dnsmessage.Resource{msg.Answers, msg.Authorities} {
		for _, rr := range section {
			ns, ok := rr.Body.(*dnsmessage.NSResource)
			if !ok {
				continue
			}
			owner, host := canonical(rr.Header.Name.String()), canonical(ns.NS.String())
			switch {
			case owner == domain:
				if !slices.Contains(nameservers, host) {
					nameservers = append(nameservers, host)
				}
			case strings.HasSuffix(domain, "."+owner) && strings.HasSuffix(owner, "."+zone):
				referralZone = owner
				if !slices.Contains(referralHosts, host) {
					referralHosts = append(referralHosts, host)
				}
			}
		}
	}
	result := &delegated{zone: zone, server: server, status: model.StatusUnknown}
	switch {
	case msg.RCode == dnsmessage.RCodeNameError:
		result.status = model.StatusUnregistered
	case len(nameservers) > 0:
		slices.Sort(nameservers)
		result.status, result.nameservers = model.StatusRegistered, nameservers
	case referralZone != "":
		return nil, referralZone, referralHosts
	}
	return result, "", nil
}

// lookupHostRetry looks up a name server's address, once more after a
// moment: under many lookups at once the system's resolver now and then
// fails.
func (c delegation) lookupHostRetry(ctx context.Context, host string) ([]string, error) {
	addrs, err := c.lookupHost(ctx, host)
	if err == nil && len(addrs) > 0 {
		return addrs, nil
	}
	select {
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.lookupHost(ctx, host)
}

func buildQuery(domain string) ([]byte, uint16, error) {
	name, err := dnsmessage.NewName(domain + ".")
	if err != nil {
		return nil, 0, err
	}
	id := uint16(rand.IntN(1 << 16)) //nolint:gosec // a message ID, not a secret
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET}},
	}
	body, err := msg.Pack()
	return body, id, err
}

// exchange sends query over UDP, and over TCP when the answer comes cut.
func exchange(ctx context.Context, server string, query []byte) ([]byte, error) {
	body, err := exchangeConn(ctx, "udp", server, query)
	if err != nil {
		return nil, err
	}
	var p dnsmessage.Parser
	if h, err := p.Start(body); err == nil && h.Truncated {
		return exchangeConn(ctx, "tcp", server, query)
	}
	return body, nil
}

func exchangeConn(ctx context.Context, network, server string, query []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if network == "udp" {
		if _, err := conn.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(query))) //nolint:gosec // a DNS query is far below 64 KiB
	if _, err := conn.Write(append(framed, query...)); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	body := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return body, nil
}

func preferIPv4(addrs []string) string {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a
		}
	}
	return addrs[0]
}

func canonical(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}
