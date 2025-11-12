package ui

import (
	"fmt"
	"math/rand"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.sr.ht/~rockorager/vaxis"
	"mvdan.cc/xurls/v2"
)

// Taken from <https://modern.ircdocs.horse/formatting.html>

var baseCodes = []vaxis.Color{
	vaxis.IndexColor(15),
	vaxis.IndexColor(0),
	vaxis.IndexColor(4),
	vaxis.IndexColor(2),
	vaxis.IndexColor(9),
	vaxis.IndexColor(1),
	vaxis.IndexColor(5),
	vaxis.IndexColor(3),
	vaxis.IndexColor(11),
	vaxis.IndexColor(10),
	vaxis.IndexColor(6),
	vaxis.IndexColor(14),
	vaxis.IndexColor(12),
	vaxis.IndexColor(13),
	vaxis.IndexColor(8),
	vaxis.IndexColor(7),
}

var hexCodes = []uint32{
	0x470000, 0x472100, 0x474700, 0x324700, 0x004700, 0x00472c, 0x004747, 0x002747, 0x000047, 0x2e0047, 0x470047, 0x47002a,
	0x740000, 0x743a00, 0x747400, 0x517400, 0x007400, 0x007449, 0x007474, 0x004074, 0x000074, 0x4b0074, 0x740074, 0x740045,
	0xb50000, 0xb56300, 0xb5b500, 0x7db500, 0x00b500, 0x00b571, 0x00b5b5, 0x0063b5, 0x0000b5, 0x7500b5, 0xb500b5, 0xb5006b,
	0xff0000, 0xff8c00, 0xffff00, 0xb2ff00, 0x00ff00, 0x00ffa0, 0x00ffff, 0x008cff, 0x0000ff, 0xa500ff, 0xff00ff, 0xff0098,
	0xff5959, 0xffb459, 0xffff71, 0xcfff60, 0x6fff6f, 0x65ffc9, 0x6dffff, 0x59b4ff, 0x5959ff, 0xc459ff, 0xff66ff, 0xff59bc,
	0xff9c9c, 0xffd39c, 0xffff9c, 0xe2ff9c, 0x9cff9c, 0x9cffdb, 0x9cffff, 0x9cd3ff, 0x9c9cff, 0xdc9cff, 0xff9cff, 0xff94d3,
	0x000000, 0x131313, 0x282828, 0x363636, 0x4d4d4d, 0x656565, 0x818181, 0x9f9f9f, 0xbcbcbc, 0xe2e2e2, 0xffffff,
}

func colorFromCode(code int) (color vaxis.Color) {
	if code < 0 || 99 <= code {
		color = ColorDefault
	} else if code < 16 {
		color = baseCodes[code]
	} else {
		color = vaxis.HexColor(hexCodes[code-16])
	}
	return
}

type rangedStyle struct {
	Start int // byte index at which Style is effective
	Style vaxis.Style
}

type StyledString struct {
	string
	styles []rangedStyle // sorted, elements cannot have the same Start value
}

func PlainString(s string) StyledString {
	return StyledString{string: s}
}

func PlainSprintf(format string, a ...interface{}) StyledString {
	return PlainString(fmt.Sprintf(format, a...))
}

func ColorString(s string, fg vaxis.Color) StyledString {
	return Styled(s, vaxis.Style{Foreground: fg})
}

func Styled(s string, style vaxis.Style) StyledString {
	rStyle := rangedStyle{
		Start: 0,
		Style: style,
	}
	return StyledString{
		string: s,
		styles: []rangedStyle{rStyle},
	}
}

func (s StyledString) String() string {
	return s.string
}

var urlRegex *regexp.Regexp

func init() {
	urlRegex, _ = xurls.StrictMatchingScheme(xurls.AnyScheme)
	urlRegex = regexp.MustCompile(urlRegex.String() + `|#[\p{L}0-9#.-]*[\p{L}0-9]`)
	urlRegex.Longest()
}

