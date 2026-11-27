package xnet

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Addr parses a transport URI and implements net.Addr and pflag.Value.
type Addr struct {
	network string // tcp, unix, or npipe
	host    string // Hostname, IP address, socket path, or pipe path.
	port    int    // TCP port; zero for path-based transports.
}

// pathBasedNetworks 是以路径（而非 host:port）标识地址的协议集合：
//   - unix：Unix Domain Socket，路径为文件系统绝对路径
//   - npipe：Windows 命名管道，路径形如 \\.\pipe\NAME
var pathBasedNetworks = map[string]bool{
	"unix":  true,
	"npipe": true,
}

// isPathBased 判断当前协议是否以路径（而非 host:port）标识地址
func (v *Addr) isPathBased() bool {
	return pathBasedNetworks[v.network]
}

// NewAddr returns an Addr initialized to tcp://0.0.0.0:8080.
func NewAddr() *Addr {
	return &Addr{
		network: "tcp",
		host:    "0.0.0.0",
		port:    8080,
	}
}

// String returns the transport URI.
func (v *Addr) String() string {
	if v.network == "" {
		return ""
	}
	if v.isPathBased() {
		return fmt.Sprintf("%s://%s", v.network, v.host)
	}
	if v.host == "" {
		return fmt.Sprintf("%s://:%d", v.network, v.port)
	}
	return fmt.Sprintf("%s://%s:%d", v.network, v.host, v.port)
}

// Set parses a TCP, Unix socket, or Windows named pipe URI.
func (v *Addr) Set(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid URI format: %w", err)
	}

	network := strings.ToLower(parsed.Scheme)
	switch network {
	case "tcp", "unix", "npipe":
		v.network = network
	default:
		return fmt.Errorf("unsupported network protocol %q", parsed.Scheme)
	}

	if v.isPathBased() {
		// unix 套接字路径、npipe 管道路径均落在 Path 中
		if parsed.Path == "" {
			return fmt.Errorf("missing %s path (e.g., '%s:///tmp/sys.sock')", v.network, v.network)
		}
		v.host = parsed.Path
		v.port = 0
		return nil
	}

	if parsed.Host == "" {
		return fmt.Errorf("missing host and port for %s protocol", v.network)
	}

	host, portStr, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return fmt.Errorf("must provide both host and port (e.g., '127.0.0.1:8080'): %w", err)
	}

	v.host = host

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("invalid port number %q (must be 0-65535)", portStr)
	}
	v.port = port

	return nil
}

// Type returns the type name displayed by pflag.
func (v *Addr) Type() string {
	return "uri"
}

// Address returns the address to pass to net.Listen or net.Dial.
func (v *Addr) Address() string {
	if v.isPathBased() {
		return v.host
	}
	return net.JoinHostPort(v.host, strconv.Itoa(v.port))
}

// Network returns the transport protocol.
func (v *Addr) Network() string {
	return v.network
}

func (v *Addr) Host() string {
	return v.host
}

func (v *Addr) Port() int {
	return v.port
}
