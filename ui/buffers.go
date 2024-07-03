package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"git.sr.ht/~delthas/senpai/events"
)

const Overlay = "/overlay"

func IsSplitRune(r rune) bool {
	return r == ' ' || r == '\t'
}

type point struct {
	X     int // in cells
	I     int // in bytes
	Split bool
}

type NotifyType int

const (
	NotifyNone NotifyType = iota
	NotifyUnread
	NotifyHighlight
)

type optional int

const (
	optionalUnset optional = iota
	optionalFalse
	optionalTrue
)

type Line struct {
	At        time.Time
	Head      string
	Body      StyledString
	HeadColor vaxis.Color
	Notify    NotifyType
	Highlight bool
	Readable  bool
	Mergeable bool
	Data      interface{}

	splitPoints []point
	width       int
	newLines    []int
}

func (l *Line) IsZero() bool {
	return l.Body.string == ""
}

func (l *Line) computeSplitPoints(vx *Vaxis) {
	if l.splitPoints == nil {
		l.splitPoints = []point{}
	}

	width := 0
	lastWasSplit := false
	l.splitPoints = l.splitPoints[:0]

	for i, r := range l.Body.string {
		curIsSplit := IsSplitRune(r)

		if i == 0 || lastWasSplit != curIsSplit {
			l.splitPoints = append(l.splitPoints, point{
				X:     width,
				I:     i,
				Split: curIsSplit,
			})
		}

		lastWasSplit = curIsSplit
		width += runeWidth(vx, r)
	}

	if !lastWasSplit {
		l.splitPoints = append(l.splitPoints, point{
			X:     width,
			I:     len(l.Body.string),
			Split: true,
		})
	}
}

// NewLines returns the offsets, in bytes, where the line should be split.
func (l *Line) NewLines(vx *Vaxis, width int) []int {
	// Beware! This function was made by your local Test Driven Developper™ who
	// doesn't understand one bit of this function and how it works (though it
	// might not work that well if you're here...).  The code below is thus very
	// cryptic and not well-structured.  However, I'm going to try to explain
	// some of those lines!

	if l.width == width {
		return l.newLines
	}
	if l.newLines == nil {
		l.newLines = []int{}
	}
	l.newLines = l.newLines[:0]
	l.width = width

	x := 0
	for i := 1; i < len(l.splitPoints); i++ {
		// Iterate through the split points 2 by 2.  Split points are placed at
		// the beginning of whitespace (see IsSplitRune) and at the beginning
		// of non-whitespace. Iterating on 2 points each time, sp1 and sp2,
		// allows consideration of a "word" of (non-)whitespace.
		// Split points have the index I in the string and the width X of the
		// screen.  Finally, the Split field is set to true if the split point
		// is at the beginning of a whitespace.

		// Below, "row" means a line in the terminal, while "line" means (l *Line).

		sp1 := l.splitPoints[i-1]
		sp2 := l.splitPoints[i]

		if 0 < len(l.newLines) && x == 0 && sp1.Split {
			// Except for the first row, let's skip the whitespace at the start
			// of the row.
		} else if !sp1.Split && sp2.X-sp1.X == width {
			// Some word occupies the width of the terminal, lets place a
			// newline at the PREVIOUS split point (i-2, which is whitespace)
			// ONLY if there isn't already one.
			if 1 < i && 0 < len(l.newLines) && l.newLines[len(l.newLines)-1] != l.splitPoints[i-2].I {
				l.newLines = append(l.newLines, l.splitPoints[i-2].I)
			}
			// and also place a newline after the word.
			x = 0
			l.newLines = append(l.newLines, sp2.I)
		} else if sp2.X-sp1.X+x < width {
			// It fits.  Advance the X coordinate with the width of the word.
			x += sp2.X - sp1.X
		} else if sp2.X-sp1.X+x == width {
			// It fits, but there is no more space in the row.
			x = 0
			l.newLines = append(l.newLines, sp2.I)
		} else if sp1.Split && width < sp2.X-sp1.X {
			// Some whitespace occupies a width larger than the terminal's.
			x = 0
			l.newLines = append(l.newLines, sp1.I)
		} else if width < sp2.X-sp1.X {
			// It doesn't fit at all.  The word is longer than the width of the
			// terminal.  In this case, no newline is placed before (like in the
			// 2nd if-else branch).  The for loop is used to place newlines in
			// the word.
			s := l.Body.string[sp1.I:sp2.I]
			j := 0
			for s != "" {
				c, wordWidth := firstCluster(vx, []rune(s))
				if width < x+wordWidth {
					x = 0
					l.newLines = append(l.newLines, sp1.I+j)
				}
				x += wordWidth
				j += len(c)
				s = s[len(c):]
			}
			if x == width {
				// The placement of the word is such that it ends right at the
				// end of the row.
				x = 0
				l.newLines = append(l.newLines, sp2.I)
			}
		} else {
			// So... IIUC this branch would be the same as
			//     else if width < sp2.X-sp1.X+x
			// IE. It doesn't fit, but the word can still be placed on the next
			// row.
			l.newLines = append(l.newLines, sp1.I)
			if sp1.Split {
				x = 0
			} else {
				x = sp2.X - sp1.X
			}
		}
	}

	if 0 < len(l.newLines) && l.newLines[len(l.newLines)-1] == len(l.Body.string) {
		// DROP any newline that is placed at the end of the string because we
		// don't care about those.
		l.newLines = l.newLines[:len(l.newLines)-1]
	}

	return l.newLines
}

