package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

const Overlay = "/overlay"

func IsSplitRune(r rune) bool {
	return r == ' ' || r == '\t'
}

type point struct {
	X, I  int
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
	HeadColor tcell.Color
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

func (l *Line) computeSplitPoints() {
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
		width += runeWidth(r)
	}

	if !lastWasSplit {
		l.splitPoints = append(l.splitPoints, point{
			X:     width,
			I:     len(l.Body.string),
			Split: true,
		})
	}
}

func (l *Line) NewLines(width int) []int {
	// Beware! This function was made by your local Test Driven Developper™ who
	// doesn't understand one bit of this function and how it works (though it
	// might not work that well if you're here...).  The code below is thus very
	// cryptic and not well structured.  However, I'm going to try to explain
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
			// TODO handle multi-codepoint graphemes?? :(
			wordWidth := 0
			h := 1
			for j, r := range l.Body.string[sp1.I:sp2.I] {
				wordWidth += runeWidth(r)
				if h*width < x+wordWidth {
					l.newLines = append(l.newLines, sp1.I+j)
					h++
				}
			}
			x = (x + wordWidth) % width
			if x == 0 {
				// The placement of the word is such that it ends right at the
				// end of the row.
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
	netID      string
	netName    string
	title      string
	highlights int
	unread     bool
	read       time.Time
	openedOnce bool

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

	scrollAmt int
	isAtTop   bool
}

type BufferList struct {
	colors ConfigColors

	list    []buffer
	overlay *buffer
	current int
	clicked int

	tlInnerWidth int
	tlHeight     int
	textWidth    int

	showBufferNumbers bool

	doMergeLine func(former *Line, addition Line)
}

// NewBufferList returns a new BufferList.
// Call Resize() once before using it.
func NewBufferList(colors ConfigColors, mergeLine func(*Line, Line)) BufferList {
	return BufferList{
		colors:      colors,
		list:        []buffer{},
		clicked:     -1,
		doMergeLine: mergeLine,
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
		b := bs.list[bs.current]
		b.highlights = 0
		b.unread = false
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

func (bs *BufferList) ShowBufferNumbers(enabled bool) {
	bs.showBufferNumbers = enabled
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

	bs.list = append(bs.list[:idx], bs.list[idx+1:]...)
	if len(bs.list) <= bs.current {
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
		bs.list = append(bs.list[:idx], bs.list[idx+1:]...)
		if len(bs.list) <= bs.current {
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
	bs.doMergeLine(former, addition)
	if former.Body.string == "" {
		return false
	}
	former.width = 0
	former.computeSplitPoints()
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

	if !line.Mergeable && current.openedOnce {
		line.Body = line.Body.ParseURLs()
	}

	if line.Mergeable && n != 0 && b.lines[n-1].Mergeable {
		l := &b.lines[n-1]
		if !bs.mergeLine(l, line) {
			b.lines = b.lines[:n-1]
		}
		// TODO change b.scrollAmt if it's not 0 and bs.current is idx.
	} else {
		line.computeSplitPoints()
		b.lines = append(b.lines, line)
		if b == current && 0 < b.scrollAmt {
			b.scrollAmt += len(line.NewLines(bs.textWidth)) + 1
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
					line.computeSplitPoints()
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

func (bs *BufferList) SetRead(netID, title string, timestamp time.Time) {
	_, b := bs.at(netID, title)
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
		b.highlights = 0
		b.unread = false
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
		y += len(line.NewLines(bs.textWidth)) + 1
	}
	if line != nil && line.At.After(b.read) {
		b.read = line.At
		return b.netID, b.title, b.read
	}
	return "", "", time.Time{}
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
		y += len(line.NewLines(bs.textWidth)) + 1
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
		y += len(line.NewLines(bs.textWidth)) + 1
	}
	b.scrollAmt = yLastHighlight
	return b.scrollAmt != 0
}

func (bs *BufferList) IsAtTop() bool {
	b := bs.cur()
	return b.isAtTop
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

func (bs *BufferList) DrawVerticalBufferList(screen tcell.Screen, x0, y0, width, height int, offset *int) {
	if y0+len(bs.list)-*offset < height {
		*offset = y0 + len(bs.list) - height
		if *offset < 0 {
			*offset = 0
		}
	}

	width--
	drawVerticalLine(screen, x0+width, y0, height)
	clearArea(screen, x0, y0, width, height)

	indexPadding := 1 + int(math.Ceil(math.Log10(float64(len(bs.list)))))
	for i, b := range bs.list[*offset:] {
		bi := *offset + i
		x := x0
		y := y0 + i
		st := tcell.StyleDefault
		if b.unread {
			st = st.Bold(true).Foreground(bs.colors.Unread)
		}
		if bi == bs.current || bi == bs.clicked {
			st = st.Reverse(true)
		}
		if bs.showBufferNumbers {
			indexSt := st.Foreground(tcell.ColorGray)
			indexText := fmt.Sprintf("%d:", bi)
			printString(screen, &x, y, Styled(indexText, indexSt))
			x = x0 + indexPadding
		}

		var title string
		if b.title == "" {
			title = b.netName
		} else {
			if bi == bs.current || bi == bs.clicked {
				screen.SetContent(x, y, ' ', nil, tcell.StyleDefault.Reverse(true))
				screen.SetContent(x+1, y, ' ', nil, tcell.StyleDefault.Reverse(true))
			}
			x += 2
			title = b.title
		}
		title = truncate(title, width-(x-x0), "\u2026")
		printString(screen, &x, y, Styled(title, st))

		if bi == bs.current || bi == bs.clicked {
			st := tcell.StyleDefault.Reverse(true)
			for ; x < x0+width; x++ {
				screen.SetContent(x, y, ' ', nil, st)
			}
			screen.SetContent(x, y, 0x2590, nil, st)
		}

		if b.highlights != 0 {
			highlightSt := st.Foreground(tcell.ColorRed).Reverse(true)
			highlightText := fmt.Sprintf(" %d ", b.highlights)
			x = x0 + width - len(highlightText)
			printString(screen, &x, y, Styled(highlightText, highlightSt))
		}
	}
}

func (bs *BufferList) HorizontalBufferOffset(x int, offset int) int {
	for i, b := range bs.list[offset:] {
		if i > 0 {
			x--
			if x < 0 {
				return -1
			}
		}
		x -= bufferWidth(&b)
		if x < 0 {
			return offset + i
		}
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
		width += bufferWidth(&bs.list[leftMost])
		if width > screenWidth {
			return leftMost + 1 // Went offscreen, need to go one step back
		}
	}

	return 0
}

func bufferWidth(b *buffer) int {
	width := 0
	if b.title == "" {
		width += stringWidth(b.netName)
	} else {
		width += stringWidth(b.title)
	}
	if 0 < b.highlights {
		width += 2 + len(fmt.Sprintf("%d", b.highlights))
	}
	return width
}

func (bs *BufferList) DrawHorizontalBufferList(screen tcell.Screen, x0, y0, width int, offset *int) {
	x := width
	for i := len(bs.list) - 1; i >= 0; i-- {
		b := &bs.list[i]
		x--
		x -= bufferWidth(b)
		if x <= 10 {
			break
		}
		if *offset > i {
			*offset = i
		}
	}
	x = x0

	for i, b := range bs.list[*offset:] {
		i := i + *offset
		if width <= x-x0 {
			break
		}
		st := tcell.StyleDefault
		if b.unread {
			st = st.Bold(true).Foreground(bs.colors.Unread)
		} else if i == bs.current {
			st = st.Underline(true)
		}
		if i == bs.clicked {
			st = st.Reverse(true)
		}

		var title string
		if b.title == "" {
			st = st.Dim(true)
			title = b.netName
		} else {
			title = b.title
		}
		title = truncate(title, width-x, "\u2026")
		printString(screen, &x, y0, Styled(title, st))

		if 0 < b.highlights {
			st = st.Foreground(tcell.ColorRed).Reverse(true)
			screen.SetContent(x, y0, ' ', nil, st)
			x++
			printNumber(screen, &x, y0, st, b.highlights)
			screen.SetContent(x, y0, ' ', nil, st)
			x++
		}
		screen.SetContent(x, y0, ' ', nil, tcell.StyleDefault)
		x++
	}
	for x < width {
		screen.SetContent(x, y0, ' ', nil, tcell.StyleDefault)
		x++
	}
}

func (bs *BufferList) DrawTimeline(screen tcell.Screen, x0, y0, nickColWidth int) {
	clearArea(screen, x0, y0, bs.tlInnerWidth+nickColWidth+9, bs.tlHeight+2)

	b := bs.cur()
	if !b.openedOnce {
		b.openedOnce = true
		for i := 0; i < len(b.lines); i++ {
			b.lines[i].Body = b.lines[i].Body.ParseURLs()
		}
	}

	xTopic := x0
	printString(screen, &xTopic, y0, Styled(b.topic, tcell.StyleDefault))
	y0++
	for x := x0; x < x0+bs.tlInnerWidth+nickColWidth+9; x++ {
		st := tcell.StyleDefault.Foreground(tcell.ColorGray)
		screen.SetContent(x, y0, 0x2500, nil, st)
	}
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
		nls := line.NewLines(bs.textWidth)

		if !rulerDrawn {
			isRead := !line.At.After(b.unreadRuler)
			if isRead && yi > y0 {
				yi--
				st := tcell.StyleDefault.Foreground(tcell.ColorRed)
				margin := 5
				for x := x0 + nickColWidth + 9 + margin; x < x0+bs.tlInnerWidth-margin; x++ {
					screen.SetContent(x, yi, '-', nil, st)
				}
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
			st := tcell.StyleDefault.Bold(true)
			// as a special case, always draw the first visible message date, even if it is a continuation line
			yd := yi
			if yd < y0 {
				yd = y0
			}
			printDate(screen, x0, yd, st, line.At.Local())
		} else if b.lines[i-1].At.Truncate(time.Minute) != line.At.Truncate(time.Minute) && yi >= y0 {
			st := tcell.StyleDefault.Foreground(tcell.ColorGray)
			printTime(screen, x0, yi, st, line.At.Local())
		}

		if yi >= y0 {
			identSt := tcell.StyleDefault.
				Foreground(line.HeadColor).
				Reverse(line.Highlight)
			printIdent(screen, x0+7, yi, nickColWidth, Styled(line.Head, identSt))
		}

		x := x1
		y := yi
		style := tcell.StyleDefault
		nextStyles := line.Body.styles

		for i, r := range line.Body.string {
			if 0 < len(nextStyles) && nextStyles[0].Start == i {
				style = nextStyles[0].Style
				nextStyles = nextStyles[1:]
			}
			if 0 < len(nls) && i == nls[0] {
				x = x1
				y++
				nls = nls[1:]
				if y0+bs.tlHeight <= y {
					break
				}
			}

			if y != yi && x == x1 && IsSplitRune(r) {
				continue
			}

			if y >= y0 {
				screen.SetContent(x, y, r, nil, style)
			}
			x += runeWidth(r)
		}
	}

	b.isAtTop = y0 <= yi
}
