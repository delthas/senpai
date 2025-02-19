package ui

import (
	"strings"

	"git.sr.ht/~rockorager/vaxis"
)

type Completion struct {
	Async   any // not nil if this completion is asynchronously loading values
	AsyncID int

	StartIdx  int
	EndIdx    int
	Text      []rune
	Display   []rune
	CursorIdx int // in runes
}

type editorLine struct {
	runes    []rune
	clusters []int
}

func newEditorLine() editorLine {
	return editorLine{
		runes:    []rune{},
		clusters: []int{0},
	}
}

func (l *editorLine) copy() editorLine {
	return editorLine{
		runes:    append([]rune{}, l.runes...),
		clusters: append([]int{}, l.clusters...),
	}
}

// Editor is the text field where the user writes messages and commands.
type Editor struct {
	ui *UI

	// text contains the written runes. An empty slice means no text is written.
	text []editorLine

	// history contains the original content of each previously sent message.
	// It gets out of sync with text when history is modified à la readline,
	// then overwrites text when a new message is added.
	history []editorLine

	lineIdx int

	// textWidth[i] contains the width of text.runes[:text.clusters[i]]. Therefore
	// len(textWidth) is always strictly greater than 0 and textWidth[0] is
	// always 0.
	textWidth []int

	// cursorIdx is the index of clusters in text of the placement of the cursor.
	cursorIdx int

	// offsetIdx is the number of clusters in text that are skipped when rendering.
	offsetIdx int

	// width is the width of the screen.
	width int

	autoCache    []Completion
	autoCacheIdx int

	backsearch        bool
	backsearchPattern []rune // pre-lowercased

	// oldest (lowest) index in text of lines that were changed.
	// used as an optimization to reduce copying when flushing lines.
	oldestTextChange int
}

// NewEditor returns a new Editor.
// Call Resize() once before using it.
func NewEditor(ui *UI) Editor {
	return Editor{
		ui:        ui,
		text:      []editorLine{newEditorLine()},
		history:   []editorLine{},
		textWidth: []int{0},
	}
}

func (e *Editor) Resize(width int) {
	if width < e.width {
		// Reset cursor to the same size, to recompute offsetIdx
		e.setCursor(e.textWidth[e.cursorIdx])
	}
	e.width = width
}

// Content result must not be modified.
func (e *Editor) Content() []rune {
	return e.text[e.lineIdx].runes
}

func (e *Editor) Empty() bool {
	return len(e.text[e.lineIdx].runes) == 0
}

// recompute must be called when runes is changed, to update
// clusters, textWidth.
func (e *Editor) recompute() {
	c := make([]int, 0, len(e.text[e.lineIdx].runes)+1)
	w := make([]int, 0, len(e.text[e.lineIdx].runes)+1)
	nc := 0
	nw := 0
	for _, g := range vaxis.Characters(string(e.text[e.lineIdx].runes)) {
		c = append(c, nc)
		w = append(w, nw)
		nc += len([]rune(g.Grapheme))
		nw += stringWidth(e.ui.vx, g.Grapheme)
	}
	c = append(c, nc)
	w = append(w, nw)
	e.text[e.lineIdx].clusters = c
	e.textWidth = w
}

// setCursor sets cursorIdx to the (grapheme cluster) offset
// corresponding to the passed rune offset runeIdx, rounding up
// to the next grapheme cluster as needed.
func (e *Editor) setCursor(runeIdx int) {
	for i, o := range e.text[e.lineIdx].clusters {
		if o >= runeIdx {
			e.cursorIdx = i
			break
		}
	}
	if e.width <= e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx] {
		e.offsetIdx += 16
		// If we went too far right, go back enough to show one cluster.
		max := len(e.text[e.lineIdx].clusters) - 2
		if max < e.offsetIdx {
			e.offsetIdx = max
		}
	}
}

func (e *Editor) PutRune(r rune) {
	e.autoCache = nil
	lowerRune := runeToLower(r)
	if e.backsearch && e.cursorIdx < len(e.text[e.lineIdx].clusters)-1 {
		lowerNext := runeToLower(e.text[e.lineIdx].runes[e.text[e.lineIdx].clusters[e.cursorIdx]])
		if lowerRune == lowerNext {
			e.right()
			e.backsearchPattern = append(e.backsearchPattern, lowerRune)
			return
		}
	}
	e.putRune(r)
	if e.backsearch {
		wasEmpty := len(e.backsearchPattern) == 0
		e.backsearchPattern = append(e.backsearchPattern, lowerRune)
		if wasEmpty {
			e.backsearchUpdate(e.lineIdx - 1)
		} else {
			e.backsearchUpdate(e.lineIdx)
		}
	}
}

