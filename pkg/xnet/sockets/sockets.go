// Package sockets 提供跨平台的 Socket 创建和配置工具，
// 支持 TCP、Unix Domain Socket 和内存 Socket 三种类型，
// 并针对不同操作系统实现端口复用等底层 Socket 配置。
package sockets

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// defaultTimeout 是 TCP 拨号连接的默认超时时间。
// 10 秒的设计平衡了正常连接的等待时间与异常场景下的响应速度：
// 太短可能导致高延迟网络的连接失败，太长则在服务不可达时阻塞过久。
const defaultTimeout = 10 * time.Second

// ErrProtocolNotAvailable indicates that this build does not support the requested transport.
var ErrProtocolNotAvailable = errors.New("protocol not available")

// ConfigureTransport 根据传输协议类型配置 HTTP 客户端的底层传输层（Transport）。
// 不同协议的配置差异：
//   - "unix"：通过 Unix Socket 通信，禁用压缩（Unix Socket 已有足够低的延迟，压缩反而增加 CPU 开销）
//   - "npipe"：通过 Windows 命名管道通信，同样禁用压缩
//   - 其他（如 "tcp"）：启用压缩，通过环境变量代理，使用 10 秒拨号超时
//
// 注意：若调用方需要手动控制压缩，应在调用本函数之后修改 tr.DisableCompression，
// 因为本函数会覆盖该设置。
func ConfigureTransport(tr *http.Transport, proto, addr string) error {
	switch proto {
	case "unix":
		return configureUnixTransport(tr, proto, addr)
	case "npipe":
		return configureNpipeTransport(tr, proto, addr)
	default:
		// 标准 TCP 传输：支持代理（honor HTTP_PROXY/HTTPS_PROXY 环境变量），启用压缩
		tr.Proxy = http.ProxyFromEnvironment
		tr.DisableCompression = false
		tr.DialContext = (&net.Dialer{
			Timeout: defaultTimeout,
		}).DialContext
	}
	return nil
}
