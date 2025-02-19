package ui

import (
	"hash/fnv"
	"math"

	"git.sr.ht/~rockorager/vaxis"
)

var ColorDefault = vaxis.Color(0)
var ColorGreen = vaxis.IndexColor(2)
var ColorRed = vaxis.IndexColor(9)

type ColorSchemeType int

type ColorScheme struct {
	Type   ColorSchemeType
	Others vaxis.Color
	Self   vaxis.Color
}

const (
	ColorSchemeBase ColorSchemeType = iota
	ColorSchemeExtended
	ColorSchemeFixed
)

var baseColors = []vaxis.Color{
	// base 16 colors, excluding grayscale colors.
	vaxis.IndexColor(1),
	vaxis.IndexColor(2),
	vaxis.IndexColor(3),
	vaxis.IndexColor(4),
	vaxis.IndexColor(5),
	vaxis.IndexColor(6),
	vaxis.IndexColor(7),
	vaxis.IndexColor(9),
	vaxis.IndexColor(10),
	vaxis.IndexColor(11),
	vaxis.IndexColor(12),
	vaxis.IndexColor(13),
	vaxis.IndexColor(14),
}

func hslToRGB(hue, sat, light float64) (r, g, b uint8) {
	var r1, g1, b1 float64
	chroma := (1 - math.Abs(2*light-1)) * sat
	h6 := hue / 60
	x := chroma * (1 - math.Abs(math.Mod(h6, 2)-1))
	if h6 < 1 {
		r1, g1, b1 = chroma, x, 0
	} else if h6 < 2 {
		r1, g1, b1 = x, chroma, 0
	} else if h6 < 3 {
		r1, g1, b1 = 0, chroma, x
	} else if h6 < 4 {
		r1, g1, b1 = 0, x, chroma
	} else if h6 < 5 {
		r1, g1, b1 = x, 0, chroma
	} else {
		r1, g1, b1 = chroma, 0, x
	}
	m := light - chroma/2
	r = uint8(math.MaxUint8 * (r1 + m))
	g = uint8(math.MaxUint8 * (g1 + m))
	b = uint8(math.MaxUint8 * (b1 + m))
	return r, g, b
}

func (ui *UI) IdentColor(scheme ColorScheme, ident string, self bool) vaxis.Color {
	h := fnv.New32()
	_, _ = h.Write([]byte(ident))
	switch scheme.Type {
	case ColorSchemeFixed:
		if self {
			return scheme.Self
		} else {
			return scheme.Others
		}
	case ColorSchemeBase:
		return baseColors[int(h.Sum32()%uint32(len(baseColors)))]
	case ColorSchemeExtended:
		sum := h.Sum32()
		lo := int(sum & 0xFFFF)
		hi := int((sum >> 16) & 0xFFFF)
		hue := float64(lo) / float64(math.MaxUint16) * 360
		sat := 1.0
		var lightMin, lightMax float64
		switch ui.colorThemeMode {
		case vaxis.DarkMode:
			lightMin, lightMax = 0.5, 0.7
		case vaxis.LightMode:
			lightMin, lightMax = 0.2, 0.4
		}
		light := lightMin + float64(hi)/float64(math.MaxUint16)*(lightMax-lightMin)
		return vaxis.RGBColor(hslToRGB(hue, sat, light))
	default:
		panic("invalid color scheme setting")
	}
}

func (ui *UI) IdentString(scheme ColorScheme, ident string, self bool) StyledString {
	color := ui.IdentColor(scheme, ident, self)
	style := vaxis.Style{
		Foreground: color,
	}
	return Styled(ident, style)
}
