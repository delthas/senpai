package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

type Completion struct {
	Text      []rune
	CursorIdx int
}

// Editor is the text field where the user writes messages and commands.
type Editor struct {
	// text contains the written runes. An empty slice means no text is written.
	text [][]rune

	lineIdx int

	// textWidth[i] contains the width of string(text[:i]). Therefore
	// len(textWidth) is always strictly greater than 0 and textWidth[0] is
	// always 0.
	textWidth []int

	// cursorIdx is the index in text of the placement of the cursor, or is
	// equal to len(text) if the cursor is at the end.
	cursorIdx int

	// offsetIdx is the number of elements of text that are skipped when
	// rendering.
	offsetIdx int

	// width is the width of the screen.
	width int

	autoComplete func(cursorIdx int, text []rune) []Completion
	autoCache    []Completion
	autoCacheIdx int

	backsearch        bool
	backsearchPattern []rune // pre-lowercased
	backsearchIdx     int
}

// NewEditor returns a new Editor.
// Call Resize() once before using it.
func NewEditor(autoComplete func(cursorIdx int, text []rune) []Completion) Editor {
	return Editor{
		text:         [][]rune{{}},
		textWidth:    []int{0},
		autoComplete: autoComplete,
	}
}

func (e *Editor) Resize(width int) {
	if width < e.width {
		e.cursorIdx = 0
		e.offsetIdx = 0
		e.autoCache = nil
		e.backsearchEnd()
	}
	e.width = width
}

// Content result must not be modified.
func (e *Editor) Content() []rune {
	return e.text[e.lineIdx]
}

func (e *Editor) TextLen() int {
	return len(e.text[e.lineIdx])
}

func (e *Editor) PutRune(r rune) {
	e.autoCache = nil
	lowerRune := runeToLower(r)
	if e.backsearch && e.cursorIdx < e.TextLen() {
		lowerNext := runeToLower(e.text[e.lineIdx][e.cursorIdx])
		if lowerRune == lowerNext {
			e.right()
			e.backsearchPattern = append(e.backsearchPattern, lowerRune)
			return
		}
	}
	e.putRune(r)
	e.right()
	if e.backsearch {
		wasEmpty := len(e.backsearchPattern) == 0
		e.backsearchPattern = append(e.backsearchPattern, lowerRune)
		if wasEmpty {
			clearLine := e.lineIdx == len(e.text)-1
			e.backsearchUpdate(e.lineIdx - 1)
			if clearLine && e.lineIdx < len(e.text)-1 {
				e.text = e.text[:len(e.text)-1]
			}
		} else {
			e.backsearchUpdate(e.lineIdx)
		}
	}
}

func (e *Editor) putRune(r rune) {
	e.text[e.lineIdx] = append(e.text[e.lineIdx], ' ')
	copy(e.text[e.lineIdx][e.cursorIdx+1:], e.text[e.lineIdx][e.cursorIdx:])
	e.text[e.lineIdx][e.cursorIdx] = r

	rw := runeWidth(r)
	tw := e.textWidth[len(e.textWidth)-1]
	e.textWidth = append(e.textWidth, tw+rw)
	for i := e.cursorIdx + 1; i < len(e.textWidth); i++ {
		e.textWidth[i] = rw + e.textWidth[i-1]
	}
}

func (e *Editor) RemRune() (ok bool) {
	ok = 0 < e.cursorIdx
	if !ok {
		return
	}
	e.remRuneAt(e.cursorIdx - 1)
	e.left()
	e.autoCache = nil
	if e.backsearch {
		if e.TextLen() == 0 {
			e.backsearchEnd()
		} else {
			e.backsearchPattern = e.backsearchPattern[:len(e.backsearchPattern)-1]
			e.backsearchUpdate(e.lineIdx)
		}
	}
	return
}

func (e *Editor) RemRuneForward() (ok bool) {
	ok = e.cursorIdx < len(e.text[e.lineIdx])
	if !ok {
		return
	}
	e.remRuneAt(e.cursorIdx)
	e.autoCache = nil
	e.backsearchEnd()
	return
}

