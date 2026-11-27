package middleware

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/gaissmai/bart"
)

// IPAllowlist is an immutable set of client networks allowed to reach the API.
// A nil *IPAllowlist allows everyone.
type IPAllowlist struct {
	table   *bart.Lite
	trusted *bart.Lite // proxies whose forwarded headers are believed; may be empty
}

// forwardedHeaders are single-value headers set by proxies/CDNs, in the order
// they are consulted. X-Forwarded-For (a list) is handled separately last.
var forwardedHeaders = []string{"CF-Connecting-IP", "True-Client-IP", "X-Real-IP", "X-Client-IP"}

func parseEntries(name string, entries []string) (*bart.Lite, int, error) {
	table := new(bart.Lite)
	n := 0
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		pfx, err := parseAllowEntry(entry)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid %s entry %q: %w", name, entry, err)
		}
		table.Insert(pfx)
		n++
	}
	return table, n, nil
}

// NewIPAllowlist parses allow entries and trusted proxy entries, each either a
// single IP ("10.0.0.1", "::1") or a CIDR ("192.168.0.0/16"). Blank entries
// are skipped. No allow entries yields a nil allowlist, which disables
// filtering.
//
// Forwarded headers (X-Forwarded-For, X-Real-IP, CF-Connecting-IP,
// True-Client-IP, X-Client-IP) are only honoured when the direct peer is in
// trusted; otherwise any client could spoof them to bypass the allowlist.
func NewIPAllowlist(entries, trusted []string) (*IPAllowlist, error) {
	table, n, err := parseEntries("allow_ips", entries)
	if err != nil {
		return nil, err
	}
	tr, _, err := parseEntries("trusted_proxies", trusted)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return &IPAllowlist{table: table, trusted: tr}, nil
}

func parseAllowEntry(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		pfx, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		// Normalize IPv4-mapped IPv6 prefixes (::ffff:a.b.c.d/n) to plain IPv4
		// so they match the unmapped client address.
		if pfx.Addr().Is4In6() && pfx.Bits() >= 96 {
			return netip.PrefixFrom(pfx.Addr().Unmap(), pfx.Bits()-96).Masked(), nil
		}
		return pfx.Masked(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap().WithZone("")
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func (a *IPAllowlist) parseIP(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.Trim(s, "[]")
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

// clientIP returns the address to authorize. It starts from the direct peer;
// if that peer is a trusted proxy it is replaced by the client named in the
// forwarded headers. ok is false when the peer is not an IP endpoint (Unix
// socket, Windows named pipe).
func (a *IPAllowlist) clientIP(r *http.Request) (addr netip.Addr, ok bool) {
	peer, ok := a.parseIP(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !a.trusted.Lookup(peer) {
		return peer, true
	}
	for _, h := range forwardedHeaders {
		if v := r.Header.Get(h); v != "" {
			if ip, ok := a.parseIP(v); ok {
				return ip, true
			}
			// A trusted proxy sent garbage: fail closed rather than fall back
			// to the proxy's own (likely allowed) address.
			return netip.Addr{}, true
		}
	}
	// X-Forwarded-For: walk from the right, skipping further trusted proxies;
	// the first untrusted hop is the real client.
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i, hop := range slices.Backward(hops) {
		ip, ok := a.parseIP(hop)
		if !ok {
			return netip.Addr{}, true
		}
		if !a.trusted.Lookup(ip) {
			return ip, true
		}
		if i == 0 {
			return ip, true
		}
	}
	return peer, true
}

// Allows reports whether the request's client address is permitted.
//
// Peers that are not IP endpoints (Unix sockets, Windows named pipes) carry no
// IP; those channels are protected by filesystem or pipe permissions and are
// always allowed. An unparseable forwarded address from a trusted proxy is
// denied.
func (a *IPAllowlist) Allows(r *http.Request) bool {
	if a == nil {
		return true
	}
	ip, ok := a.clientIP(r)
	if !ok {
		return true
	}
	return ip.IsValid() && a.table.Lookup(ip)
}

// Middleware rejects requests from addresses outside the allowlist with 403.
func (a *IPAllowlist) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Allows(r) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "client ip not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Log records the active allowlist at startup.
func (a *IPAllowlist) Log() {
	if a == nil {
		return
	}
	var list []string
	for pfx := range a.table.AllSorted() {
		list = append(list, pfx.String())
	}
	var trusted []string
	for pfx := range a.trusted.AllSorted() {
		trusted = append(trusted, pfx.String())
	}
	slog.Info("ip allowlist enabled", slog.Any("allow_ips", list), slog.Any("trusted_proxies", trusted))
}