type buffer struct {
	netID         string
	netName       string
	title         string
	highlights    int
	notifications []int
	unread        bool
	read          time.Time
	openedOnce    bool

	// This is the "last read" timestamp when the buffer was last focused.
	// If the "last read" timestamp changes while the buffer is focused,
	// the ruler should not move.
	unreadRuler time.Time
	// Whether to draw the unread bar for the current buffer.
	// The goal is to draw the unread bar iff there was at least one unread
	// message when the buffer was opened.
	// The unreadSkip value starts off as optionalUnset, then gets set to
	// either optionalFalse or optionalTrue when a message is received.
	unreadSkip optional

	lines []Line
	topic string

	scrollAmt int // offset in lines from the bottom
	isAtTop   bool
}

type BufferList struct {
	ui *UI

	list    []buffer
	overlay *buffer
	current int
	clicked int

	tlInnerWidth int
	tlHeight     int
	textWidth    int

	filterBuffers      bool
	filterBuffersQuery string // lowercased
}

// NewBufferList returns a new BufferList.
// Call Resize() once before using it.
func NewBufferList(ui *UI) BufferList {
	return BufferList{
		ui:      ui,
		list:    []buffer{},
		clicked: -1,
	}
}

func (bs *BufferList) ResizeTimeline(tlInnerWidth, tlHeight, textWidth int) {
	bs.tlInnerWidth = tlInnerWidth
	bs.tlHeight = tlHeight - 2
	bs.textWidth = textWidth
}

func (bs *BufferList) OpenOverlay() {
	bs.overlay = &buffer{
		netID:   "",
		netName: "",
		title:   Overlay,
	}
}

func (bs *BufferList) CloseOverlay() {
	bs.overlay = nil
}

func (bs *BufferList) HasOverlay() bool {
	return bs.overlay != nil
}

func (bs *BufferList) To(i int) bool {
	bs.overlay = nil
	if i == bs.current {
		return false
	}
	if 0 <= i {
		bs.current = i
		if len(bs.list) <= bs.current {
			bs.current = len(bs.list) - 1
		}
		bs.clearRead(bs.current)
		b := bs.list[bs.current]
		b.unreadRuler = b.read
		if len(b.lines) > 0 {
			l := b.lines[len(b.lines)-1]
			if !l.At.After(b.unreadRuler) {
				b.unreadSkip = optionalTrue
			} else {
				b.unreadSkip = optionalFalse
			}
		} else {
			b.unreadSkip = optionalUnset
		}
		bs.list[bs.current] = b
		return true
	}
	return false
}

func (bs *BufferList) FilterBuffers(enable bool, query string) {
	bs.filterBuffers = enable
	bs.filterBuffersQuery = strings.ToLower(query)
}

func (bs *BufferList) Next() {
	c := (bs.current + 1) % len(bs.list)
	bs.To(c)
}