func (e *Editor) remRuneAt(idx int) {
	// TODO avoid looping twice
	rw := e.textWidth[idx+1] - e.textWidth[idx]
	for i := idx + 1; i < len(e.textWidth); i++ {
		e.textWidth[i] -= rw
	}
	copy(e.textWidth[idx+1:], e.textWidth[idx+2:])
	e.textWidth = e.textWidth[:len(e.textWidth)-1]

	copy(e.text[e.lineIdx][idx:], e.text[e.lineIdx][idx+1:])
	e.text[e.lineIdx] = e.text[e.lineIdx][:len(e.text[e.lineIdx])-1]
}

func (e *Editor) RemWord() (ok bool) {
	ok = 0 < e.cursorIdx
	if !ok {
		return
	}

	line := e.text[e.lineIdx]

	// To allow doing something like this (| is the cursor):
	// Hello world|
	// Hello |
	// |
	for e.cursorIdx > 0 && line[e.cursorIdx-1] == ' ' {
		e.remRuneAt(e.cursorIdx - 1)
		e.left()
	}

	for i := e.cursorIdx - 1; i >= 0; i -= 1 {
		if line[i] == ' ' {
			break
		}
		e.remRuneAt(i)
		e.left()
	}

	e.autoCache = nil
	e.backsearchEnd()
	return
}

func (e *Editor) Flush() (content string) {
	content = string(e.text[e.lineIdx])
	if len(e.text[len(e.text)-1]) == 0 {
		e.lineIdx = len(e.text) - 1
	} else {
		e.lineIdx = len(e.text)
		e.text = append(e.text, []rune{})
	}
	e.textWidth = e.textWidth[:1]
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
	return
}

func (e *Editor) Clear() bool {
	if e.TextLen() == 0 {
		return false
	}
	e.text[e.lineIdx] = []rune{}
	e.textWidth = e.textWidth[:1]
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	return true
}

func (e *Editor) Set(text string) {
	r := []rune(text)
	e.text[e.lineIdx] = r
	e.cursorIdx = len(r)
	e.computeTextWidth()
	e.offsetIdx = 0
	for e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
		e.offsetIdx++
	}
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) Right() {
	e.right()
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) right() {
	if e.cursorIdx == len(e.text[e.lineIdx]) {
		return
	}
	e.cursorIdx++
	if e.width <= e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx] {
		e.offsetIdx += 16
		max := len(e.text[e.lineIdx]) - 1
		if max < e.offsetIdx {
			e.offsetIdx = max
		}
	}
}

func (e *Editor) RightWord() {
	line := e.text[e.lineIdx]

	if e.cursorIdx == len(line) {
		return
	}

	for e.cursorIdx < len(line) && line[e.cursorIdx] == ' ' {
		e.Right()
	}
	for i := e.cursorIdx; i < len(line) && line[i] != ' '; i += 1 {
		e.Right()
	}
}

func (e *Editor) Left() {
	e.left()
	e.backsearchEnd()
}

func (e *Editor) left() {
	if e.cursorIdx == 0 {
		return
	}
	e.cursorIdx--
	if e.cursorIdx <= e.offsetIdx {
		e.offsetIdx -= 16
		if e.offsetIdx < 0 {
			e.offsetIdx = 0
		}
	}
}

func (e *Editor) LeftWord() {
	if e.cursorIdx == 0 {
		return
	}

	line := e.text[e.lineIdx]

	for e.cursorIdx > 0 && line[e.cursorIdx-1] == ' ' {
		e.left()
	}
	for i := e.cursorIdx - 1; i >= 0 && line[i] != ' '; i -= 1 {
		e.left()
	}

	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) Home() {
	if e.cursorIdx == 0 {
		return
	}
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) End() {
	if e.cursorIdx == len(e.text[e.lineIdx]) {
		return
	}
	e.cursorIdx = len(e.text[e.lineIdx])
	for e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
		e.offsetIdx++
	}
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) Up() {
	if e.lineIdx == 0 {
		return
	}
	e.lineIdx--
	e.computeTextWidth()
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
	e.End()
}