// putRune inserts a rune at the current cursor position,
// then updates moves the cursor position after that rune.
// (If inserting the rune merged a grapheme cluster, we
// move the cursor after that cluster.)
func (e *Editor) putRune(r rune) {
	ci := e.text[e.lineIdx].clusters[e.cursorIdx]
	e.text[e.lineIdx].runes = append(e.text[e.lineIdx].runes, ' ')
	copy(e.text[e.lineIdx].runes[ci+1:], e.text[e.lineIdx].runes[ci:])
	e.text[e.lineIdx].runes[ci] = r

	e.recompute()
	e.setCursor(ci + 1)

	e.bumpOldestChange()
}

func (e *Editor) RemCluster() (ok bool) {
	ok = 0 < e.cursorIdx
	if !ok {
		return
	}
	e.remClusterAt(e.cursorIdx - 1)
	e.left()
	e.autoCache = nil
	if e.backsearch {
		if e.Empty() || len(e.backsearchPattern) == 0 {
			e.backsearchEnd()
		} else {
			e.backsearchPattern = e.backsearchPattern[:len(e.backsearchPattern)-1]
			e.backsearchUpdate(e.lineIdx)
		}
	}
	return
}

func (e *Editor) RemClusterForward() (ok bool) {
	ok = e.cursorIdx < len(e.text[e.lineIdx].clusters)-1
	if !ok {
		return
	}
	e.remClusterAt(e.cursorIdx)
	e.autoCache = nil
	e.backsearchEnd()
	return
}

func (e *Editor) remClusterAt(idx int) {
	rs := e.text[e.lineIdx].clusters[idx]
	re := e.text[e.lineIdx].clusters[idx+1]
	copy(e.text[e.lineIdx].runes[rs:], e.text[e.lineIdx].runes[re:])
	e.text[e.lineIdx].runes = e.text[e.lineIdx].runes[:len(e.text[e.lineIdx].runes)-(re-rs)]

	e.recompute()
	e.bumpOldestChange()
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
	for e.cursorIdx > 0 && line.runes[line.clusters[e.cursorIdx-1]] == ' ' {
		e.remClusterAt(e.cursorIdx - 1)
		e.left()
	}

	for i := e.cursorIdx - 1; i >= 0; i -= 1 {
		if line.runes[line.clusters[i]] == ' ' {
			break
		}
		e.remClusterAt(i)
		e.left()
	}

	e.autoCache = nil
	e.backsearchEnd()
	return
}

func (e *Editor) Flush() string {
	l := e.text[e.lineIdx]
	content := string(l.runes)
	if len(content) > 0 {
		e.history = append(e.history, l.copy())
	}
	for i, line := range e.history[e.oldestTextChange:] {
		i := i + e.oldestTextChange
		e.text[i] = line.copy()
	}
	if len(content) > 0 {
		e.text = append(e.text, newEditorLine())
	} else {
		e.text[len(e.text)-1] = newEditorLine()
	}
	e.lineIdx = len(e.text) - 1
	e.textWidth = e.textWidth[:1]
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
	e.oldestTextChange = len(e.text) - 1
	return content
}

func (e *Editor) Clear() bool {
	if e.Empty() {
		return false
	}
	e.text[e.lineIdx] = newEditorLine()
	e.bumpOldestChange()
	e.textWidth = e.textWidth[:1]
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	return true
}

func (e *Editor) Set(text string) {
	r := []rune(text)
	e.text[e.lineIdx].runes = r
	e.recompute()
	e.bumpOldestChange()
	e.cursorIdx = len(e.text[e.lineIdx].clusters) - 1
	e.offsetIdx = 0
	for e.offsetIdx < len(e.textWidth)-1 && e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
		e.offsetIdx++
	}
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) Enter() bool {
	if e.autoCache != nil {
		return e.AutoComplete()
	}
	return false
}

func (e *Editor) Right() {
	e.right()
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) right() {
	if e.cursorIdx == len(e.text[e.lineIdx].clusters)-1 {
		return
	}
	e.cursorIdx++
	if e.width <= e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx] {
		e.offsetIdx += 16
		// If we went too far right, go back enough to show one cluster.
		max := len(e.text[e.lineIdx].clusters) - 2
		if max < e.offsetIdx {
			e.offsetIdx = max
		}
	}
}