func (bs *BufferList) Previous() {
	c := (bs.current - 1 + len(bs.list)) % len(bs.list)
	bs.To(c)
}

func (bs *BufferList) NextUnread() {
	for i := 0; i < len(bs.list); i++ {
		c := (bs.current + i) % len(bs.list)
		if bs.list[c].unread {
			bs.To(c)
			return
		}
	}
}

func (bs *BufferList) PreviousUnread() {
	for i := 0; i < len(bs.list); i++ {
		c := (bs.current - i + len(bs.list)) % len(bs.list)
		if bs.list[c].unread {
			bs.To(c)
			return
		}
	}
}

func (bs *BufferList) Add(netID, netName, title string) (i int, added bool) {
	i = 0
	lTitle := strings.ToLower(title)
	for bi, b := range bs.list {
		if netName == "" && b.netID == netID {
			netName = b.netName
		}
		if netName == "" || b.netName < netName {
			i = bi + 1
			continue
		}
		if b.netName > netName {
			break
		}
		lbTitle := strings.ToLower(b.title)
		if lbTitle < lTitle {
			i = bi + 1
			continue
		}
		if lbTitle == lTitle {
			return i, false
		}
		break
	}

	if i <= bs.current && bs.current < len(bs.list) {
		bs.current++
	}

	b := buffer{
		netID:   netID,
		netName: netName,
		title:   title,
	}
	if i == len(bs.list) {
		bs.list = append(bs.list, b)
	} else {
		bs.list = append(bs.list[:i+1], bs.list[i:]...)
		bs.list[i] = b
	}
	return i, true
}

func (bs *BufferList) Remove(netID, title string) bool {
	idx, b := bs.at(netID, title)
	if b == bs.overlay {
		bs.overlay = nil
		return false
	}
	if idx < 0 {
		return false
	}
	updated := bs.current == idx

	bs.clearRead(idx)
	bs.list = append(bs.list[:idx], bs.list[idx+1:]...)
	if bs.current >= idx {
		bs.current--
	}
	if updated {
		// Force refresh current buffer
		c := bs.current
		bs.current = -1
		bs.To(c)
	}
	return true
}

func (bs *BufferList) RemoveNetwork(netID string) {
	updated := false
	for idx := 0; idx < len(bs.list); idx++ {
		b := &bs.list[idx]
		if b.netID != netID {
			continue
		}
		if idx == bs.current {
			updated = true
		}
		bs.clearRead(idx)
		bs.list = append(bs.list[:idx], bs.list[idx+1:]...)
		if bs.current >= idx {
			bs.current--
		}
		idx--
	}
	if updated {
		// Force refresh current buffer
		c := bs.current
		bs.current = -1
		bs.To(c)
	}
}

func (bs *BufferList) mergeLine(former *Line, addition Line) (keepLine bool) {
	bs.ui.config.MergeLine(former, addition)
	if former.Body.string == "" {
		return false
	}
	former.width = 0
	former.computeSplitPoints(bs.ui.vx)
	return true
}

func (bs *BufferList) AddLine(netID, title string, line Line) {
	_, b := bs.at(netID, title)
	if b == nil {
		return
	}
	current := bs.cur()

	n := len(b.lines)
	line.At = line.At.UTC()

	if !line.Mergeable && b.openedOnce {
		line.Body = line.Body.ParseURLs()
	}

	if line.Mergeable && n != 0 && b.lines[n-1].Mergeable {
		l := &b.lines[n-1]
		if !bs.mergeLine(l, line) {
			b.lines = b.lines[:n-1]
		}
		// TODO change b.scrollAmt if it's not 0 and bs.current is idx.
	} else {
		line.computeSplitPoints(bs.ui.vx)
		b.lines = append(b.lines, line)
		if b == current && 0 < b.scrollAmt {
			b.scrollAmt += len(line.NewLines(bs.ui.vx, bs.textWidth)) + 1
		}
	}

	if line.Notify != NotifyNone && b != current {
		b.unread = true
	}
	if line.Notify == NotifyHighlight && b != current {
		b.highlights++
	}
	if b == current && b.unreadSkip == optionalUnset && len(b.lines) > 0 {
		if b.unreadRuler.IsZero() || !b.lines[len(b.lines)-1].At.After(b.unreadRuler) {
			b.unreadSkip = optionalTrue
		} else {
			b.unreadSkip = optionalFalse
		}
	}
}

