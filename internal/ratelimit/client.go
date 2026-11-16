// Package ratelimit works out who a request is from and limits repeated
// failures per client address (security design §3). Nothing here is keyed by
// account, so nobody can lock a user out by guessing at their name.
package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrusted parses the JL_TRUSTED_PROXIES list: comma- or space-separated
// CIDRs such as 10.0.0.0/8. A bare address stands for just that host.
func ParseTrusted(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		p, err := netip.ParsePrefix(item)
		if err != nil {
			a, aerr := netip.ParseAddr(item)
			if aerr != nil || a.Zone() != "" {
				return nil, fmt.Errorf("JL_TRUSTED_PROXIES must be a list of CIDRs such as 10.0.0.0/8 or 192.168.1.5/32, %q is not one", item)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if a := p.Addr(); a.Is4In6() {
			// An IPv4-mapped prefix is the IPv4 one: ::ffff:10.0.0.0/104 is 10.0.0.0/8.
			if p.Bits() < 96 {
				return nil, fmt.Errorf("JL_TRUSTED_PROXIES: %q is too wide to be an IPv4 range", item)
			}
			p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// Clients finds a request's client address.
type Clients struct {
	trusted []netip.Prefix
}

// NewClients returns a resolver that trusts X-Forwarded-For only from peers
// inside trusted. With none, every request is from its own connection.
func NewClients(trusted []netip.Prefix) *Clients { return &Clients{trusted: trusted} }

func (c *Clients) isTrusted(a netip.Addr) bool {
	for _, p := range c.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Addr is the client's address. It is the connection's own, unless that is a
// trusted proxy: then it is the right-most X-Forwarded-For entry that isn't
// itself a trusted proxy. A missing or unparseable header falls back to the
// connection. No other header is read.
func (c *Clients) Addr(r *http.Request) netip.Addr {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if len(c.trusted) == 0 || !c.isTrusted(peer) {
		return peer
	}
	var entries []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		entries = append(entries, strings.Split(v, ",")...)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		a, ok := parseAddr(strings.TrimSpace(entries[i]))
		if !ok {
			return peer
		}
		if !c.isTrusted(a) {
			return a
		}
	}
	return peer
}

// Key is what limits are counted by: an IPv4 address as it is, an IPv6 one by
// its /64, since one host commonly controls a whole /64. "" means the address
// could not be read.
func (c *Clients) Key(r *http.Request) string { return KeyOf(c.Addr(r)) }

// KeyOf is the limiter key for an address.
func KeyOf(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	if a.Is4() {
		return a.String()
	}
	p, _ := a.Prefix(64)
	return p.String()
}

// parseAddr reads an address with or without a port, without a zone, with an
// IPv4-mapped IPv6 address read as the IPv4 one.
func parseAddr(s string) (netip.Addr, bool) {
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.WithZone("").Unmap(), true
}
