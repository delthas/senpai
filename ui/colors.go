package ui

import (
	"hash/fnv"

	"git.sr.ht/~rockorager/vaxis"
)

var ColorRed = vaxis.IndexColor(9)
var ColorGray = vaxis.IndexColor(8)

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

var colors = map[ColorSchemeType][]vaxis.Color{
	// base 16 colors, excluding grayscale colors.
	ColorSchemeBase: {
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
	},
	// all XTerm extended colors with HSL saturation=1, light=0.5
	ColorSchemeExtended: {
		vaxis.IndexColor(196), // HSL hue: 0°
		vaxis.IndexColor(202), // HSL hue: 22°
		vaxis.IndexColor(208), // HSL hue: 32°
		vaxis.IndexColor(214), // HSL hue: 41°
		vaxis.IndexColor(220), // HSL hue: 51°
		vaxis.IndexColor(226), // HSL hue: 60°
		vaxis.IndexColor(190), // HSL hue: 69°
		vaxis.IndexColor(154), // HSL hue: 79°
		vaxis.IndexColor(118), // HSL hue: 88°
		vaxis.IndexColor(82),  // HSL hue: 98°
		vaxis.IndexColor(46),  // HSL hue: 120°
		vaxis.IndexColor(47),  // HSL hue: 142°
		vaxis.IndexColor(48),  // HSL hue: 152°
		vaxis.IndexColor(49),  // HSL hue: 161°
		vaxis.IndexColor(50),  // HSL hue: 171°
		vaxis.IndexColor(51),  // HSL hue: 180°
		vaxis.IndexColor(45),  // HSL hue: 189°
		vaxis.IndexColor(39),  // HSL hue: 199°
		vaxis.IndexColor(33),  // HSL hue: 208°
		vaxis.IndexColor(27),  // HSL hue: 218°
		vaxis.IndexColor(21),  // HSL hue: 240°
		vaxis.IndexColor(57),  // HSL hue: 262°
		vaxis.IndexColor(93),  // HSL hue: 272°
		vaxis.IndexColor(129), // HSL hue: 281°
		vaxis.IndexColor(165), // HSL hue: 291°
		vaxis.IndexColor(201), // HSL hue: 300°
		vaxis.IndexColor(200), // HSL hue: 309°
		vaxis.IndexColor(199), // HSL hue: 319°
		vaxis.IndexColor(198), // HSL hue: 328°
		vaxis.IndexColor(197), // HSL hue: 338°
	},
}

func IdentColor(scheme ColorScheme, ident string, self bool) vaxis.Color {
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
	style := vaxis.Style{
		Foreground: color,
	}
	return Styled(ident, style)
}