func (bs *BufferList) AddLines(netID, title string, before, after []Line) {
	_, b := bs.at(netID, title)
	if b == nil {
		return
	}
	updateRead := b != bs.cur() && !b.read.IsZero()

	lines := make([]Line, 0, len(before)+len(b.lines)+len(after))
	for _, buf := range []*[]Line{&before, &b.lines, &after} {
		for _, line := range *buf {
			if line.Mergeable && len(lines) > 0 && lines[len(lines)-1].Mergeable {
				l := &lines[len(lines)-1]
				if !bs.mergeLine(l, line) {
					lines = lines[:len(lines)-1]
				}
			} else {
				if buf != &b.lines {
					if b.openedOnce {
						line.Body = line.Body.ParseURLs()
					}
					line.computeSplitPoints(bs.ui.vx)
				}
				lines = append(lines, line)
			}

			if updateRead && line.At.After(b.read) {
				if line.Notify != NotifyNone {
					b.unread = true
				}
				if line.Notify == NotifyHighlight {
					b.highlights++
				}
			}
		}
	}
	b.lines = lines
	if b == bs.cur() && b.unreadSkip == optionalUnset && len(b.lines) > 0 {
		if b.unreadRuler.IsZero() || !b.lines[len(b.lines)-1].At.After(b.unreadRuler) {
			b.unreadSkip = optionalTrue
		} else {
			b.unreadSkip = optionalFalse
		}
	}
}

func (bs *BufferList) SetTopic(netID, title string, topic string) {
	_, b := bs.at(netID, title)
	if b == nil {
		return
	}
	b.topic = topic
}

func (bs *BufferList) clearRead(i int) {
	b := &bs.list[i]
	b.highlights = 0
	b.unread = false
	if len(b.notifications) > 0 {
		for _, id := range b.notifications {
			notifyClose(id)
		}
		b.notifications = b.notifications[:0]
	}
}

func (bs *BufferList) SetRead(netID, title string, timestamp time.Time) {
	i, b := bs.at(netID, title)
	if b == nil {
		return
	}
	clearRead := true
	for i := len(b.lines) - 1; i >= 0; i-- {
		line := &b.lines[i]
		if !line.At.After(timestamp) {
			break
		}
		if line.Readable && line.Notify != NotifyNone {
			clearRead = false
			break
		}
	}
	if clearRead {
		bs.clearRead(i)
	}
	if b.read.Before(timestamp) {
		b.read = timestamp
		// For buffers that were focused _before_ we receive any "last read" date.
		if b.unreadRuler.IsZero() {
			b.unreadRuler = b.read
		}
	}
}

func (bs *BufferList) UpdateRead() (netID, title string, timestamp time.Time) {
	b := bs.cur()
	var line *Line
	y := 0
	for i := len(b.lines) - 1; 0 <= i; i-- {
		line = &b.lines[i]
		if y >= b.scrollAmt && line.Readable {
			break
		}
		y += len(line.NewLines(bs.ui.vx, bs.textWidth)) + 1
	}
	if line != nil && line.At.After(b.read) {
		b.read = line.At
		return b.netID, b.title, b.read
	}
	return "", "", time.Time{}
}

func (bs *BufferList) Buffer(i int) (netID, title string, ok bool) {
	if i < 0 || i >= len(bs.list) {
		return
	}
	b := &bs.list[i]
	return b.netID, b.title, true
}

func (bs *BufferList) Current() (netID, title string) {
	b := &bs.list[bs.current]
	return b.netID, b.title
}

func (bs *BufferList) ScrollUp(n int) {
	b := bs.cur()
	if b.isAtTop {
		return
	}
	b.scrollAmt += n
}

func (bs *BufferList) ScrollDown(n int) {
	b := bs.cur()
	b.scrollAmt -= n

	if b.scrollAmt < 0 {
		b.scrollAmt = 0
	}
}

