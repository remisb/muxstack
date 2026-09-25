package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPConfig configures ClientIP.
type ClientIPConfig struct {
	// TrustedProxies are the reverse proxies (load balancers, Caddy, nginx)
	// whose X-Forwarded-For header is believed. Empty trusts nobody: the
	// client is always the connection's peer. ParseTrustedProxies builds it
	// from strings such as "127.0.0.1" or "10.0.0.0/8".
	TrustedProxies []netip.Prefix
}

const clientIPKey contextKey = "clientIP"

// ClientIP returns a Middleware that works out the client's address once and
// stores it in the request context, where ClientAddr reads it. RateLimiter
// keys on it by default and Logger logs it, so put ClientIP before (outside)
// both in the chain.
//
// Behind a reverse proxy every connection comes from the proxy, so the peer
// address alone would make all clients one. X-Forwarded-For names the real
// client, but anyone can send that header, so it is read only when the peer is
// a trusted proxy, and walked right to left: the first address that is not a
// trusted proxy is the client. Entries left of it were written by the client
// and are ignored.
//
// r.RemoteAddr is left untouched: it is still the address of the connection.
//
//	middleware.Chain(mux,
//	    middleware.ClientIP(middleware.ClientIPConfig{TrustedProxies: proxies}),
//	    middleware.Logger(logger),
//	    middleware.RateLimiter(middleware.RateLimitConfig{RequestsPerInterval: 5, Interval: time.Minute}),
//	)
func ClientIP(cfg ClientIPConfig) Middleware {
	trusted := append([]netip.Prefix(nil), cfg.TrustedProxies...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if addr, ok := resolveClient(r, trusted); ok {
				r = r.WithContext(context.WithValue(r.Context(), clientIPKey, addr))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientAddr returns the client address ClientIP resolved, or, without
// ClientIP in the chain, the connection's peer address. Either way it is a bare
// IP ("192.0.2.1", "2001:db8::1") with no port or brackets. If RemoteAddr is
// not an address at all, it is returned trimmed.
func ClientAddr(r *http.Request) string {
	if addr, ok := r.Context().Value(clientIPKey).(netip.Addr); ok {
		return addr.String()
	}
	if addr, ok := parseAddr(r.RemoteAddr); ok {
		return addr.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// ParseTrustedProxies parses IP addresses and CIDR prefixes, such as the
// comma-separated value of a TRUSTED_PROXIES setting split into a slice.
func ParseTrustedProxies(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR prefix", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func resolveClient(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !isTrusted(peer, trusted) {
		return peer, true
	}
	hops := r.Header.Values("X-Forwarded-For")
	last := peer
	for i := len(hops) - 1; i >= 0; i-- {
		parts := strings.Split(hops[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			a, ok := parseAddr(parts[j])
			if !ok {
				// Trusted proxies write valid addresses; stop at the last
				// hop that can be vouched for.
				return last, true
			}
			if !isTrusted(a, trusted) {
				return a, true
			}
			last = a
		}
	}
	return last, true
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseAddr accepts "ip", "ip:port" and "[ipv6]:port", and unmaps
// IPv4-in-IPv6 so 127.0.0.1 and ::ffff:127.0.0.1 are the same client.
func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}
