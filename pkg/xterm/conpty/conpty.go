//go:build windows

package conpty

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	updateProcThreadAttribute = kernel32.NewProc("UpdateProcThreadAttribute")
	ErrConPtyUnsupported      = errors.New("ConPty is not available on this version of Windows")
)

// IsConPtyAvailable reports whether this Windows build exports every API ConPTY
// needs. The system DLL does not change while the process runs, so the probe
// happens once.
var IsConPtyAvailable = sync.OnceValue(func() bool {
	for _, name := range []string{"CreatePseudoConsole", "ResizePseudoConsole", "ClosePseudoConsole", "InitializeProcThreadAttributeList", "UpdateProcThreadAttribute"} {
		if kernel32.NewProc(name).Find() != nil {
			return false
		}
	}
	return true
})

// Terminal owns the pseudo console and its input/output pipes. It accepts one
// successful process start. Always call Close, even after the process exits.
type Terminal struct {
	mu            sync.Mutex
	console       windows.Handle
	input, output *os.File
	size          windows.Coord
	started       bool
	closed        bool
	finishOnce    sync.Once
	closeOnce     sync.Once
	closeErr      error
}

// Close cancels pending input and drains the final console output before closing
// the host reader. On older Windows builds ClosePseudoConsole flushes synchronously.
func (t *Terminal) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()

		inputErr := t.input.Close()
		outputErr := closeConsoleOutput(t.output, t.finish)
		t.closeErr = errors.Join(inputErr, outputErr)
	})
	return t.closeErr
}

func (t *Terminal) finish() {
	t.finishOnce.Do(func() {
		t.mu.Lock()
		console := t.console
		t.console = 0
		t.mu.Unlock()
		windows.ClosePseudoConsole(console)
	})
}

func closeConsoleOutput(output *os.File, finish func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, output)
	}()
	finish()
	err := output.Close()
	<-done
	return err
}

func (t *Terminal) Resize(width, height int16) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.console == 0 {
		return os.ErrClosed
	}
	if width <= 0 || height <= 0 {
		return os.ErrInvalid
	}
	size := windows.Coord{X: width, Y: height}
	if err := windows.ResizePseudoConsole(t.console, size); err != nil {
		return err
	}
	t.size = size
	return nil
}

func (t *Terminal) Size() (width, height int16, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.console == 0 {
		return 0, 0, os.ErrClosed
	}
	return t.size.X, t.size.Y, nil
}

func (t *Terminal) SetWriteDeadline(deadline time.Time) error {
	return t.input.SetWriteDeadline(deadline)
}

func (t *Terminal) Interrupt() error {
	_, err := t.Write([]byte{3})
	return err
}

func (t *Terminal) Read(buf []byte) (int, error)  { return t.output.Read(buf) }
func (t *Terminal) Write(buf []byte) (int, error) { return t.input.Write(buf) }

// SendEOF types Ctrl+Z followed by Enter, the EOF convention supported by
// some Windows programs reading a cooked console at the start of a line.
// It does not close the input pipe, and raw-mode programs may read it as input.
func (t *Terminal) SendEOF() error {
	_, err := t.Write([]byte{0x1a, '\r'})
	return err
}

// Open allocates a pseudo console without starting a process.
func Open(cols, rows int) (*Terminal, error) {
	if !IsConPtyAvailable() {
		return nil, ErrConPtyUnsupported
	}
	if cols <= 0 || cols > math.MaxInt16 || rows <= 0 || rows > math.MaxInt16 {
		return nil, fmt.Errorf("invalid ConPTY dimensions %dx%d: %w", cols, rows, os.ErrInvalid)
	}

	input, childInput, err := openConptyPipe(false)
	if err != nil {
		return nil, fmt.Errorf("create ConPTY input: %w", err)
	}
	output, childOutput, err := openConptyPipe(true)
	if err != nil {
		_ = windows.CloseHandle(childInput)
		_ = input.Close()
		return nil, fmt.Errorf("create ConPTY output: %w", err)
	}
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	var console windows.Handle
	err = windows.CreatePseudoConsole(size, childInput, childOutput, 0, &console)
	_ = windows.CloseHandle(childInput)
	_ = windows.CloseHandle(childOutput)
	if err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	return &Terminal{console: console, input: input, output: output, size: size}, nil
}

func environmentBlock(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return []uint16{0, 0}, nil
	}
	keys := make([]string, 0, len(env))
	byKey := make(map[string]string, len(env))
	for _, entry := range env {
		start := 0
		if strings.HasPrefix(entry, "=") {
			start = 1 // Windows drive-directory entries have a leading '='.
		}
		end := strings.IndexByte(entry[start:], '=')
		if end < 1 || strings.ContainsRune(entry, 0) {
			return nil, errors.New("invalid Windows environment entry")
		}
		key := strings.ToUpper(entry[:start+end])
		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}
		byKey[key] = entry
	}
	slices.Sort(keys)
	var block []uint16
	for _, key := range keys {
		for _, r := range byKey[key] {
			block = utf16.AppendRune(block, r)
		}
		block = append(block, 0)
	}
	return append(block, 0), nil
}

// The synchronous server endpoint belongs to ConPTY, while Go owns the
// overlapped client endpoint so that Close can cancel blocked host I/O.
func openConptyPipe(hostReads bool) (*os.File, windows.Handle, error) {
	name := `\\.\pipe\conpty-` + rand.Text()
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, 0, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, 0, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, 0, err
	}
	attrs := windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	direction, access := uint32(windows.PIPE_ACCESS_INBOUND), os.O_WRONLY
	if hostReads {
		direction, access = windows.PIPE_ACCESS_OUTBOUND, os.O_RDONLY
	}
	child, err := windows.CreateNamedPipe(name16, direction|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 65536, 65536, 0, &attrs)
	if err != nil {
		return nil, 0, err
	}
	host, err := os.OpenFile(name, access|int(windows.O_FILE_FLAG_OVERLAPPED), 0)
	if err != nil {
		_ = windows.CloseHandle(child)
		return nil, 0, err
	}
	if err := windows.ConnectNamedPipe(child, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = host.Close()
		_ = windows.CloseHandle(child)
		return nil, 0, err
	}
	if err := host.SetDeadline(time.Time{}); err != nil {
		_ = host.Close()
		_ = windows.CloseHandle(child)
		return nil, 0, fmt.Errorf("ConPTY host pipe is not pollable: %w", err)
	}
	return host, child, nil
}
