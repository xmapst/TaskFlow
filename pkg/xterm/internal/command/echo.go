package command

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/xmapst/taskflow/pkg/xterm/parser"
)

const (
	echoTimeout = 450 * time.Millisecond
	echoQuiet   = 25 * time.Millisecond
)

type echoRow struct {
	text                            string
	x, y                            int
	breaks, revision, layout, bells uint64
	cols                            int
	ready, alternate                bool
	updated                         time.Time
}

// echoScreen is shared by the PTY read pump and the input pump. The input
// pump never holds its lock while writing to the PTY or invoking an auditor.
type echoScreen struct {
	mu                      sync.Mutex
	screen                  parser.Screen
	changed                 chan struct{}
	revision, layout, bells uint64
	updated                 time.Time
	cols                    int
}

func (e *echoScreen) signal() {
	e.revision++
	e.notify()
}

func (e *echoScreen) notify() {
	e.updated = time.Now()
	if e.changed != nil {
		close(e.changed)
		e.changed = nil
	}
}

func (e *echoScreen) resize(cols int16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cols = int(cols)
	e.layout++
	e.signal()
}

func (e *echoScreen) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.screen = parser.Screen{}
	e.signal()
}

func (e *echoScreen) feed(data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.screen.Feed(data)
	// A bell-only response wakes waiters, but is not a fresh text echo.
	if len(bytes.Trim(data, "\a")) == 0 {
		e.bells++
		e.notify()
	} else {
		e.signal()
	}
}

func (e *echoScreen) read() (echoRow, <-chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.changed == nil {
		e.changed = make(chan struct{})
	}
	text, x, y, breaks, ready := e.screen.EchoLine()
	cols := e.cols
	if cols == 0 {
		cols = 80
	}
	return echoRow{text, x, y, breaks, e.revision, e.layout, e.bells, cols, ready, e.screen.AlternateScreen(), e.updated}, e.changed
}

func (e *echoScreen) wait(ctx context.Context, match func(echoRow) bool) (echoRow, bool) {
	ctx, cancel := context.WithTimeout(ctx, echoTimeout)
	defer cancel()
	for ctx.Err() == nil {
		row, changed := e.read()
		if match(row) {
			if delay := time.Until(row.updated.Add(echoQuiet)); delay <= 0 {
				return row, true
			} else {
				timer := time.NewTimer(delay)
				select {
				case <-changed:
				case <-timer.C:
				case <-ctx.Done():
				}
				timer.Stop()
				continue
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
		}
	}
	return echoRow{}, false
}

func (r echoRow) isPrompt() bool {
	if !r.ready || r.x != runewidth.StringWidth(r.text) || r.x >= r.cols {
		return false
	}
	text := strings.TrimRight(r.text, " ")
	last, _ := utf8.DecodeLastRuneInString(text)
	return strings.ContainsRune("$#>%❯➜", last)
}

func (r echoRow) commandAfter(prompt echoRow) (string, bool) {
	if !r.ready || r.y != prompt.y || r.breaks != prompt.breaks || r.layout != prompt.layout || r.x < prompt.x || runewidth.StringWidth(r.text) >= r.cols {
		return "", false
	}
	return strings.CutPrefix(r.text, prompt.text)
}
