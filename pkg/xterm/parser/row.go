package parser

import (
	"slices"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Row 是一行终端文本。dataRune 以 rune 存储，宽字符占两个单元格(cell)，
// 零宽字符(组合符)依附在前一个字符之后。所有操作都以「列」为单位，
// 不再维护独立的光标状态，列与 rune 下标的换算由 locate 统一完成。
type Row struct {
	dataRune []rune
	// fish shell 补全提示
	tipRune []rune
	// wide 记录 dataRune 里是否出现过宽度不是 1 的字符(宽字符或组合符)。
	// 为 false 时 rune 下标等于列号，locate、width 都是 O(1)；它只增不减，
	// 因此是保守的：即使后来宽字符被擦掉，也只是退回到逐字符计算，结果依然正确。
	wide      bool
	tipRecord bool
}

func (r *Row) String() string {
	return strings.TrimSuffix(string(r.dataRune), string(r.tipRune))
}

func blanks(n int) []rune {
	out := make([]rune, max(n, 0))
	for i := range out {
		out[i] = ' '
	}
	return out
}

func (r *Row) width() int {
	if !r.wide {
		return len(r.dataRune)
	}
	w := 0
	for _, c := range r.dataRune {
		w += runewidth.RuneWidth(c)
	}
	return w
}

// locate 返回覆盖 col 列的 rune 下标，以及 col 相对该 rune 起始列的偏移。
// 当 col 位于行尾之外时返回 len(dataRune)，偏移为需要补齐的空白列数。
// 零宽字符不覆盖任何列，因此总是被跳过。
func (r *Row) locate(col int) (index, offset int) {
	if !r.wide {
		// 每个 rune 恰好占一列：列内直接寻址，列外的偏移是需要补齐的空白数。
		if col < len(r.dataRune) {
			return col, 0
		}
		return len(r.dataRune), col - len(r.dataRune)
	}
	pos := 0
	for i, c := range r.dataRune {
		w := runewidth.RuneWidth(c)
		if pos+w > col {
			return i, col - pos
		}
		pos += w
	}
	return len(r.dataRune), col - pos
}

// splice 用 ins 替换 [start,end) 列。被边界切开的宽字符，其残留的半边以空格代替，
// 以保证后续字符的列位置不变。start 必须落在行内(start < width)，或 start==end 且在行内。
func (r *Row) splice(start, end int, ins []rune) {
	i, off := r.locate(start)
	if i >= len(r.dataRune) {
		r.dataRune = append(append(r.dataRune, blanks(off)...), ins...)
		r.noteWide(ins)
		return
	}
	if !r.wide && off == 0 && !hasWide(ins) {
		// 快路径：每个 rune 一列，[start,end) 就是下标区间，原地替换。
		end = min(end, len(r.dataRune))
		r.dataRune = slices.Replace(r.dataRune, start, end, ins...)
		return
	}
	pos := start - off
	out := make([]rune, 0, len(r.dataRune)+len(ins)+2)
	out = append(out, r.dataRune[:i]...)
	out = append(out, blanks(off)...)
	out = append(out, ins...)
	j := i
	for j < len(r.dataRune) && pos < end {
		pos += runewidth.RuneWidth(r.dataRune[j])
		j++
	}
	out = append(out, blanks(pos-end)...)
	// 被替换字符携带的组合符一并丢弃。
	for j < len(r.dataRune) && runewidth.RuneWidth(r.dataRune[j]) == 0 {
		j++
	}
	out = append(out, r.dataRune[j:]...)
	r.dataRune = out
	r.noteWide(ins)
}

// hasWide 报告 rs 中是否含宽度不是 1 的字符。
func hasWide(rs []rune) bool {
	for _, c := range rs {
		if c >= 0x80 && runewidth.RuneWidth(c) != 1 {
			return true
		}
	}
	return false
}

func (r *Row) noteWide(rs []rune) {
	if !r.wide && hasWide(rs) {
		r.wide = true
	}
}

// put 在 x 列写入一个字符(覆盖语义)，行尾之外的空隙以空格补齐。
func (r *Row) put(x int, c rune) {
	w := runewidth.RuneWidth(c)
	if w != 1 {
		r.wide = true
	}
	switch {
	case w <= 0:
		if len(r.dataRune) > maxCols {
			return
		}
		i, _ := r.locate(x)
		r.dataRune = append(r.dataRune, 0)
		copy(r.dataRune[i+1:], r.dataRune[i:])
		r.dataRune[i] = c
	default:
		i, off := r.locate(x)
		if i >= len(r.dataRune) {
			r.dataRune = append(r.dataRune, blanks(off)...)
			r.dataRune = append(r.dataRune, c)
		} else {
			r.splice(x, x+w, []rune{c})
		}
	}
	r.addTipRune(c)
}

// truncate 擦除 x 列及其右侧(EL 0)。
func (r *Row) truncate(x int) {
	i, off := r.locate(x)
	if i >= len(r.dataRune) {
		return
	}
	r.dataRune = append(r.dataRune[:i], blanks(off)...)
}

// blankRange 把行内 [start,end) 列写成空格，不会扩展行尾(EL 1)。
func (r *Row) blankRange(start, end int) {
	end = min(end, r.width())
	if start < 0 || start >= end {
		return
	}
	r.splice(start, end, blanks(end-start))
}

// eraseChars 擦除 x 列起的 n 个单元格而不移动后面的内容(ECH)。
// 擦除范围覆盖到行尾时等价于截断。
func (r *Row) eraseChars(x, n int) {
	w := r.width()
	if x >= w || n <= 0 {
		return
	}
	if x+n >= w {
		r.truncate(x)
		return
	}
	r.blankRange(x, x+n)
}

// deleteChars 删除 x 列起的 n 个单元格，右侧内容左移(DCH)。
func (r *Row) deleteChars(x, n int) {
	end := min(x+n, r.width())
	if x >= end {
		return
	}
	r.splice(x, end, nil)
}

// insertBlank 在 x 列插入 n 个空白，右侧内容右移并截断到 maxCols(ICH)。
func (r *Row) insertBlank(x, n int) {
	if x >= r.width() || n <= 0 {
		return
	}
	r.splice(x, x, blanks(min(n, maxCols)))
	r.capWidth(maxCols)
}

// capWidth 丢弃超出 limit 列的内容。
func (r *Row) capWidth(limit int) {
	if !r.wide {
		if len(r.dataRune) > limit {
			r.dataRune = r.dataRune[:limit]
		}
		return
	}
	pos := 0
	for i, c := range r.dataRune {
		pos += runewidth.RuneWidth(c)
		if pos > limit {
			r.dataRune = r.dataRune[:i]
			return
		}
	}
}

func (r *Row) startRecord() {
	r.tipRecord = true
	r.tipRune = make([]rune, 0, 100)
}

func (r *Row) stopRecord() {
	if !r.tipRecord {
		r.tipRune = make([]rune, 0, 100)
	}
	r.tipRecord = false
}

func (r *Row) addTipRune(code rune) {
	if r.tipRecord && len(r.tipRune) < maxCols {
		r.tipRune = append(r.tipRune, code)
	}
}
