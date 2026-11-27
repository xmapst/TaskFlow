//go:build windows

package sockets

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// control 在 Windows 上设置 SO_REUSEADDR Socket 选项，作为 net.ListenConfig.Control 回调使用。
// Windows 不支持 SO_REUSEPORT（Linux 特性），仅设置 SO_REUSEADDR 以允许服务重启时
// 立即重用处于 TIME_WAIT 状态的端口，避免 "bind: address already in use" 错误。
// 注意：Windows 的 SO_REUSEADDR 语义与 Linux 有所不同，
// 在 Windows 上它还允许多个进程绑定同一地址/端口（存在安全风险），
// 在生产环境中需结合其他访问控制手段使用。
func control(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		// 仅设置 SO_REUSEADDR，Windows 不支持 SO_REUSEPORT
		if err := windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1); err != nil {
			return
		}
	})
}
