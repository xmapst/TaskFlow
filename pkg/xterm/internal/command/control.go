package command

import (
	"context"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const maxCommandLine = 4095 // Leave room for Enter in a Linux canonical PTY line.

func (f *Control) cancelCommandByte() byte {
	if runtime.GOOS == "windows" {
		return 3 // Ctrl+C cancels the PowerShell line.
	}
	return 21 // Ctrl+U clears the line without interrupting the foreground process.
}

const (
	commandDenied = "\r\nCommand blocked by policy.\r\n"
	unknownInput  = "\r\nCannot verify the command from terminal echo; clear the line and try again.\r\n"
	tabEchoWait   = "\a" // Signal the withheld Enter without moving the shell's cursor.
)

// Auditor decides whether to submit an echo-verified command. An error allows
// submission only if the session is active and the echo still verifies.
type Auditor func(ctx context.Context, command string) (allow bool, err error)

// Interceptor selects a local action for an audited, echo-verified command.
// The prompt is display text, not an authenticated statement of shell state.
type Interceptor func(command, prompt string) (action func(context.Context) error)

// Control owns a session's command and echo state. The input goroutine calls
// Write, Resize and InvalidateEcho; the output goroutine calls ObserveOutput.
type Control struct {
	echo         echoScreen
	auditor      Auditor
	intercept    Interceptor
	childInFront func() bool // Optional PTY foreground check.
	canInteract  bool        // Set only after submitting an audited command.
	prompt       echoRow
	anchored     bool
	lastWrite    uint64
	priorPrompt  echoRow
	promptAfter  uint64
	needPrompt   bool
	line         []byte // expected text for plain input, to reject incomplete echo
	edited       bool
	tabbed       bool // The current line contains a forwarded completion key.
	tabWaits     int  // Enter keys withheld while completion echo was unverified.
	beforeTab    echoRow
	unknown      bool
	lastCR       bool
	paste        bool
	escape       [32]byte
	escLen       int
}

// New constructs a command control. When audit is nil, input passes through.
func New(cols int16, audit Auditor, intercept Interceptor, childInForeground func() bool) *Control {
	return &Control{echo: echoScreen{cols: int(cols)}, auditor: audit, intercept: intercept, childInFront: childInForeground}
}

// ObserveOutput feeds terminal text, not transfer bytes, into the echo screen.
func (f *Control) ObserveOutput(data []byte) {
	if f.auditor != nil && len(data) != 0 {
		f.echo.feed(data)
	}
}

// Resize updates the command screen width.
func (f *Control) Resize(cols int16) {
	f.echo.resize(cols)
}

func (f *Control) promptReady(r echoRow) bool {
	if !r.isPrompt() {
		return false
	}
	return !f.needPrompt || (r.revision > f.promptAfter &&
		(r.breaks != f.priorPrompt.breaks || r.y != f.priorPrompt.y || r.text == f.priorPrompt.text))
}

func (f *Control) interactive(r echoRow) bool {
	return f.canInteract && r.alternate && f.childInFront != nil && f.childInFront()
}

func (f *Control) anchor(r echoRow) {
	f.prompt, f.anchored = r, true
	f.needPrompt, f.canInteract = false, false
}

// tabBell matches a no-op completion on a row that was already verified before Tab.
func (f *Control) tabBell(r echoRow) bool {
	before := f.beforeTab
	return before.ready && r.ready && r.bells > before.bells && r.revision == before.revision &&
		r.text == before.text && r.x == before.x && r.y == before.y &&
		r.breaks == before.breaks && r.layout == before.layout
}

// relocatePrompt follows a prompt that the shell redrew on a later row, as bash
// does after listing completion candidates. The prompt text and column must be
// unchanged, so only a redraw of the anchored prompt is accepted. It returns
// the anchor to use for r.
func (f *Control) relocatePrompt(r echoRow) echoRow {
	p := f.prompt
	if !f.tabbed || !r.ready || r.layout != p.layout || (r.y == p.y && r.breaks == p.breaks) ||
		r.x < p.x || runewidth.StringWidth(r.text) >= r.cols || !strings.HasPrefix(r.text, p.text) {
		return p
	}
	p.y, p.breaks = r.y, r.breaks
	return p
}

// completionQuery reports whether bash is asking a readline question
// ("Display all N possibilities? (y or n)") or paging a completion list
// ("--More--"). Keys then answer bash instead of editing a command line, so they
// must be forwarded verbatim. Keys bash rejects only ring the bell, leaving the
// question on screen, so the row alone decides; it changes once bash accepts one.
// The question must be on a row after the anchored prompt.
func (f *Control) completionQuery(r echoRow) bool {
	if !r.ready {
		return false
	}
	text := strings.TrimRight(r.text, " ")
	if !strings.HasSuffix(text, "(y or n)") && !strings.HasSuffix(text, "--More--") {
		return false
	}
	return !f.anchored || r.y != f.prompt.y || r.breaks != f.prompt.breaks
}

// answerQuery forwards the answer to a completion question. A (y or n) question
// consumes one key; a pager consumes the whole frame. Afterwards the shell
// redraws its prompt (or the pager continues), so wait for that like after Enter.
func (f *Control) answerQuery(ctx context.Context, row echoRow, data []byte, send, notice func([]byte) error) error {
	n := len(data)
	if !strings.HasSuffix(strings.TrimRight(row.text, " "), "--More--") {
		n = 1
	}
	if err := send(data[:n]); err != nil {
		return err
	}
	current, _ := f.echo.read()
	prior := f.prompt
	if !f.anchored {
		prior = f.priorPrompt
	}
	f.resetLine()
	f.lastCR = false
	f.canInteract = false
	f.priorPrompt, f.promptAfter, f.needPrompt = prior, current.revision, true
	if n == len(data) {
		return nil
	}
	f.echo.wait(ctx, func(r echoRow) bool { return f.completionQuery(r) || f.promptReady(r) })
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.Write(ctx, data[n:], send, notice)
}

// Write forwards keys, withholding command submission until echo and policy permit it.
func (f *Control) Write(ctx context.Context, data []byte, send func([]byte) error, notice func([]byte) error) error {
	if f.auditor == nil {
		return send(data)
	}
	if len(data) == 0 {
		return nil
	}
	if row, _ := f.echo.read(); f.completionQuery(row) && data[0] != 3 {
		return f.answerQuery(ctx, row, data, send, notice)
	}
	modeWaited := false
	if f.canInteract {
		row, _ := f.echo.read()
		if !f.interactive(row) && !f.promptReady(row) && data[0] != 3 {
			// Input can arrive while the child enters the alternate screen or
			// while the shell is redrawing its prompt after the child exits.
			modeWaited = true
			row, _ = f.echo.wait(ctx, func(r echoRow) bool { return f.interactive(r) || f.promptReady(r) })
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if current, _ := f.echo.read(); f.interactive(current) {
			// Do not ship a second line in the same write: the first submit
			// key can exit the program and return ownership of the PTY.
			if i := strings.IndexAny(string(data), "\r\n\x03\x04\x1a"); i >= 0 {
				data = data[:i+1]
			}
			return send(data)
		}
		if f.promptReady(row) {
			f.canInteract = false
		}
	}
	pending := make([]byte, 0, len(data))
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if !f.anchored && !f.unknown {
			prompt, _ := f.echo.read()
			ok := f.promptReady(prompt)
			if !modeWaited || ok {
				prompt, ok = f.echo.wait(ctx, f.promptReady)
			}
			if !ok {
				if err := ctx.Err(); err != nil {
					return err
				}
				f.unknown = true
				pending = pending[:0]
				return nil
			}
			f.anchor(prompt)
		}
		row, _ := f.echo.read()
		f.prompt = f.relocatePrompt(row)
		beforeTab := echoRow{}
		if len(pending) == 1 && pending[0] == '\t' {
			if cmd, ok := row.commandAfter(f.prompt); ok && strings.TrimSpace(cmd) != "" &&
				(row.revision > f.lastWrite || f.tabBell(row)) && (f.edited || cmd == string(f.line)) {
				beforeTab = row
			}
		}
		f.beforeTab = beforeTab
		f.lastWrite = row.revision
		err := send(pending)
		pending = pending[:0]
		return err
	}
	for _, b := range data {
		if b == '\n' && f.lastCR {
			f.lastCR = false
			continue
		}
		f.lastCR = false
		switch b {
		case '\r', '\n':
			f.lastCR = b == '\r'
			if f.paste {
				// Don't turn bracketed paste into multiple executions, even when
				// the markers or newlines arrived in separate frames.
				f.unknown = true
				continue
			}
			if err := flush(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			message := ""
			approved := false
			var action func(context.Context) error
			switch {
			case f.unknown || f.escLen != 0 || !utf8.Valid(f.line):
				message = unknownInput
			case !f.anchored:
				// Even an empty Enter must wait for the new shell prompt.
				if _, ok := f.echo.wait(ctx, f.promptReady); !ok {
					message = unknownInput
				}
			case f.anchored:
				row, ok := f.echo.wait(ctx, func(r echoRow) bool {
					cmd, valid := r.commandAfter(f.relocatePrompt(r))
					if !valid || (r.revision <= f.lastWrite && !f.tabBell(r)) {
						return false
					}
					if f.edited {
						return strings.TrimSpace(cmd) != ""
					}
					return cmd == string(f.line)
				})
				if err := ctx.Err(); err != nil {
					return err
				}
				if !ok {
					if f.tabbed && f.tabWaits == 0 {
						// Completion may still be running. Keep the line and its echo
						// baseline, and drop the rest of this frame rather than replaying
						// input after the withheld Enter. A second consecutive miss means
						// the echo will not verify (for example a completion pager), so
						// cancel the line instead of swallowing Enter forever.
						f.tabWaits++
						return notice([]byte(tabEchoWait))
					}
					message = unknownInput
				} else {
					f.prompt = f.relocatePrompt(row)
					raw, _ := row.commandAfter(f.prompt)
					if command := strings.TrimSpace(raw); command != "" {
						allow, err := f.auditor(ctx, command)
						if ctxErr := ctx.Err(); ctxErr != nil {
							return ctxErr
						}
						if err == nil && !allow {
							message = commandDenied
						} else {
							latest, _ := f.echo.read()
							if current, ok := latest.commandAfter(f.prompt); !ok || current != raw ||
								(row.revision <= f.lastWrite && !f.tabBell(latest)) {
								message = unknownInput
							} else {
								approved = true
								if f.intercept != nil {
									action = f.intercept(command, f.prompt.text)
								}
							}
						}
					}
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if f.anchored || f.needPrompt || message == "" {
				current, _ := f.echo.read()
				if f.anchored {
					f.priorPrompt = f.prompt
				} else if f.promptReady(current) {
					f.priorPrompt = current
				}
				f.promptAfter = current.revision
				f.needPrompt = true
			}
			f.canInteract = approved && action == nil
			cancelByte := f.cancelCommandByte()
			if message == unknownInput || (f.anchored && f.edited) {
				// Ctrl+U may clear only the text before a moved cursor.
				cancelByte = 3
			}
			f.resetLine()
			if action != nil {
				if err := send([]byte{3}); err != nil {
					return err
				}
				// Do not replay the rest of a frame across the local action.
				return action(ctx)
			}
			if message == "" {
				if err := send([]byte{b}); err != nil {
					return err
				}
			} else {
				if err := send([]byte{cancelByte}); err != nil {
					return err
				}
				if err := notice([]byte(message)); err != nil {
					return err
				}
			}
		case 3: // Ctrl+C resets local reconstruction and interrupts the foreground.
			pending = pending[:0]
			f.needPrompt = false
			f.canInteract = false
			f.resetLine()
			if err := send([]byte{b}); err != nil {
				return err
			}
		default:
			if f.escLen != 0 || b == '\x1b' {
				pending = append(pending, f.consumeEscape(b)...)
				continue
			}
			switch b {
			case 21: // Ctrl+U clears an ordinary line; unsupported editing still needs Ctrl+C.
				f.line = f.line[:0]
				if !f.paste {
					pending = append(pending, b)
				} else {
					f.unknown = true
				}
			case '\t':
				if !f.paste && !f.unknown {
					if err := flush(); err != nil {
						return err
					}
					if f.unknown {
						continue
					}
					pending = append(pending, b)
					if err := flush(); err != nil {
						return err
					}
					f.edited, f.tabbed, f.tabWaits = true, true, 0
				} else {
					f.unknown = true
				}
			case '\b', 127:
				if f.paste {
					f.unknown = true
				} else if (len(f.line) > 0 || f.edited) && !f.unknown {
					if len(f.line) > 0 {
						_, size := utf8.DecodeLastRune(f.line)
						f.line = f.line[:len(f.line)-size]
					}
					pending = append(pending, b)
				}
			case 4: // Ctrl+D on a clean, empty line retains shell EOF.
				if len(f.line) == 0 && !f.edited && !f.unknown && !f.paste && !f.anchored && len(pending) == 0 {
					if err := send([]byte{b}); err != nil {
						return err
					}
				} else {
					f.unknown = true
				}
			default:
				if b < ' ' || len(f.line) >= maxCommandLine {
					f.unknown = true
				}
				if !f.unknown {
					f.line = append(f.line, b)
					pending = append(pending, b)
				}
			}
		}
	}
	return flush()
}

// InvalidateEcho discards the current row and input state before the next prompt arrives.
// Only the input goroutine may call this.
func (f *Control) InvalidateEcho() {
	if f.auditor == nil {
		return
	}
	f.resetLine()
	f.needPrompt, f.canInteract = false, false
	f.lastCR = false
	f.echo.reset()
}

func (f *Control) resetLine() {
	f.line = f.line[:0]
	f.edited, f.tabbed, f.anchored = false, false, false
	f.tabWaits = 0
	f.beforeTab = echoRow{}
	f.unknown = false
	f.paste = false
	f.escLen = 0
}

// These reports are emitted by xterm.js independently of keyboard input.
// Drop them rather than forwarding untrusted escape sequences to the shell.
var terminalReport = regexp.MustCompile(`^\x1b\[(?:\??[0-9]{1,6};[0-9]{1,6}R|0n|\?(?:1;2|6)c|>(?:0;276;0|85;95;0|83;40003;0)c|[IO])$`)

var editingKey = regexp.MustCompile(`^\x1b(?:[\[O][ABCDHF]|\[[13478]~)$`)

func (f *Control) consumeEscape(b byte) []byte {
	switch f.escLen {
	case -1:
		if b >= 0x40 && b <= 0x7e {
			f.escLen = 0
		}
	case 0:
		f.escape[0] = b
		f.escLen = 1
	case 1:
		if b != '[' && b != 'O' {
			f.unknown, f.escLen = true, 0
			return nil
		}
		f.escape[1] = b
		f.escLen = 2
	default:
		f.escape[f.escLen] = b
		f.escLen++
		if b >= 0x40 && b <= 0x7e {
			switch string(f.escape[:f.escLen]) {
			case "\x1b[200~":
				if f.paste {
					f.unknown = true
				}
				f.paste = true
			case "\x1b[201~":
				if !f.paste {
					f.unknown = true
				}
				f.paste = false
			default:
				seq := f.escape[:f.escLen]
				if !f.unknown && !f.paste && editingKey.Match(seq) {
					f.edited = true
					f.escLen = 0
					return seq
				}
				if !terminalReport.Match(seq) {
					f.unknown = true
				}
			}
			f.escLen = 0
		} else if f.escLen == len(f.escape) {
			f.unknown, f.escLen = true, -1
		}
	}
	return nil
}
