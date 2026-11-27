//go:build windows

package sockets

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
)

// configureUnixTransport 返回 ErrProtocolNotAvailable，因为本项目在 Windows 上使用命名管道作为本地 IPC 传输。
func configureUnixTransport(tr *http.Transport, proto, addr string) error {
	return ErrProtocolNotAvailable
}

// configureNpipeTransport 配置 HTTP Transport 通过 Windows 命名管道通信。
// 禁用压缩的原因同 Unix Socket：本地 IPC 无需压缩，CPU 开销大于收益。
// 使用 winio.DialPipeContext 实现支持 Context 取消的命名管道连接，
// 覆盖 DialContext 将所有 HTTP 请求路由到命名管道。
func configureNpipeTransport(tr *http.Transport, proto, addr string) error {
	// 本地命名管道通信禁用压缩
	tr.DisableCompression = true
	tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, addr)
	}
	return nil
}

// DialPipe 连接到 Windows 命名管道，timeout 为连接超时时间。
// 使用 go-winio 库实现与 Windows API 的低层交互。
func DialPipe(addr string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(addr, &timeout)
}

// DefaultPipePath 返回 Windows 命名管道的标准路径格式（\\.\pipe\NAME），
// 与 Linux 的 DefaultPipePath（Unix Socket）提供对称的 API，实现跨平台抽象。
func DefaultPipePath(name string) string {
	return fmt.Sprintf("npipe:////./pipe/%s", name)
}
