// Package listener opens TCP, Unix socket, and Windows named pipe listeners.
package listener

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/pires/go-proxyproto"

	"github.com/xmapst/taskflow/pkg/xnet/sockets"
)

// New opens a listener for proto and addr. TCP listeners enable keepalive and
// parse PROXY protocol headers, but only from peers listed in trustedProxies
// (single IPs or CIDRs). Local transports are platform-specific.
//
// The PROXY header is optional for every peer, so direct clients, HTTP
// reverse proxies that only set X-Forwarded-For, and L4 load balancers that
// prepend a PROXY header can all share one port:
//
//   - peer in trustedProxies: a PROXY header, if present, replaces the
//     connection's RemoteAddr with the address it declares; without one the
//     peer's own address is kept.
//   - any other peer: a PROXY header is read and discarded, so RemoteAddr
//     always stays the real TCP peer and cannot be forged by the client.
//
// With an empty trustedProxies no peer is trusted and PROXY headers are never
// honoured.
func New(ctx context.Context, proto, addr string, trustedProxies []string) (net.Listener, error) {
	switch proto {
	case "tcp":
		policy, err := proxyPolicy(trustedProxies)
		if err != nil {
			return nil, err
		}
		ln, err := sockets.NewTCPSocket(ctx, addr)
		if err != nil {
			return nil, err
		}
		return &proxyproto.Listener{
			Listener: &Keepalive{
				Listener: ln,
			},
			ConnPolicy: policy,
		}, nil
	default:
		return newPlatformListener(proto, addr)
	}
}

// proxyPolicy trusts PROXY headers only from the given peers. Blank entries are
// skipped so the list can be shared with the HTTP-level trusted proxy setting.
// An invalid entry is an error rather than being dropped, which would silently
// narrow the trust set.
func proxyPolicy(trustedProxies []string) (proxyproto.ConnPolicyFunc, error) {
	trusted := make([]string, 0, len(trustedProxies))
	for _, entry := range trustedProxies {
		if entry = strings.TrimSpace(entry); entry != "" {
			trusted = append(trusted, entry)
		}
	}
	policy, err := proxyproto.PolicyFromRanges(trusted, proxyproto.USE, proxyproto.IGNORE)
	if err != nil {
		return nil, fmt.Errorf("invalid trusted_proxies: %w", err)
	}
	return policy, nil
}
