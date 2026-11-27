package sockets

import (
	"context"
	"net"
)

// NewTCPSocket 创建一个配置了端口复用选项的 TCP 监听器。
// 使用 net.ListenConfig.Control 回调在 Socket 创建阶段设置 SO_REUSEADDR 和 SO_REUSEPORT
// （Windows 仅 SO_REUSEADDR），允许服务重启时立即绑定同一端口，提升服务可用性。
// 注意：本函数不处理 TLS，如需加密传输需由调用方自行用 tls.NewListener 包装返回的监听器。
func NewTCPSocket(ctx context.Context, addr string) (net.Listener, error) {
	lc := &net.ListenConfig{
		// 注入 control 函数，在 bind() 之前配置 Socket 选项
		Control: control,
	}
	l, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return l, nil
}