func (bs *BufferList) ScrollUpHighlight() bool {
	b := bs.cur()
	ymin := b.scrollAmt + bs.tlHeight
	y := 0
	for i := len(b.lines) - 1; 0 <= i; i-- {
		line := &b.lines[i]
		if ymin <= y && line.Highlight {
			b.scrollAmt = y - bs.tlHeight + 1
			return true
		}
		y += len(line.NewLines(bs.ui.vx, bs.textWidth)) + 1
	}
	return false
}

func (bs *BufferList) ScrollDownHighlight() bool {
	b := bs.cur()
	yLastHighlight := 0
	y := 0
	for i := len(b.lines) - 1; 0 <= i && y < b.scrollAmt; i-- {
		line := &b.lines[i]
		if line.Highlight {
			yLastHighlight = y
		}
		y += len(line.NewLines(bs.ui.vx, bs.textWidth)) + 1
	}
	b.scrollAmt = yLastHighlight
	return b.scrollAmt != 0
}

// LinesAboveOffset returns a rough approximate of the number of lines
// above the offset (that is, starting from the bottom of the screen,
// up to the first line).
func (bs *BufferList) LinesAboveOffset() int {
	b := bs.cur()
	return len(b.lines) - b.scrollAmt
}

func (bs *BufferList) at(netID, title string) (int, *buffer) {
	if netID == "" && title == Overlay {
		return -1, bs.overlay
	}
	lTitle := strings.ToLower(title)
	for i, b := range bs.list {
		if b.netID == netID && strings.ToLower(b.title) == lTitle {
			return i, &bs.list[i]
		}
	}
	return -1, nil
}

func (bs *BufferList) cur() *buffer {
	if bs.overlay != nil {
		return bs.overlay
	}
	return &bs.list[bs.current]
}

func (bs *BufferList) DrawVerticalBufferList(vx *Vaxis, x0, y0, width, height int, offset *int) {
	if y0+len(bs.list)-*offset < height {
		*offset = y0 + len(bs.list) - height
		if *offset < 0 {
			*offset = 0
		}
	}
	off := bs.VerticalBufferOffset(0, *offset)
	if off < 0 {
		off = len(bs.list)
	}

	width--
	drawVerticalLine(vx, x0+width, y0, height)
	clearArea(vx, x0, y0, width, height)

	indexPadding := 1 + int(math.Ceil(math.Log10(float64(len(bs.list)))))
	y := y0
	for i, b := range bs.list[off:] {
		bi := off + i
		x := x0
		var st vaxis.Style
		if b.unread {
			st.Attribute |= vaxis.AttrBold
			st.Foreground = bs.ui.config.Colors.Unread
		}
		if bi == bs.current || bi == bs.clicked {
			st.Attribute |= vaxis.AttrReverse
		}

		var title string
		if b.title == "" {
			title = b.netName
		} else {
			title = b.title
		}

		if bs.filterBuffers {
			if !strings.Contains(strings.ToLower(title), bs.filterBuffersQuery) {
				continue
			}
			indexSt := st
			indexSt.Foreground = ColorGray
			indexText := fmt.Sprintf("%d:", bi+1)
			printString(vx, &x, y, Styled(indexText, indexSt))
			x = x0 + indexPadding
		}

		if b.title != "" {
			if bi == bs.current || bi == bs.clicked {
				st := vaxis.Style{
					Attribute: vaxis.AttrReverse,
				}
				setCell(vx, x, y, ' ', st)
				setCell(vx, x+1, y, ' ', st)
			}
			x += 2
		}
		title = truncate(vx, title, width-(x-x0), "\u2026")
		printString(vx, &x, y, Styled(title, st))

		if bi == bs.current || bi == bs.clicked {
			st := vaxis.Style{
				Attribute: vaxis.AttrReverse,
			}
			for ; x < x0+width; x++ {
				setCell(vx, x, y, ' ', st)
			}
			setCell(vx, x, y, ' ', st)
			setCell(vx, x, y, '▐', st)
		}

		if b.highlights != 0 {
			highlightSt := st
			highlightSt.Foreground = ColorRed
			highlightSt.Attribute |= vaxis.AttrReverse
			highlightText := fmt.Sprintf(" %d ", b.highlights)
			x = x0 + width - len(highlightText)
			printString(vx, &x, y, Styled(highlightText, highlightSt))
		}

		y++
	}
}

