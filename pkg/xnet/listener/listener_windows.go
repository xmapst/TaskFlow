//go:build windows

package listener

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// newPlatformListener opens a Windows named pipe for local IPC.
func newPlatformListener(proto, addr string) (net.Listener, error) {
	switch proto {
	case "npipe":
		// Allow full access to built-in administrators and Local System.
		sddl := "D:P(A;;GA;;;BA)(A;;GA;;;SY)"
		c := winio.PipeConfig{
			SecurityDescriptor: sddl,
			// 消息模式支持 CloseWrite()，允许客户端发送 EOF 而不关闭连接
			MessageMode:      true,
			InputBufferSize:  65536, // 64 KiB input buffer.
			OutputBufferSize: 65536, // 64 KiB output buffer.
		}
		return winio.ListenPipe(addr, &c)
	default:
		return nil, fmt.Errorf("invalid protocol format: %q", proto)
	}
}
