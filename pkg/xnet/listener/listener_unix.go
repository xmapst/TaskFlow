//go:build !windows

package listener

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/xmapst/taskflow/pkg/xnet/sockets"
)

// newPlatformListener opens a Unix socket, creating its parent directory
// if necessary and assigning the process’s effective group to the socket.
func newPlatformListener(proto, addr string) (net.Listener, error) {
	switch proto {
	case "unix":
		err := os.MkdirAll(filepath.Dir(addr), os.ModePerm)
		if err != nil {
			return nil, err
		}
		return sockets.NewUnixSocket(addr, os.Getegid())
	default:
		return nil, fmt.Errorf("invalid protocol format: %q", proto)
	}
}