func (e *Editor) Down() {
	if e.lineIdx == len(e.text)-1 {
		if len(e.text[e.lineIdx]) == 0 {
			return
		}
		e.Flush()
		return
	}
	e.lineIdx++
	e.computeTextWidth()
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
	e.End()
}

func (e *Editor) AutoComplete(offset int) (ok bool) {
	if e.autoCache == nil {
		e.autoCache = e.autoComplete(e.cursorIdx, e.text[e.lineIdx])
		if len(e.autoCache) == 0 {
			e.autoCache = nil
			return false
		}
		e.autoCacheIdx = 0
	} else {
		e.autoCacheIdx = (e.autoCacheIdx + len(e.autoCache) + offset) % len(e.autoCache)
	}

	e.text[e.lineIdx] = e.autoCache[e.autoCacheIdx].Text
	e.cursorIdx = e.autoCache[e.autoCacheIdx].CursorIdx
	e.computeTextWidth()
	if len(e.textWidth) <= e.offsetIdx {
		e.offsetIdx = 0
	}
	for e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
		e.offsetIdx++
	}

	e.backsearchEnd()
	return true
}

func (e *Editor) BackSearch() {
	clearLine := false
	if !e.backsearch {
		e.backsearch = true
		e.backsearchPattern = []rune(strings.ToLower(string(e.text[e.lineIdx])))
		clearLine = e.lineIdx == len(e.text)-1
	}
	e.backsearchUpdate(e.lineIdx - 1)
	if clearLine && e.lineIdx < len(e.text)-1 {
		e.text = e.text[:len(e.text)-1]
	}
}

func (e *Editor) backsearchUpdate(start int) {
	if len(e.backsearchPattern) == 0 {
		return
	}
	pattern := string(e.backsearchPattern)
	for i := start; i >= 0; i-- {
		if match := strings.Index(strings.ToLower(string(e.text[i])), pattern); match >= 0 {
			e.lineIdx = i
			e.computeTextWidth()
			e.cursorIdx = runeOffset(string(e.text[i]), match) + len(e.backsearchPattern)
			e.offsetIdx = 0
			for e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
				e.offsetIdx++
			}
			e.autoCache = nil
			break
		}
	}
}

func (e *Editor) backsearchEnd() {
	e.backsearch = false
}

func (e *Editor) computeTextWidth() {
	e.textWidth = e.textWidth[:1]
	rw := 0
	for _, r := range e.text[e.lineIdx] {
		rw += runeWidth(r)
		e.textWidth = append(e.textWidth, rw)
	}
}

func (e *Editor) Draw(screen tcell.Screen, x0, y int) {
	st := tcell.StyleDefault

	x := x0
	i := e.offsetIdx

	for i < len(e.text[e.lineIdx]) && x < x0+e.width {
		r := e.text[e.lineIdx][i]
		s := st
		if e.backsearch && i < e.cursorIdx && i >= e.cursorIdx-len(e.backsearchPattern) {
			s = s.Underline(true)
		}
		screen.SetContent(x, y, r, nil, s)
		x += runeWidth(r)
		i++
	}

	for x < x0+e.width {
		screen.SetContent(x, y, ' ', nil, st)
		x++
	}

	cursorX := x0 + e.textWidth[e.cursorIdx] - e.textWidth[e.offsetIdx]
	screen.ShowCursor(cursorX, y)
}

// runeOffset returns the lowercase version of a rune
// TODO: len(strings.ToLower(string(r))) == len(strings.ToUpper(string(r))) for all x?
func runeToLower(r rune) rune {
	return []rune(strings.ToLower(string(r)))[0]
}

// runeOffset returns the rune index of the rune starting at byte i in string s
func runeOffset(s string, pos int) int {
	n := 0
	for i := range s {
		if i >= pos {
			return n
		}
		n++
	}
	return n
}
