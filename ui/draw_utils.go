package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/delthas/go-localeinfo"
	"github.com/rivo/uniseg"
)

var asciiStringCache []string

func init() {
	asciiStringCache = make([]string, 0x80)
	for i := range asciiStringCache {
		asciiStringCache[i] = string(rune(i))
	}
}

var runeWidthMap = make(map[rune]int)

func runeWidth(vx *Vaxis, r rune) int {
	if vx == nil { // For tests only
		return 1
	}
	if r == '\n' {
		r = '↲'
	}
	if r <= 0x1F {
		return 0
	}
	if r <= 0x7F {
		return 1
	}
	if n, ok := runeWidthMap[r]; ok {
		return n
	}
	n := vx.RenderedWidth(string([]rune{r}))
	runeWidthMap[r] = n
	return n
}

func stringWidth(vx *Vaxis, s string) int {
	if vx == nil { // For tests only
		return len(s)
	}
	if len(s) == 1 { // Single-character ASCII fast path
		if s[0] == '\n' { // Replaced with ↲
			return 1
		}
		if s[0] <= 0x1F {
			return 0
		}
		if s[0] <= 0x7F {
			return 1
		}
	}
	r := []rune(s)
	if len(r) == 1 { // Single-character fast path
		return runeWidth(vx, r[0])
	}
	return vx.RenderedWidth(s)
}

func truncate(vx *Vaxis, s string, w int, tail string) string {
	if stringWidth(vx, s) <= w {
		return s
	}
	w -= stringWidth(vx, tail)

	width := 0
	var sb strings.Builder
	for _, c := range vaxis.Characters(s) {
		chWidth := stringWidth(vx, c.Grapheme)
		if width+chWidth > w {
			break
		}
		width += chWidth
		sb.WriteString(c.Grapheme)
	}
	sb.WriteString(tail)
	return sb.String()
}

var clusterWidthMap = make(map[string]int)

// width in cells
func firstCluster(vx *Vaxis, r []rune) (c string, width int) {
	if len(r) == 0 { // Empty fast-path
		return "", 0
	}
	if r[0] == '\t' {
		return " ", 1
	}
	if r[0] <= 0x7F { // ASCII fast-path
		return asciiStringCache[int(r[0])], runeWidth(vx, r[0])
	}
	c, _, _, _ = uniseg.FirstGraphemeClusterInString(string(r), -1)

	var cw int
	if n, ok := clusterWidthMap[c]; ok {
		cw = n
	} else {
		cw = stringWidth(vx, c)
		clusterWidthMap[c] = cw
	}
	return c, cw
}

func setCell(vx *Vaxis, x int, y int, r rune, st vaxis.Style) {
	vx.window.SetCell(x, y, vaxis.Cell{
		Character: vaxis.Character{
			Grapheme: string([]rune{r}),
		},
		Style: st,
	})
}

// limit = -1 means no limit
// di is an offset in runes
func printCluster(vx *Vaxis, x int, y int, limit int, r []rune, st vaxis.Style) (dx int, di int) {
	if limit >= 0 && x >= limit {
		return 0, 0
	}
	var c string
	var w int
	if len(r) > 0 && r[0] <= 0x7F { // ASCII fast-path
		c = asciiStringCache[int(r[0])]
		w = runeWidth(vx, r[0])
		di = 1
	} else {
		c, w = firstCluster(vx, r)
		di = len([]rune(c))
	}
	if limit >= 0 && w > limit-x {
		return 0, 0
	}
	vx.window.SetCell(x, y, vaxis.Cell{
		Character: vaxis.Character{
			Grapheme: c,
		},
		Style: st,
	})
	return w, di
}

func printString(vx *Vaxis, x *int, y int, s StyledString) {
	var st vaxis.Style
	nextStyles := s.styles

	i := 0
	sr := []rune(s.string)
	for len(sr) > 0 {
		if 0 < len(nextStyles) && nextStyles[0].Start == i {
			st = nextStyles[0].Style
			nextStyles = nextStyles[1:]
		}
		dx, di := printCluster(vx, *x, y, -1, sr, st)
		*x += dx
		i += len(string(sr[:di]))
		sr = sr[di:]
	}
}

func printIdent(vx *Vaxis, x, y, width int, s StyledString) (xb int, xe int) {
	s.string = truncate(vx, s.string, width, "\u2026")
	x += width - stringWidth(vx, s.string)
	var st vaxis.Style
	if len(s.styles) != 0 && s.styles[0].Start == 0 {
		st = s.styles[0].Style
	}
	setCell(vx, x-1, y, ' ', st)
	xb = x
	printString(vx, &x, y, s)
	if len(s.styles) != 0 {
		// TODO check if it's not a style that is from the truncated
		// part of s.
		st = s.styles[len(s.styles)-1].Style
	}
	setCell(vx, x, y, ' ', st)
	xe = x
	return
}

func printNumber(vx *Vaxis, x *int, y int, st vaxis.Style, n int) {
	s := Styled(fmt.Sprintf("%d", n), st)
	printString(vx, x, y, s)
}

var dateConfig sync.Once
var dateMonthFirst bool

func loadDateInfo() {
	// Very overkill, but I like it :)
	// Try to extract from the user locale whether they'd rather have the date
	// printed as dd/mm or mm/dd.
	// If we're not sure, print dd/mm.
	l, err := localeinfo.NewLocale("")
	if err != nil {
		return
	}
	format := l.DateFormat()
	dayIndex := -1
	for _, s := range []string{"%d", "%e"} {
		dayIndex = strings.Index(format, s)
		if dayIndex >= 0 {
			break
		}
	}
	if dayIndex == -1 {
		return
	}
	monthIndex := -1
	for _, s := range []string{"%m", "%b", "%B"} {
		monthIndex = strings.Index(format, s)
		if monthIndex >= 0 {
			break
		}
	}
	if monthIndex == -1 {
		return
	}
	if monthIndex < dayIndex {
		dateMonthFirst = true
	}
}

func printDate(vx *Vaxis, x int, y int, st vaxis.Style, t time.Time) {
	dateConfig.Do(loadDateInfo)
	_, m, d := t.Date()
	var left, right int
	if dateMonthFirst {
		left, right = int(m), d
	} else {
		left, right = d, int(m)
	}
	l0 := rune(left/10) + '0'
	l1 := rune(left%10) + '0'
	r0 := rune(right/10) + '0'
	r1 := rune(right%10) + '0'

	setCell(vx, x+0, y, l0, st)
	setCell(vx, x+1, y, l1, st)
	setCell(vx, x+2, y, '/', st)
	setCell(vx, x+3, y, r0, st)
	setCell(vx, x+4, y, r1, st)
}

func printTime(vx *Vaxis, x int, y int, st vaxis.Style, t time.Time) {
	hr0 := rune(t.Hour()/10) + '0'
	hr1 := rune(t.Hour()%10) + '0'
	mn0 := rune(t.Minute()/10) + '0'
	mn1 := rune(t.Minute()%10) + '0'
	setCell(vx, x+0, y, hr0, st)
	setCell(vx, x+1, y, hr1, st)
	setCell(vx, x+2, y, ':', st)
	setCell(vx, x+3, y, mn0, st)
	setCell(vx, x+4, y, mn1, st)
}

func clearArea(vx *Vaxis, x0, y0, width, height int) {
	vx.window.New(x0, y0, width, height).Clear()
}
