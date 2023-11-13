package ui

import (
	"hash/fnv"

	"github.com/gdamore/tcell/v2"
)

type ColorSchemeType int

type ColorScheme struct {
	Type   ColorSchemeType
	Others tcell.Color
	Self   tcell.Color
}

const (
	ColorSchemeBase ColorSchemeType = iota
	ColorSchemeExtended
	ColorSchemeFixed
)

var colors = map[ColorSchemeType][]tcell.Color{
	// base 16 colors, excluding grayscale colors.
	ColorSchemeBase: {
		tcell.ColorMaroon,
		tcell.ColorGreen,
		tcell.ColorOlive,
		tcell.ColorNavy,
		tcell.ColorPurple,
		tcell.ColorTeal,
		tcell.ColorSilver,
		tcell.ColorRed,
		tcell.ColorLime,
		tcell.ColorYellow,
		tcell.ColorBlue,
		tcell.ColorFuchsia,
		tcell.ColorAqua,
	},
	// all XTerm extended colors with HSL saturation=1, light=0.5
	ColorSchemeExtended: {
		tcell.Color196, // HSL hue: 0°
		tcell.Color202, // HSL hue: 22°
		tcell.Color208, // HSL hue: 32°
		tcell.Color214, // HSL hue: 41°
		tcell.Color220, // HSL hue: 51°
		tcell.Color226, // HSL hue: 60°
		tcell.Color190, // HSL hue: 69°
		tcell.Color154, // HSL hue: 79°
		tcell.Color118, // HSL hue: 88°
		tcell.Color82,  // HSL hue: 98°
		tcell.Color46,  // HSL hue: 120°
		tcell.Color47,  // HSL hue: 142°
		tcell.Color48,  // HSL hue: 152°
		tcell.Color49,  // HSL hue: 161°
		tcell.Color50,  // HSL hue: 171°
		tcell.Color51,  // HSL hue: 180°
		tcell.Color45,  // HSL hue: 189°
		tcell.Color39,  // HSL hue: 199°
		tcell.Color33,  // HSL hue: 208°
		tcell.Color27,  // HSL hue: 218°
		tcell.Color21,  // HSL hue: 240°
		tcell.Color57,  // HSL hue: 262°
		tcell.Color93,  // HSL hue: 272°
		tcell.Color129, // HSL hue: 281°
		tcell.Color165, // HSL hue: 291°
		tcell.Color201, // HSL hue: 300°
		tcell.Color200, // HSL hue: 309°
		tcell.Color199, // HSL hue: 319°
		tcell.Color198, // HSL hue: 328°
		tcell.Color197, // HSL hue: 338°
	},
}

func IdentColor(scheme ColorScheme, ident string, self bool) tcell.Color {
	h := fnv.New32()
	_, _ = h.Write([]byte(ident))
	if scheme.Type == ColorSchemeFixed {
		if self {
			return scheme.Self
		} else {
			return scheme.Others
		}
	}
	c := colors[scheme.Type]
	return c[int(h.Sum32()%uint32(len(c)))]
}

func IdentString(scheme ColorScheme, ident string, self bool) StyledString {
	color := IdentColor(scheme, ident, self)
	style := tcell.StyleDefault.Foreground(color)
	return Styled(ident, style)
}
