//go:build !windows

package sockets

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// maxUnixSocketPathSize 是当前平台的 Unix Socket 地址结构中路径字段的长度。
const maxUnixSocketPathSize = len(syscall.RawSockaddrUnix{}.Path)

// configureUnixTransport 配置 HTTP Transport 通过 Unix Domain Socket 通信。
// 验证路径长度，防止超出内核限制（Linux 通常为 108 字节）导致 bind 失败。
// 禁用压缩是因为本地进程间通信延迟极低，压缩带来的 CPU 开销超过节省的带宽。
// 使用闭包固定 proto 和 addr，覆盖 DialContext 使所有 HTTP 请求走 Unix Socket。
func configureUnixTransport(tr *http.Transport, proto, addr string) error {
	if len(addr) > maxUnixSocketPathSize {
		return fmt.Errorf("unix socket path %q is too long", addr)
	}
	// 本地 IPC 通信禁用压缩，减少 CPU 开销
	tr.DisableCompression = true
	dialer := &net.Dialer{
		Timeout: defaultTimeout,
	}
	// 固定使用指定的 proto（"unix"）和 addr，覆盖 HTTP 客户端的默认拨号行为
	tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, proto, addr)
	}
	return nil
}

// configureNpipeTransport 在 Linux 上不支持 Windows 命名管道，直接返回协议不可用错误。
func configureNpipeTransport(tr *http.Transport, proto, addr string) error {
	return ErrProtocolNotAvailable
}

// DialPipe 在非 Windows 平台上连接指定路径的 Unix Socket。
func DialPipe(addr string, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout: timeout,
	}

	return dialer.Dial("unix", addr)
}

// DefaultPipePath 在 Linux 上返回标准的 Unix Socket 路径格式（/var/run/NAME.sock），
// 提供与 Windows 命名管道（npipe:////./pipe/NAME）对称的 API。
func DefaultPipePath(name string) string {
	return fmt.Sprintf("unix:///var/run/%s.sock", name)
}
