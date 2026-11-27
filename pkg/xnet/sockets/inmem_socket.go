package sockets

import (
	"errors"
	"net"
	"sync"
)

// errClosed 是内存 Socket 关闭后尝试操作时返回的错误，与标准库的描述保持一致。
var errClosed = errors.New("use of closed network connection")

// InmemSocket 是纯内存实现的 net.Listener，通过 channel 传递连接对象，
// 无需经过操作系统的网络协议栈，适用于单进程内的组件间通信（如进程内 RPC）。
// 相比 Unix Socket 和 TCP，内存 Socket 的优势是零系统调用开销和无资源泄漏风险。
type InmemSocket struct {
	chConn  chan net.Conn // 接受连接请求的缓冲通道
	chClose chan struct{} // 关闭信号通道，使用 close() 广播以允许多个 goroutine 同时感知
	addr    string
	mu      sync.Mutex // 保护 chClose 的幂等关闭操作
}

// dummyAddr 是内存 Socket 的虚拟地址类型，仅存储一个字符串，
// 用于满足 net.Listener.Addr() 返回 net.Addr 接口的要求，无实际网络含义。
type dummyAddr string

// NewInmemSocket 创建指定地址标识和连接缓冲容量的内存 Socket 监听器。
// addr 仅作为标识符，不代表真实地址；bufSize 控制 Accept() 之前可缓冲的连接数量，
// 建议根据预期并发连接数设置，防止 Dial() 在消费方未及时 Accept 时阻塞。
func NewInmemSocket(addr string, bufSize int) *InmemSocket {
	return &InmemSocket{
		chConn:  make(chan net.Conn, bufSize),
		chClose: make(chan struct{}),
		addr:    addr,
	}
}

// Addr 返回虚拟地址，满足 net.Listener 接口。
func (s *InmemSocket) Addr() net.Addr {
	return dummyAddr(s.addr)
}

// Accept 阻塞等待下一个连接或关闭信号。
// 通过 select 同时监听连接通道和关闭通道，使得 Close() 可以立即中断阻塞的 Accept() 调用，
// 避免 goroutine 泄漏。
func (s *InmemSocket) Accept() (net.Conn, error) {
	select {
	case conn := <-s.chConn:
		return conn, nil
	case <-s.chClose:
		return nil, errClosed
	}
}

// Close 关闭监听器，使所有阻塞的 Accept() 和 Dial() 调用立即返回 errClosed。
// 使用 select + default 实现幂等关闭：多次调用 Close() 不会 panic，
// 因为 close() 一个已关闭的 channel 会 panic，此处提前检测避免该问题。
func (s *InmemSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.chClose:
		// 已经关闭，幂等返回
	default:
		close(s.chClose) // 广播关闭信号给所有等待者
	}
	return nil
}

// Dial 建立一条内存连接，使用 net.Pipe() 创建全双工的内存管道，
// 服务端持有 srvConn（由 Accept() 返回），客户端持有 clientConn（由 Dial 返回）。
// 若监听器已关闭，Dial() 立即返回 errClosed，避免连接写入已关闭的通道。
func (s *InmemSocket) Dial(network, addr string) (net.Conn, error) {
	srvConn, clientConn := net.Pipe()
	select {
	case s.chConn <- srvConn: // 将服务端连接发送给等待中的 Accept()
	case <-s.chClose:
		return nil, errClosed
	}

	return clientConn, nil
}

// Network 实现 net.Addr 接口，返回地址标识符字符串。
func (a dummyAddr) Network() string {
	return string(a)
}

// String 实现 net.Addr 接口，返回地址标识符字符串。
func (a dummyAddr) String() string {
	return string(a)
}