func (bs *BufferList) HorizontalBufferOffset(x int, offset int) int {
	if bs.filterBuffers {
		offset = 0
	}
	i := 0
	for bi, b := range bs.list[offset:] {
		if bs.filterBuffers {
			var title string
			if b.title == "" {
				title = b.netName
			} else {
				title = b.title
			}
			if !strings.Contains(strings.ToLower(title), bs.filterBuffersQuery) {
				continue
			}
		}
		if i > 0 {
			x--
			if x < 0 {
				return -1
			}
		}
		x -= bs.bufferWidth(&b)
		if x < 0 {
			return offset + bi
		}
		i++
	}
	return -1
}

func (bs *BufferList) VerticalBufferOffset(y int, offset int) int {
	if !bs.filterBuffers {
		return offset + y
	}

	for i, b := range bs.list {
		var title string
		if b.title == "" {
			title = b.netName
		} else {
			title = b.title
		}

		if bs.filterBuffers {
			if !strings.Contains(strings.ToLower(title), bs.filterBuffersQuery) {
				continue
			}
		}
		if y == 0 {
			return i
		}
		y--
	}
	return -1
}

func (bs *BufferList) GetLeftMost(screenWidth int) int {
	if len(bs.list) == 0 {
		return 0
	}

	width := 0
	var leftMost int

	for leftMost = bs.current; leftMost >= 0; leftMost-- {
		if leftMost < bs.current {
			width++
		}
		width += bs.bufferWidth(&bs.list[leftMost])
		if width > screenWidth {
			return leftMost + 1 // Went offscreen, need to go one step back
		}
	}

	return 0
}

func (bs *BufferList) bufferWidth(b *buffer) int {
	width := 0
	if b.title == "" {
		width += stringWidth(bs.ui.vx, b.netName)
	} else {
		width += stringWidth(bs.ui.vx, b.title)
	}
	if 0 < b.highlights {
		width += 2 + len(fmt.Sprintf("%d", b.highlights))
	}
	return width
}

func (bs *BufferList) DrawHorizontalBufferList(vx *Vaxis, x0, y0, width int, offset *int) {
	x := width
	for i := len(bs.list) - 1; i >= 0; i-- {
		b := &bs.list[i]
		x--
		x -= bs.bufferWidth(b)
		if x <= 10 {
			break
		}
		if *offset > i {
			*offset = i
		}
	}
	x = x0

	off := bs.HorizontalBufferOffset(0, *offset)
	if off < 0 {
		off = len(bs.list)
	}

	for i, b := range bs.list[off:] {
		i := i + off
		if width <= x-x0 {
			break
		}
		var st vaxis.Style
		if b.unread {
			st.Attribute |= vaxis.AttrBold
			st.Foreground = bs.ui.config.Colors.Unread
		} else if i == bs.current {
			st.UnderlineStyle = vaxis.UnderlineSingle
		}
		if i == bs.clicked {
			st.Attribute |= vaxis.AttrReverse
		}

		var title string
		if b.title == "" {
			st.Attribute |= vaxis.AttrDim
			title = b.netName
		} else {
			title = b.title
		}

		if bs.filterBuffers {
			if !strings.Contains(strings.ToLower(title), bs.filterBuffersQuery) {
				continue
			}
		}

		title = truncate(vx, title, width-x, "\u2026")
		printString(vx, &x, y0, Styled(title, st))

		if 0 < b.highlights {
			st.Foreground = ColorRed
			st.Attribute |= vaxis.AttrReverse
			setCell(vx, x, y0, ' ', st)
			x++
			printNumber(vx, &x, y0, st, b.highlights)
			setCell(vx, x, y0, ' ', st)
			x++
		}
		setCell(vx, x, y0, ' ', vaxis.Style{})
		x++
	}
	for x < width {
		setCell(vx, x, y0, ' ', vaxis.Style{})
		x++
	}
}