func (e *Editor) RightWord() {
	line := e.text[e.lineIdx]

	if e.cursorIdx == len(line.clusters)-1 {
		return
	}

	for e.cursorIdx < len(line.clusters)-1 && line.runes[line.clusters[e.cursorIdx]] == ' ' {
		e.Right()
	}
	for i := e.cursorIdx; i < len(line.clusters)-1 && line.runes[line.clusters[i]] != ' '; i++ {
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

	for e.cursorIdx > 0 && line.runes[line.clusters[e.cursorIdx-1]] == ' ' {
		e.left()
	}
	for i := e.cursorIdx - 1; i >= 0 && line.runes[line.clusters[i]] != ' '; i-- {
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
	if e.cursorIdx == len(e.text[e.lineIdx].clusters)-1 {
		return
	}
	e.cursorIdx = len(e.text[e.lineIdx].clusters) - 1
	for e.offsetIdx < len(e.textWidth)-1 && e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
		e.offsetIdx++
	}
	e.autoCache = nil
	e.backsearchEnd()
}

func (e *Editor) Up() {
	if e.autoCache != nil {
		e.autoCacheIdx = (e.autoCacheIdx + 1) % len(e.autoCache)
		return
	}
	if e.lineIdx == 0 {
		return
	}
	e.lineIdx--
	e.recompute()
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
	e.End()
}

func (e *Editor) Down() {
	if e.autoCache != nil {
		e.autoCacheIdx = (e.autoCacheIdx + len(e.autoCache) - 1) % len(e.autoCache)
		return
	}
	if e.lineIdx == len(e.text)-1 {
		e.Flush()
		return
	}
	e.lineIdx++
	e.recompute()
	e.cursorIdx = 0
	e.offsetIdx = 0
	e.autoCache = nil
	e.backsearchEnd()
	e.End()
}

func (e *Editor) AutoComplete() (ok bool) {
	if e.autoCache == nil {
		e.autoCache = e.ui.config.AutoComplete(e.text[e.lineIdx].clusters[e.cursorIdx], e.text[e.lineIdx].runes)
		if len(e.autoCache) == 0 {
			e.autoCache = nil
			return false
		}
		e.autoCacheIdx = 0
		if len(e.autoCache) > 1 || e.autoCache[0].Async != nil {
			return false
		}
	}
	if e.autoCache[e.autoCacheIdx].Async != nil {
		return false
	}

	e.text[e.lineIdx].runes = e.autoCache[e.autoCacheIdx].Text
	e.recompute()
	e.bumpOldestChange()
	e.setCursor(e.autoCache[e.autoCacheIdx].CursorIdx)
	if len(e.textWidth) <= e.offsetIdx {
		e.offsetIdx = 0
	}
	for e.offsetIdx < len(e.textWidth)-1 && e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
		e.offsetIdx++
	}
	e.autoCache = nil

	e.backsearchEnd()
	return true
}

func (e *Editor) AsyncCompletions(id int, cs []Completion) {
	for i := 0; i < len(e.autoCache); i++ {
		c := &e.autoCache[i]
		if c.AsyncID != id {
			continue
		}
		a := append([]Completion{}, e.autoCache[:i]...)
		a = append(a, cs...)
		a = append(a, e.autoCache[i+1:]...)
		e.autoCache = a
		break
	}
	if len(e.autoCache) == 0 {
		e.autoCache = nil
		e.autoCacheIdx = 0
	} else if e.autoCacheIdx >= len(e.autoCache) {
		e.autoCacheIdx = len(e.autoCache) - 1
	}
}

func (e *Editor) BackSearch() {
	if !e.backsearch {
		e.backsearch = true
		e.backsearchPattern = []rune(strings.ToLower(string(e.text[e.lineIdx].runes)))
	}
	e.backsearchUpdate(e.lineIdx - 1)
}

