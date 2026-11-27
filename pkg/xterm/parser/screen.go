package parser

import (
	"slices"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Screen is a bounded, best-effort terminal text model. Its zero value is
// ready to use. A screen belongs to one stream and is not concurrent-safe.
type Screen struct {
	Cursor     *Cursor
	Rows       []*Row
	params     []byte
	utf8Tail   []byte
	lineBreaks uint64
	pasteMode  bool
	state      byte
	alternate  bool

	// 光标保存槽(DECSC/SCOSC)，以及进入备用屏幕时被换出的主屏幕。
	saved      Cursor
	mainRows   []*Row
	mainCursor Cursor
	mainSaved  Cursor
}

const (
	maxRows  = 256
	maxCols  = 1024
	tabWidth = 8
)

const (
	textState byte = iota
	escapeState
	intermediateState
	csiState
	discardCSIState
	stringState
	stringEscapeState
)

// Parse consumes the next chunk and returns a fresh snapshot of the retained
// rows. Incomplete UTF-8, CSI and control strings continue in the next chunk.
func (s *Screen) Parse(data []byte) []string {
	s.Feed(data)
	result := make([]string, len(s.Rows))
	for i, row := range s.Rows {
		result[i] = row.String()
	}
	return result
}

// Feed consumes output without allocating a snapshot of the entire screen.
func (s *Screen) Feed(data []byte) {
	if s.Cursor == nil {
		s.Cursor = &Cursor{Y: 1}
	}
	if len(s.utf8Tail) != 0 {
		data = append(s.utf8Tail, data...)
		s.utf8Tail = nil
	}
	for len(data) != 0 {
		b := data[0]
		if s.state == textState && b >= utf8.RuneSelf {
			if !utf8.FullRune(data) {
				s.utf8Tail = append(s.utf8Tail, data...)
				break
			}
			r, n := utf8.DecodeRune(data)
			s.appendCharacter(r)
			data = data[n:]
			continue
		}
		data = data[1:]
		s.consumeByte(b)
	}
}

// AlternateScreen reports whether the terminal has selected its alternate buffer.
func (s *Screen) AlternateScreen() bool { return s.alternate }

// EchoLine returns the untrimmed cursor row and cursor column. ready is false
// during an incomplete sequence or when the alternate screen is active.
func (s *Screen) EchoLine() (line string, x, y int, breaks uint64, ready bool) {
	if s.Cursor == nil || s.Cursor.Y < 1 || s.Cursor.Y > len(s.Rows) || s.state != textState || len(s.utf8Tail) != 0 || s.alternate {
		return "", 0, 0, s.lineBreaks, false
	}
	return string(s.Rows[s.Cursor.Y-1].dataRune), s.Cursor.X, s.Cursor.Y, s.lineBreaks, true
}

func (s *Screen) consumeByte(b byte) {
	// CAN 与 SUB 在任何未完成的序列中都会中止它，且自身不产生输出。
	if (b == 0x18 || b == 0x1a) && s.state != textState {
		s.state = textState
		return
	}
	switch s.state {
	case textState:
		switch {
		case b == 0x1b:
			s.state = escapeState
		case b < 0x20:
			s.parseC0Sequence(rune(b))
		case b != 0x7f:
			s.appendCharacter(rune(b))
		}
	case escapeState:
		s.state = textState
		switch {
		case b == '[':
			s.state = csiState
			s.params = s.params[:0]
		case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
			s.state = stringState
		case b >= 0x20 && b <= 0x2f:
			s.state = intermediateState
		case b == 0x1b:
			s.state = escapeState
		case b < 0x20:
			s.parseC0Sequence(rune(b))
		default:
			s.applyEscape(b)
		}
	case intermediateState:
		if b == 0x1b {
			s.state = escapeState
		} else if b < 0x20 || b > 0x2f {
			s.state = textState
		}
	case csiState, discardCSIState:
		switch {
		case b == 0x1b:
			s.state = escapeState
		case b >= 0x40 && b <= 0x7e:
			if s.state == csiState {
				s.applyCSI(b)
			}
			s.state = textState
		case b >= 0x20 && b <= 0x3f && len(s.params) < 64 && s.state == csiState:
			s.params = append(s.params, b)
		default:
			s.state = discardCSIState
		}
	case stringState, stringEscapeState:
		if b == 0x07 || (s.state == stringEscapeState && b == '\\') {
			s.state = textState
		} else if b == 0x1b {
			s.state = stringEscapeState
		} else {
			s.state = stringState
		}
	}
}

// applyEscape 处理不带中间字节的两字节 ESC 序列。
func (s *Screen) applyEscape(final byte) {
	switch final {
	case '7': // DECSC
		s.saveCursor()
	case '8': // DECRC
		s.restoreCursor()
	case 'D': // IND
		s.lineFeed()
	case 'E': // NEL
		s.Cursor.X = 0
		s.lineFeed()
	case 'M': // RI
		if s.Cursor.Y > 1 {
			s.Cursor.Y--
		} else {
			s.GetCursorRow()
			s.insertRows(0, 1)
		}
	case 'c': // RIS
		s.reset()
	}
}

func (s *Screen) parseC0Sequence(code rune) {
	switch code {
	case 0x07: // BEL is ignored.
	case 0x08: // Backspace.
		s.Cursor.MoveLeft(1)
	case 0x09: // Horizontal tab.
		s.Cursor.X = nextTabStop(s.Cursor.X)
	case 0x0a, 0x0b, 0x0c: // LF、VT、FF 都是换行。
		s.lineFeed()
	case 0x0d: // Carriage return.
		s.Cursor.X = 0
	}
}

// lineFeed 把光标下移一行(不回到行首)，到达保留行数上限时丢弃最旧的一行。
func (s *Screen) lineFeed() {
	s.lineBreaks++
	if s.Cursor.Y >= maxRows {
		s.GetCursorRow()
		copy(s.Rows, s.Rows[1:])
		s.Rows = s.Rows[:len(s.Rows)-1]
	} else {
		s.Cursor.Y++
	}
	s.GetCursorRow()
}

func (s *Screen) appendCharacter(code rune) {
	if s.Cursor.X >= maxCols {
		return
	}
	s.GetCursorRow().put(s.Cursor.X, code)
	s.Cursor.X = min(s.Cursor.X+runewidth.RuneWidth(code), maxCols)
}

func (s *Screen) saveCursor() { s.saved = *s.Cursor }

func (s *Screen) restoreCursor() {
	*s.Cursor = s.saved
	s.Cursor.Y = max(s.Cursor.Y, 1)
}

// reset 实现 RIS：回到初始状态，但换行计数保持单调递增，让等待回显的一方感知到屏幕已变化。
func (s *Screen) reset() {
	breaks := s.lineBreaks + 1
	*s = Screen{Cursor: &Cursor{Y: 1}, lineBreaks: breaks, state: textState}
}

// switchAlternate 在主屏幕与备用屏幕之间切换。进入时换出主屏幕内容与光标，
// 离开时原样换回；没有对应的进入就离开则不做任何事，避免误擦主屏幕。
func (s *Screen) switchAlternate(on bool) {
	switch {
	case on && !s.alternate:
		s.mainRows, s.mainCursor, s.mainSaved = s.Rows, *s.Cursor, s.saved
		s.Rows, *s.Cursor, s.saved = nil, Cursor{Y: 1}, Cursor{}
		s.alternate = true
	case on:
		s.Rows, *s.Cursor = nil, Cursor{Y: 1}
	case s.alternate:
		s.Rows, *s.Cursor, s.saved = s.mainRows, s.mainCursor, s.mainSaved
		s.mainRows = nil
		s.alternate = false
	default:
		return
	}
	s.lineBreaks++
}

// eraseDisplay 实现 ED。光标位置在所有模式下都保持不变。
func (s *Screen) eraseDisplay(mode int) {
	row := s.GetCursorRow()
	switch mode {
	case 0:
		row.truncate(s.Cursor.X)
		s.Rows = s.Rows[:s.Cursor.Y]
	case 1:
		for _, r := range s.Rows[:s.Cursor.Y-1] {
			*r = Row{}
		}
		row.blankRange(0, s.Cursor.X+1)
	case 2:
		for _, r := range s.Rows {
			*r = Row{}
		}
	}
}

// eraseLine 实现 EL。
func (s *Screen) eraseLine(mode int) {
	row := s.GetCursorRow()
	switch mode {
	case 0:
		row.truncate(s.Cursor.X)
	case 1:
		row.blankRange(0, s.Cursor.X+1)
	case 2:
		*row = Row{}
	}
}

// insertRows 在 at 行之前插入 n 个空行，超出 maxRows 的底部行被丢弃(IL)。
func (s *Screen) insertRows(at, n int) {
	at = min(max(at, 0), len(s.Rows))
	fresh := make([]*Row, min(n, maxRows))
	for i := range fresh {
		fresh[i] = &Row{}
	}
	s.Rows = slices.Insert(s.Rows, at, fresh...)
	if len(s.Rows) > maxRows {
		s.Rows = s.Rows[:maxRows]
	}
}

// deleteRows 删除从 at 行起的 n 行，下方内容上移(DL)。
func (s *Screen) deleteRows(at, n int) {
	at = min(max(at, 0), len(s.Rows))
	s.Rows = slices.Delete(s.Rows, at, min(at+n, len(s.Rows)))
	s.GetCursorRow()
}

func (s *Screen) GetCursorRow() *Row {
	if s.Cursor == nil {
		s.Cursor = &Cursor{Y: 1}
	}
	s.Cursor.Y = min(max(s.Cursor.Y, 1), maxRows)
	for len(s.Rows) < s.Cursor.Y {
		s.Rows = append(s.Rows, &Row{})
	}
	return s.Rows[s.Cursor.Y-1]
}
