//go:build !windows

package sockets

import (
	"net"
	"os"
	"syscall"
)

// SockOption 是 Unix Socket 创建时的选项函数类型，用于配置 Socket 文件的属主和权限。
// 遵循函数式选项模式，允许调用方按需组合权限配置。
type SockOption func(string) error

// WithChown 返回一个设置 Socket 文件所有者（uid 和 gid）的选项函数。
// 通过指定 GID 可以限制只有特定用户组的成员才能访问 Socket，
// 结合 660 权限（WithChmod）实现精细的进程间通信访问控制。
func WithChown(uid, gid int) SockOption {
	return func(path string) error {
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
		return nil
	}
}

// WithChmod 返回一个设置 Socket 文件访问权限的选项函数。
// 常用权限值：
//   - 0o660：仅所有者和所属组可读写（配合 WithChown 精确控制访问）
//   - 0o666：所有用户可读写（仅在无安全要求的临时环境使用）
func WithChmod(mask os.FileMode) SockOption {
	return func(path string) error {
		if err := os.Chmod(path, mask); err != nil {
			return err
		}
		return nil
	}
}

// NewUnixSocketWithOpts 创建具有指定权限选项的 Unix Domain Socket 监听器。
// 关键实现细节——临时修改 umask 以规避竞态条件：
//
// 问题背景：net.Listen("unix", path) 调用后到应用 WithChmod() 权限之前，
// 存在短暂的时间窗口，期间 Socket 文件的权限由系统 umask 决定（通常为 0022，
// 即权限为 644），允许任意用户读取，存在安全风险。
//
// 解决方案：将 umask 临时设置为 0777，迫使 Socket 以 0000 权限（无任何访问权限）创建，
// 然后通过 WithChmod() 显式设置期望的权限，消除不安全的时间窗口。
// 注意：umask 修改会影响同进程内其他 goroutine 在此期间的文件创建，
// 代码中未使用 defer 以尽快恢复 umask，降低副作用持续时间。
func NewUnixSocketWithOpts(path string, opts ...SockOption) (net.Listener, error) {
	// 若文件已存在，先删除旧的 Socket 文件（否则 bind 会失败）
	// os.IsNotExist 允许文件不存在的情况，保证幂等性
	if err := syscall.Unlink(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// 临时设置 umask 为 0777，使 Socket 以最严格权限（0000）创建，
	// 消除创建 Socket 与应用 chmod 之间的安全时间窗口
	origUmask := syscall.Umask(0o777)
	l, err := net.Listen("unix", path)
	// 尽快恢复原始 umask，减少对其他并发文件操作的影响
	syscall.Umask(origUmask)
	if err != nil {
		return nil, err
	}

	// 依次应用所有选项（如 chown、chmod），任一失败则关闭并返回错误
	for _, op := range opts {
		if err = op(path); err != nil {
			_ = l.Close()
			return nil, err
		}
	}

	return l, nil
}

// NewUnixSocket 创建 Unix Socket 监听器，使用 root:gid 所有权。
func NewUnixSocket(path string, gid int) (net.Listener, error) {
	return NewUnixSocketWithOpts(path, WithChown(0, gid), WithChmod(os.ModeSocket|os.ModePerm))
}
