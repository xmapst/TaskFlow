package parser

// csiParams 是一条 CSI 序列解析后的参数。nums 中缺省的参数记为 -1。
type csiParams struct {
	private byte // '<' '=' '>' '?' 之一，没有则为 0
	inter   bool // 含中间字节(0x20-0x2f)，如 CSI Ps SP A
	valid   bool
	nums    []int
}

// maxParam 防止畸形序列中的超长数字溢出；真正的数量上限由各处理函数按屏幕尺寸截断。
const maxParam = 1 << 16

func parseCSIParams(raw []byte) csiParams {
	p := csiParams{valid: true}
	cur := -1
	for i, b := range raw {
		switch {
		case b >= '0' && b <= '9':
			cur = min(max(cur, 0)*10+int(b-'0'), maxParam)
		case b == ';' || b == ':':
			p.nums = append(p.nums, cur)
			cur = -1
		case b >= '<' && b <= '?':
			if i != 0 {
				p.valid = false
			}
			p.private = b
		case b >= 0x20 && b <= 0x2f:
			p.inter = true
		default:
			p.valid = false
		}
	}
	if cur >= 0 || len(p.nums) > 0 {
		p.nums = append(p.nums, cur)
	}
	return p
}

// raw 返回第 i 个参数，缺省为 def；0 是有意义的值(如 ED/EL 的模式)。
func (p csiParams) raw(i, def int) int {
	if i >= len(p.nums) || p.nums[i] < 0 {
		return def
	}
	return p.nums[i]
}

// count 返回第 i 个参数；缺省与 0 都表示 1(VT 的位置与计数约定)。
func (p csiParams) count(i int) int {
	if n := p.raw(i, 1); n > 0 {
		return n
	}
	return 1
}

func (s *Screen) applyCSI(final byte) {
	p := parseCSIParams(s.params)
	if !p.valid {
		return
	}
	switch final {
	case 'h', 'l':
		s.setMode(p, final == 'h')
		return
	}
	// 带中间字节的序列(SR、DECSCUSR、DECSTR 等)不影响文本模型。
	if p.inter {
		return
	}
	// 私有前缀只有 DECSED/DECSEL 与对应的 ED/EL 等价；其余如 CSI ? u、CSI > m 一律忽略，
	// 避免被误当成普通序列。
	if p.private != 0 && !(p.private == '?' && (final == 'J' || final == 'K')) {
		return
	}
	s.dispatchCSI(final, p)
	s.Cursor.X = min(max(s.Cursor.X, 0), maxCols)
	s.Cursor.Y = min(max(s.Cursor.Y, 1), maxRows)
}

func (s *Screen) dispatchCSI(final byte, p csiParams) {
	c := s.Cursor
	switch final {
	// ---- 光标移动 ----
	case 'A': // CUU
		c.MoveUp(p.count(0))
	case 'B', 'e': // CUD、VPR
		c.MoveDown(p.count(0))
	case 'C', 'a': // CUF、HPR
		c.MoveRight(p.count(0))
	case 'D': // CUB
		c.MoveLeft(p.count(0))
	case 'E': // CNL
		c.MoveDown(p.count(0))
		c.X = 0
	case 'F': // CPL
		c.MoveUp(p.count(0))
		c.X = 0
	case 'G', '`': // CHA、HPA
		c.X = p.count(0) - 1
	case 'd': // VPA
		c.Y = p.count(0)
	case 'H', 'f': // CUP、HVP
		c.Y, c.X = p.count(0), p.count(1)-1
	case 'I': // CHT
		for range min(p.count(0), maxCols) {
			c.X = nextTabStop(c.X)
		}
	case 'Z': // CBT
		for range min(p.count(0), maxCols) {
			c.X = prevTabStop(c.X)
		}

	// ---- 擦除 ----
	case 'J':
		s.eraseDisplay(p.raw(0, 0))
	case 'K':
		s.eraseLine(p.raw(0, 0))
	case 'X': // ECH：原位擦除，不移动右侧内容
		s.GetCursorRow().eraseChars(c.X, p.count(0))
	case 'P': // DCH
		s.GetCursorRow().deleteChars(c.X, p.count(0))
	case '@': // ICH
		s.GetCursorRow().insertBlank(c.X, p.count(0))

	// ---- 行操作 ----
	case 'L': // IL
		s.GetCursorRow()
		s.insertRows(c.Y-1, p.count(0))
		c.X = 0
	case 'M': // DL
		s.GetCursorRow()
		s.deleteRows(c.Y-1, p.count(0))
		c.X = 0

	// ---- 光标保存 / 恢复(SCOSC、SCORC) ----
	case 's':
		if len(p.nums) == 0 {
			s.saveCursor()
		}
	case 'u':
		if len(p.nums) == 0 {
			s.restoreCursor()
		}

	case 'm':
		s.applySGR(p)
	}
}

// applySGR 只为 fish 的补全提示做特殊处理：ESC[30m 结束记录，ESC[90m 开始记录灰色提示。
func (s *Screen) applySGR(p csiParams) {
	if len(p.nums) != 1 || s.Cursor.Y < 1 || len(s.Rows) == 0 {
		return
	}
	row := s.Rows[min(s.Cursor.Y, len(s.Rows))-1]
	switch p.nums[0] {
	case 30:
		row.stopRecord()
	case 90:
		row.startRecord()
	}
}

// setMode 处理 CSI ? Pm h/l。
func (s *Screen) setMode(p csiParams, on bool) {
	if p.private != '?' {
		return
	}
	for _, mode := range p.nums {
		switch mode {
		case 47, 1047, 1049:
			s.switchAlternate(on)
		case 1048:
			if on {
				s.saveCursor()
			} else {
				s.restoreCursor()
			}
		case 2004:
			s.pasteMode = on
		}
	}
}

func nextTabStop(x int) int { return min((x/tabWidth+1)*tabWidth, maxCols) }

func prevTabStop(x int) int {
	if x <= 0 {
		return 0
	}
	return (x - 1) / tabWidth * tabWidth
}