func (s StyledString) ParseURLs() StyledString {
	if !strings.ContainsAny(s.string, ".#") {
		// fast path: no URL
		return s
	}

	styles := make([]rangedStyle, 0, len(s.styles))

	urls := urlRegex.FindAllStringIndex(s.string, -1)
	j := 0
	lastStyle := rangedStyle{
		Start: -1,
	}
	for i := 0; i < len(urls); i++ {
		u := urls[i]
		ub, ue := u[0], u[1]
		link := s.string[u[0]:u[1]]
		var params string
		if link[0] == '#' {
			if prev := lastRuneBefore(s.string, u[0]); !(prev == 0 || unicode.IsSpace(prev) || prev == '(' || prev == '[') {
				// channel link preceded by a non-space character: eg a#a: drop
				continue
			}
			if !strings.ContainsFunc(link[1:], func(r rune) bool {
				return r < '0' || r > '9'
			}) {
				// channel link with only numbers: eg #1234: drop, because this is likely a reference to a ticket
				continue
			}
			// store channel in hyperlink params, but create no link.
			// this allows us to save the link data without actually creating a link in the terminal
			params = fmt.Sprintf("channel=%v", link)
			link = ""
		} else {
			if u, err := url.Parse(link); err != nil || u.Scheme == "" {
				link = "https://" + link
			}
			params = fmt.Sprintf("id=_%010d", rand.Int31())
		}
		// find last style starting before or at url begin
		for ; j < len(s.styles); j++ {
			st := s.styles[j]
			if st.Start > ub {
				break
			}
			if st.Start == ub {
				// a style already starts at this position, edit it
				st.Style.Hyperlink = link
				st.Style.HyperlinkParams = params
			}
			lastStyle = st
			styles = append(styles, st)
		}
		if lastStyle.Start != ub {
			// no style existed at this position, add one from the last style
			st := lastStyle.Style
			st.Hyperlink = link
			st.HyperlinkParams = params
			styles = append(styles, rangedStyle{
				Start: ub,
				Style: st,
			})
		}
		// find last style starting before or at url end
		for ; j < len(s.styles); j++ {
			st := s.styles[j]
			if st.Start > ue {
				break
			}
			if st.Start < ue {
				st.Style.Hyperlink = link
				st.Style.HyperlinkParams = params
			}
			lastStyle = st
			styles = append(styles, st)
		}
		if lastStyle.Start != ue {
			// no style existed at this position, add one from the last style without the hyperlink
			st := lastStyle.Style
			st.Hyperlink = ""
			st.HyperlinkParams = ""
			styles = append(styles, rangedStyle{
				Start: ue,
				Style: st,
			})
		}
	}
	styles = append(styles, s.styles[j:]...)

	return StyledString{
		string: s.string,
		styles: styles,
	}
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

func parseColorNumber(raw string) (color vaxis.Color, n int) {
	if len(raw) == 0 || !isDigit(raw[0]) {
		return
	}

	// len(raw) >= 1 and its first character is a digit.

	if len(raw) == 1 || !isDigit(raw[1]) {
		code, _ := strconv.Atoi(raw[:1])
		return colorFromCode(code), 1
	}

	// len(raw) >= 2 and the two first characters are digits.

	code, _ := strconv.Atoi(raw[:2])
	return colorFromCode(code), 2
}

func parseColor(raw string) (fg, bg vaxis.Color, n int) {
	fg, n = parseColorNumber(raw)
	raw = raw[n:]

	if len(raw) == 0 || raw[0] != ',' {
		return fg, ColorDefault, n
	}

	n++
	bg, p := parseColorNumber(raw[1:])
	n += p

	if bg == ColorDefault {
		// Lone comma, do not parse as part of a color code.
		return fg, ColorDefault, n - 1
	}

	return fg, bg, n
}

func parseHexColorNumber(raw string) (color vaxis.Color, n int) {
	if len(raw) < 6 {
		return
	}
	if raw[0] == '+' || raw[0] == '-' {
		return
	}
	value, err := strconv.ParseInt(raw[:6], 16, 32)
	if err != nil {
		return
	}
	return vaxis.HexColor(uint32(value)), 6
}

func parseHexColor(raw string) (fg, bg vaxis.Color, n int) {
	fg, n = parseHexColorNumber(raw)
	raw = raw[n:]

	if len(raw) == 0 || raw[0] != ',' {
		return fg, ColorDefault, n
	}

	n++
	bg, p := parseHexColorNumber(raw[1:])
	n += p

	if bg == ColorDefault {
		// Lone comma, do not parse as part of a color code.
		return fg, ColorDefault, n - 1
	}

	return fg, bg, n
}

func lastRuneBefore(s string, i int) rune {
	var r rune
	for ri, rr := range s {
		if ri >= i {
			break
		}
		r = rr
	}
	return r
}

func IRCString(raw string) StyledString {
	var formatted strings.Builder
	var styles []rangedStyle
	var last vaxis.Style

	for len(raw) != 0 {
		r, runeSize := utf8.DecodeRuneInString(raw)
		current := last
		if r == 0x0F {
			current = vaxis.Style{}
		} else if r == 0x02 {
			current.Attribute ^= vaxis.AttrBold
		} else if r == 0x03 || r == 0x04 {
			var fg vaxis.Color
			var bg vaxis.Color
			var n int
			if r == 0x03 {
				fg, bg, n = parseColor(raw[1:])
			} else {
				fg, bg, n = parseHexColor(raw[1:])
			}
			raw = raw[n:]
			if n == 0 {
				current.Foreground = ColorDefault
				current.Background = ColorDefault
			} else if bg == ColorDefault {
				current.Foreground = fg
			} else {
				current.Foreground = fg
				current.Background = bg
			}
		} else if r == 0x16 {
			current.Attribute ^= vaxis.AttrReverse
		} else if r == 0x1D {
			current.Attribute ^= vaxis.AttrItalic
		} else if r == 0x1E {
			current.Attribute ^= vaxis.AttrStrikethrough
		} else if r == 0x1F {
			if last.UnderlineStyle == vaxis.UnderlineOff {
				current.UnderlineStyle = vaxis.UnderlineSingle
			} else {
				current.UnderlineStyle = vaxis.UnderlineOff
			}
		} else {
			formatted.WriteRune(r)
		}
		if last != current {
			if len(styles) != 0 && styles[len(styles)-1].Start == formatted.Len() {
				styles[len(styles)-1] = rangedStyle{
					Start: formatted.Len(),
					Style: current,
				}
			} else {
				styles = append(styles, rangedStyle{
					Start: formatted.Len(),
					Style: current,
				})
			}
		}
		last = current
		raw = raw[runeSize:]
	}

	return StyledString{
		string: formatted.String(),
		styles: styles,
	}
}

type StyledStringBuilder struct {
	strings.Builder
	styles []rangedStyle
}

func (sb *StyledStringBuilder) Reset() {
	sb.Builder.Reset()
	sb.styles = sb.styles[:0]
}

func (sb *StyledStringBuilder) WriteStyledString(s StyledString) {
	start := len(sb.styles)
	sb.styles = append(sb.styles, s.styles...)
	for i := start; i < len(sb.styles); i++ {
		sb.styles[i].Start += sb.Len()
	}
	sb.WriteString(s.string)
}

func (sb *StyledStringBuilder) AddStyle(start int, style vaxis.Style) {
	for i := 0; i < len(sb.styles); i++ {
		if sb.styles[i].Start == i {
			sb.styles[i].Style = style
			break
		} else if sb.styles[i].Start < i {
			sb.styles = append(sb.styles[:i+1], sb.styles[i:]...)
			sb.styles[i+1] = rangedStyle{
				Start: start,
				Style: style,
			}
			break
		}
	}
	sb.styles = append(sb.styles, rangedStyle{
		Start: start,
		Style: style,
	})
}

func (sb *StyledStringBuilder) SetStyle(style vaxis.Style) {
	sb.styles = append(sb.styles, rangedStyle{
		Start: sb.Len(),
		Style: style,
	})
}

func (sb *StyledStringBuilder) StyledString() StyledString {
	string := sb.String()
	styles := make([]rangedStyle, 0, len(sb.styles))
	for _, style := range sb.styles {
		if len(string) <= style.Start {
			break
		}
		styles = append(styles, style)
	}
	return StyledString{
		string: string,
		styles: styles,
	}
}
