//go:build !windows

package sockets

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// control 在 Socket 创建时设置端口复用选项，作为 net.ListenConfig.Control 回调使用。
// 同时设置 SO_REUSEADDR 和 SO_REUSEPORT 的目的：
//   - SO_REUSEADDR：允许绑定处于 TIME_WAIT 状态的端口，防止服务重启时因端口占用而失败
//   - SO_REUSEPORT：允许多个 Socket 绑定到同一端口，支持多进程/多 goroutine 负载均衡监听
//
// 通过 syscall.RawConn.Control 在 Socket 的文件描述符上直接调用 syscall，
// 绕过 Go 标准库的高层封装，以便在 Socket 创建阶段（bind 之前）完成配置。
func control(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		// SO_REUSEADDR：服务重启时可立即重用处于 TIME_WAIT 的端口，避免 "address already in use" 错误
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
			return
		}
		// SO_REUSEPORT：允许多个监听器绑定同一端口，结合负载均衡可提升多核并发吞吐量
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
			return
		}
	})
}