func (e *Editor) backsearchUpdate(start int) {
	if len(e.backsearchPattern) == 0 {
		return
	}
	pattern := string(e.backsearchPattern)
	for i := start; i >= 0; i-- {
		if match := strings.Index(strings.ToLower(string(e.text[i].runes)), pattern); match >= 0 {
			e.lineIdx = i
			e.recompute()
			e.setCursor(runeOffset(string(e.text[i].runes), match) + len(e.backsearchPattern))
			e.offsetIdx = 0
			for e.offsetIdx < len(e.textWidth)-1 && e.width < e.textWidth[e.cursorIdx]-e.textWidth[e.offsetIdx]+16 {
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

// call this everytime e.text is modified
func (e *Editor) bumpOldestChange() {
	if e.oldestTextChange > e.lineIdx {
		e.oldestTextChange = e.lineIdx
	}
}

func (e *Editor) Draw(vx *Vaxis, x0, y int, hint string) {
	var st vaxis.Style

	x := x0
	i := e.text[e.lineIdx].clusters[e.offsetIdx]
	text := e.text[e.lineIdx].runes
	showCursor := true

	if len(text) == 0 && len(hint) > 0 && !e.backsearch {
		i = 0
		text = []rune(hint)
		st.Foreground = e.ui.config.Colors.Status
		showCursor = false
	}

	autoStart := -1
	autoEnd := -1
	autoX := x0
	if e.autoCache != nil {
		autoStart = e.autoCache[e.autoCacheIdx].StartIdx
		autoEnd = e.autoCache[e.autoCacheIdx].EndIdx
	}

	ci := e.text[e.lineIdx].clusters[e.cursorIdx]
	for i < len(text) {
		r := text[i:]
		s := st
		if e.backsearch && i < ci && i >= ci-len(e.backsearchPattern) {
			s.UnderlineStyle = vaxis.UnderlineSingle
		}
		if i >= autoStart && i < autoEnd {
			s.UnderlineStyle = vaxis.UnderlineSingle
		}
		if i == autoStart {
			autoX = x
		}
		if r[0] == '\n' {
			s.Attribute |= vaxis.AttrBold
			s.Foreground = ColorRed
			r = []rune{'↲'}
		}
		dx, di := printCluster(vx, x, y, x0+e.width, r, s)
		if di == 0 {
			break
		}
		x += dx
		i += di
	}
	if i == autoStart {
		autoX = x
	}

	for x < x0+e.width {
		setCell(vx, x, y, ' ', st)
		x++
	}

	autoCount := y - 2
	if autoCount < 0 {
		autoCount = 0
	} else if autoCount > len(e.autoCache) {
		autoCount = len(e.autoCache)
	} else if autoCount > 10 {
		autoCount = 10
	}
	autoOff := 0
	if len(e.autoCache) > autoCount {
		autoOff = e.autoCacheIdx - autoCount/2
		if autoOff < 0 {
			autoOff = 0
		} else if autoOff > len(e.autoCache)-autoCount {
			autoOff = len(e.autoCache) - autoCount
		}
	}

	for ci, completion := range e.autoCache[autoOff : autoOff+autoCount] {
		display := completion.Display
		if display == nil {
			display = completion.Text[completion.StartIdx:]
		}
		var unselectable bool
		if completion.Async != nil {
			display = []rune("Loading...")
			unselectable = true
		} else if (ci == 0 && autoOff > 0) || (ci == autoCount-1 && autoOff+autoCount < len(e.autoCache)) {
			display = []rune("...")
			unselectable = true
		}

		x := autoX
		y := y - ci - 1
		i := 0
		for i < len(display) {
			s := vaxis.Style{
				Attribute: vaxis.AttrReverse,
			}
			if ci+autoOff == e.autoCacheIdx {
				s.Attribute |= vaxis.AttrBold
			} else {
				s.Attribute |= vaxis.AttrDim
			}
			if unselectable {
				s.Attribute |= vaxis.AttrItalic
			}
			dx, di := printCluster(vx, x, y, x0+e.width, display[i:], s)
			if di == 0 {
				break
			}
			x += dx
			i += di
		}
	}

	if showCursor {
		cursorX := x0 + e.textWidth[e.cursorIdx] - e.textWidth[e.offsetIdx]
		vx.ShowCursor(cursorX, y, vaxis.CursorBeam)
	} else {
		vx.HideCursor()
	}
}

// runeOffset returns the lowercase version of a rune
// TODO: len(strings.ToLower(string(r))) == len(strings.ToUpper(string(r))) for all x?
func runeToLower(r rune) rune {
	return []rune(strings.ToLower(string(r)))[0]
}

// runeOffset returns the rune index of the rune starting at byte pos in string s
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
