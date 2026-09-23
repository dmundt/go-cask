package web

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// This file holds the viewer's caller-identity rule for deployments behind a
// reverse proxy (viewer-security §5.2). The throttle keys on a caller address,
// so a forwarded address may only be honoured when the direct peer is a proxy
// the operator configured as trusted: the header is otherwise attacker-supplied
// and would let a caller either evade the throttle or push another client into
// a block. The default configuration trusts nothing and uses the peer address
// alone, which is the pre-existing behavior.

// trustedProxy is the parsed form of Config.TrustedProxies.
type trustedProxy struct {
	nets []*net.IPNet
	ips  []net.IP
}

// isTrusted reports whether addr (a bare IP) is a configured trusted proxy.
// The receiver may be nil, which trusts nothing.
func (t *trustedProxy) isTrusted(addr string) bool {
	if t == nil {
		return false
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, entry := range t.ips {
		if entry.Equal(ip) {
			return true
		}
	}
	for _, network := range t.nets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// newTrustedProxy parses proxy addresses: a CIDR block ("10.0.0.0/8"), a
// single IP ("127.0.0.1" or "::1"), or an ip:port, whose port is ignored
// because the peer address the viewer compares is a bare IP. A value that is
// none of these is an error, so a mistyped flag fails viewer startup instead
// of silently trusting nothing — which would reintroduce the shared-bucket
// lockout the setting exists to prevent. An empty list trusts nothing.
func newTrustedProxy(proxies []string) (*trustedProxy, error) {
	trusted := &trustedProxy{}
	for _, entry := range proxies {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			trusted.nets = append(trusted.nets, network)
			continue
		}
		candidate := entry
		if host, _, err := net.SplitHostPort(entry); err == nil {
			candidate = host
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			return nil, fmt.Errorf("trusted proxy %q is neither an IP address nor a CIDR block", entry)
		}
		trusted.ips = append(trusted.ips, ip)
	}
	return trusted, nil
}

// callerIP returns the address the login throttle keys on: the direct peer
// address, unless that peer is a trusted proxy and the request carries a client
// address forwarded on its behalf.
func (s *Server) callerIP(r *http.Request) string {
	peer := peerIP(r)
	if !s.trusted.isTrusted(peer) {
		// The header is unauthenticated here: an untrusted caller cannot move
		// its own bucket and cannot move another client's (viewer-security §5.2).
		return peer
	}
	if client, ok := forwardedClient(r, s.trusted); ok {
		return client
	}
	// A trusted proxy that forwards no client address still speaks for itself.
	return peer
}

// peerIP is the direct peer's address: the TCP source address, with the port
// removed. A RemoteAddr the server cannot split is returned verbatim, which
// cannot parse as an IP, so it matches no trusted proxy and is only ever used
// as the caller's own bucket.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forwardedClient returns the client address a trusted peer forwarded, as
// "rightmost address the proxy did not vouch for".
//
// X-Forwarded-For is a hop list that each proxy appends to, so the last entry
// is the one the direct peer appended and the entries left of it were supplied
// by upstream hops. Walking right to left and stopping at the first address
// that is not a trusted proxy therefore yields the closest hop the trusted
// chain does not own: whatever a client prepended to the left of that hop is
// discarded, so a caller cannot pick its own throttle bucket by writing the
// header itself. When every entry is a trusted proxy the leftmost entry is the
// caller.
//
// A hop is taken exactly as the header presents it, with only surrounding
// whitespace removed. Anything that is not a bare IP therefore fails the
// trusted-proxy test above and becomes the caller value verbatim — the
// conservative outcome, since a distinct bucket only over-throttles the value
// that was presented, while normalizing text into an IP could hand a caller
// another client's bucket.
//
// The standardized Forwarded header (RFC 7239) supplies the address only when
// X-Forwarded-For is absent; its grammar is richer, so its first `for=` value
// is read as a node (quotes, brackets, and port removed).
func forwardedClient(r *http.Request, trusted *trustedProxy) (string, bool) {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(hops[i])
			if hop == "" {
				// The proxy appended nothing for this hop; keep looking left.
				continue
			}
			if !trusted.isTrusted(hop) {
				return hop, true
			}
			if i == 0 {
				return hop, true
			}
		}
		return "", false
	}
	if forwarded := r.Header.Get("Forwarded"); forwarded != "" {
		if client, ok := forwardedFor(forwarded); ok {
			return client, true
		}
	}
	return "", false
}

// forwardedFor extracts the first `for=` value of an RFC 7239 Forwarded header.
// The value is taken as presented, minus the quotes and brackets the grammar
// allows; anything it cannot read is no address at all.
func forwardedFor(header string) (string, bool) {
	for element := range strings.SplitSeq(header, ",") {
		for param := range strings.SplitSeq(element, ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "for") {
				continue
			}
			value = strings.TrimSpace(value)
			if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			}
			if value == "" {
				return "", false
			}
			return nodeAddress(value), true
		}
	}
	return "", false
}

// nodeAddress reduces an RFC 7239 node value to the address the throttle can
// key on: an IP literal, with the brackets and port the grammar allows
// removed. A value that is not an IP — an obfuscated identifier or anything
// else — is returned verbatim, so it becomes its own bucket.
func nodeAddress(value string) string {
	if net.ParseIP(value) != nil {
		return value
	}
	if strings.HasPrefix(value, "[") {
		if end := strings.Index(value, "]"); end > 0 {
			return value[1:end]
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}