func (bs *BufferList) DrawTimeline(ui *UI, x0, y0, nickColWidth int) {
	vx := ui.vx
	clearArea(vx, x0, y0, bs.tlInnerWidth+nickColWidth+9, bs.tlHeight+2)

	b := bs.cur()
	if !b.openedOnce {
		b.openedOnce = true
		for i := 0; i < len(b.lines); i++ {
			b.lines[i].Body = b.lines[i].Body.ParseURLs()
		}
	}

	xTopic := x0
	printString(vx, &xTopic, y0, Styled(b.topic, vaxis.Style{}))
	y0++
	drawHorizontalLine(vx, x0, y0, bs.tlInnerWidth+nickColWidth+9)
	y0++

	if bs.textWidth < bs.tlInnerWidth {
		x0 += (bs.tlInnerWidth - bs.textWidth) / 2
	}

	yi := b.scrollAmt + y0 + bs.tlHeight
	rulerDrawn := b.unreadSkip != optionalFalse || b.unreadRuler.IsZero() || b.title == ""
	for i := len(b.lines) - 1; 0 <= i; i-- {
		if yi < y0 {
			break
		}

		x1 := x0 + 9 + nickColWidth

		line := &b.lines[i]
		nls := line.NewLines(bs.ui.vx, bs.textWidth)

		if !rulerDrawn {
			isRead := !line.At.After(b.unreadRuler)
			if isRead && yi > y0 {
				yi--
				st := vaxis.Style{
					Foreground: ColorGray,
				}
				printIdent(vx, x0+7, yi, nickColWidth, Styled("--", st))
				drawHorizontalLine(vx, x0, yi, 9+nickColWidth+bs.tlInnerWidth)
				rulerDrawn = true
			}
		}

		yi -= len(nls) + 1
		if y0+bs.tlHeight <= yi {
			continue
		}

		var showDate bool
		if i == 0 || yi <= y0 {
			showDate = true
		} else {
			yb, mb, dd := b.lines[i-1].At.Local().Date()
			ya, ma, da := b.lines[i].At.Local().Date()
			showDate = yb != ya || mb != ma || dd != da
		}
		if showDate {
			st := vaxis.Style{
				Attribute: vaxis.AttrBold,
			}
			// as a special case, always draw the first visible message date, even if it is a continuation line
			yd := yi
			if yd < y0 {
				yd = y0
			}
			printDate(vx, x0, yd, st, line.At.Local())
		} else if b.lines[i-1].At.Truncate(time.Minute) != line.At.Truncate(time.Minute) && yi >= y0 {
			st := vaxis.Style{
				Foreground: ColorGray,
			}
			printTime(vx, x0, yi, st, line.At.Local())
		}

		if yi >= y0 {
			identSt := vaxis.Style{
				Foreground: line.HeadColor,
			}
			if line.Highlight {
				identSt.Attribute |= vaxis.AttrReverse
			}
			printIdent(vx, x0+7, yi, nickColWidth, Styled(line.Head, identSt))
		}

		x := x1
		y := yi
		var style vaxis.Style
		nextStyles := line.Body.styles

		lbi := 0
		l := []rune(line.Body.string)
		for len(l) > 0 {
			if 0 < len(nextStyles) && nextStyles[0].Start == lbi {
				style = nextStyles[0].Style
				nextStyles = nextStyles[1:]
			}
			if 0 < len(nls) && lbi == nls[0] {
				x = x1
				y++
				nls = nls[1:]
				if y0+bs.tlHeight <= y {
					break
				}
			}

			if y != yi && x == x1 && IsSplitRune(l[0]) {
				lbi += len(string(l[0]))
				l = l[1:]
				continue
			}

			xb := x
			if y >= y0 {
				dx, di := printCluster(vx, x, y, -1, l, style)
				x += dx
				lbi += len(string(l[:di]))
				l = l[di:]
			} else {
				c, cw := firstCluster(vx, l)
				x += cw
				lbi += len(c)
				l = l[len([]rune(c)):]
			}

			if style.Hyperlink != "" {
				ui.clickEvents = append(ui.clickEvents, clickEvent{
					xb: xb,
					xe: x,
					y:  y,
					event: &events.EventClickLink{
						EventClick: events.EventClick{
							NetID:  b.netID,
							Buffer: b.title,
						},
						Link: style.Hyperlink,
					},
				})
			}
		}
	}

	b.isAtTop = y0 <= yi
}
