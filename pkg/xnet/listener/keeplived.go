package listener

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// Keepalive configures TCP keepalive on accepted connections.
type Keepalive struct {
	Listener net.Listener
}

// Accept enables TCP keepalive with a 10-second period and sets an absolute
// read deadline 120 seconds after acceptance. Reads do not extend the deadline.
func (kln *Keepalive) Accept() (net.Conn, error) {
	c, err := kln.Listener.Accept()
	if err != nil {
		return nil, err
	}

	kac, err := kln.createKeepaliveConn(c)
	if err != nil {
		// createKeepaliveConn 失败时 kac 未生成，c 本身仍是打开的连接，必须显式关闭，
		// 否则调用方拿到的是 (nil, err)，无法访问该连接来释放底层 fd
		_ = c.Close()
		return nil, fmt.Errorf("create keepalive connection failed, %w", err)
	}
	if err = kac.SetKeepAlive(true); err != nil {
		_ = kac.Close()
		return nil, fmt.Errorf("SetKeepAlive failed, %w", err)
	}
	if err = kac.SetKeepAlivePeriod(10 * time.Second); err != nil {
		_ = kac.Close()
		return nil, fmt.Errorf("SetKeepAlivePeriod failed, %w", err)
	}
	if err = kac.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
		_ = kac.Close()
		return nil, fmt.Errorf("SetReadDeadline failed, %w", err)
	}
	return kac, nil
}

// Close 关闭底层监听器，拒绝后续的连接请求。
// 底层监听器为 nil 时不执行任何操作。
func (kln *Keepalive) Close() error {
	if kln.Listener == nil {
		return nil
	}
	return kln.Listener.Close()
}

// Addr 返回监听器绑定的网络地址。
// 对 nil 监听器的调用安全返回 nil，防止调用方在未初始化状态下 panic。
func (kln *Keepalive) Addr() net.Addr {
	if kln.Listener == nil {
		return nil
	}
	return kln.Listener.Addr()
}

// createKeepaliveConn 将通用 net.Conn 转换为 *net.TCPConn 以访问 TCP 专有的 Keepalive API。
// 若连接不是 TCP 类型（如 Unix Socket），直接返回错误，因为 Keepalive 是 TCP 专属特性。
func (kln *Keepalive) createKeepaliveConn(c net.Conn) (*keepAliveConn, error) {
	tcpc, ok := c.(*net.TCPConn)
	if !ok {
		return nil, errors.New("only tcp connections have keepalive")
	}
	return &keepAliveConn{
		tcpc,
	}, nil
}

// keepAliveConn 是 *net.TCPConn 的轻量包装，提供访问 Keepalive 配置方法的接口。
// 通过嵌入 *net.TCPConn 继承所有 TCP 连接方法，同时可在必要时添加额外行为。
type keepAliveConn struct {
	*net.TCPConn
}

// SetKeepAlive 开启或关闭 TCP Keepalive 探测。
func (l *keepAliveConn) SetKeepAlive(doKeepAlive bool) error {
	return l.TCPConn.SetKeepAlive(doKeepAlive)
}

// SetKeepAlivePeriod 设置 Keepalive 探测的时间间隔。
// 该值的具体含义取决于操作系统。
func (l *keepAliveConn) SetKeepAlivePeriod(d time.Duration) error {
	return l.TCPConn.SetKeepAlivePeriod(d)
}
