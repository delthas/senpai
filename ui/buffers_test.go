package ui

import (
	"strings"
	"testing"
)

func assertSplitPoints(t *testing.T, body string, expected []point) {
	l := Line{Body: PlainString(body)}
	l.computeSplitPoints(nil)

	if len(l.splitPoints) != len(expected) {
		t.Errorf("%q: expected %d split points got %d", body, len(expected), len(l.splitPoints))
		return
	}

	for i := 0; i < len(expected); i++ {
		e := expected[i]
		a := l.splitPoints[i]

		if e.X != a.X {
			t.Errorf("%q, point #%d: expected X=%d got %d", body, i, e.X, a.X)
		}
		if e.I != a.I {
			t.Errorf("%q, point #%d: expected I=%d got %d", body, i, e.I, a.I)
		}
		if e.Split != a.Split {
			t.Errorf("%q, point #%d: expected Split=%t got %t", body, i, e.Split, a.Split)
		}
	}
}

func TestLineSplitPoints(t *testing.T) {
	assertSplitPoints(t, "hello", []point{
		{X: 0, I: 0, Split: false},
		{X: 5, I: 5, Split: true},
	})
	assertSplitPoints(t, "hello world", []point{
		{X: 0, I: 0, Split: false},
		{X: 5, I: 5, Split: true},
		{X: 6, I: 6, Split: false},
		{X: 11, I: 11, Split: true},
	})
	assertSplitPoints(t, "lorem ipsum dolor shit amet", []point{
		{X: 0, I: 0, Split: false},
		{X: 5, I: 5, Split: true},
		{X: 6, I: 6, Split: false},
		{X: 11, I: 11, Split: true},
		{X: 12, I: 12, Split: false},
		{X: 17, I: 17, Split: true},
		{X: 18, I: 18, Split: false},
		{X: 22, I: 22, Split: true},
		{X: 23, I: 23, Split: false},
		{X: 27, I: 27, Split: true},
	})
}

func showSplit(s string, nls []int) string {
	var sb strings.Builder
	sb.Grow(len(s) + len(nls))

	for i, r := range s {
		if 0 < len(nls) && i == nls[0] {
			sb.WriteRune('|')
			nls = nls[1:]
		}
		sb.WriteRune(r)
	}

	return sb.String()
}

func assertNewLines(t *testing.T, body string, width int, expected int) {
	l := Line{Body: PlainString(body)}
	l.computeSplitPoints(nil)

	actual := l.NewLines(nil, width)

	if len(actual)+1 != expected {
		s := showSplit(body, actual)
		t.Errorf("%q with width=%d expected to take %d lines, takes %d: '%s' (%v)", body, width, expected, len(actual)+1, s, actual)
		return
	}
}

func TestRenderedHeight(t *testing.T) {
	assertNewLines(t, "0123456789", 1, 10)
	assertNewLines(t, "0123456789", 2, 5)
	assertNewLines(t, "0123456789", 3, 4)
	assertNewLines(t, "0123456789", 4, 3)
	assertNewLines(t, "0123456789", 5, 2)
	assertNewLines(t, "0123456789", 6, 2)
	assertNewLines(t, "0123456789", 7, 2)
	assertNewLines(t, "0123456789", 8, 2)
	assertNewLines(t, "0123456789", 9, 2)
	assertNewLines(t, "0123456789", 10, 1)
	assertNewLines(t, "0123456789", 11, 1)

	// LEN=9, WIDTH=9
	assertNewLines(t, "take care", 1, 8)  // |t|a|k|e|c|a|r|e|
	assertNewLines(t, "take care", 2, 4)  // |ta|ke|ca|re|
	assertNewLines(t, "take care", 3, 3)  // |tak|e c|are|
	assertNewLines(t, "take care", 4, 2)  // |take|care|
	assertNewLines(t, "take care", 5, 2)  // |take |care |
	assertNewLines(t, "take care", 6, 2)  // |take  |care  |
	assertNewLines(t, "take care", 7, 2)  // |take   |care   |
	assertNewLines(t, "take care", 8, 2)  // |take    |care    |
	assertNewLines(t, "take care", 9, 1)  // |take care|
	assertNewLines(t, "take care", 10, 1) // |take care |

	// LEN=10, WIDTH=10
	assertNewLines(t, "take  care", 1, 8)  // |t|a|k|e|c|a|r|e|
	assertNewLines(t, "take  care", 2, 4)  // |ta|ke|ca|re|
	assertNewLines(t, "take  care", 3, 4)  // |tak|e  |car|e  |
	assertNewLines(t, "take  care", 4, 2)  // |take|care|
	assertNewLines(t, "take  care", 5, 2)  // |take |care |
	assertNewLines(t, "take  care", 6, 2)  // |take  |care  |
	assertNewLines(t, "take  care", 7, 2)  // |take   |care   |
	assertNewLines(t, "take  care", 8, 2)  // |take    |care    |
	assertNewLines(t, "take  care", 9, 2)  // |take     |care     |
	assertNewLines(t, "take  care", 10, 1) // |take  care|
	assertNewLines(t, "take  care", 11, 1) // |take  care |

	// LEN=16, WIDTH=16
	assertNewLines(t, "have a good day!", 1, 13) // |h|a|v|e|a|g|o|o|d|d|a|y|!|
	assertNewLines(t, "have a good day!", 2, 7)  // |ha|ve|a |go|od|da|y!|
	assertNewLines(t, "have a good day!", 3, 5)  // |hav|e a|goo|d d|ay!|
	assertNewLines(t, "have a good day!", 4, 4)  // |have|a   |good|day!|
	assertNewLines(t, "have a good day!", 5, 4)  // |have |a    |good |day! |
	assertNewLines(t, "have a good day!", 6, 3)  // |have a|good  |day!  |
	assertNewLines(t, "have a good day!", 7, 3)  // |have a |good   |day!   |
	assertNewLines(t, "have a good day!", 8, 3)  // |have a  |good    |day!    |
	assertNewLines(t, "have a good day!", 9, 2)  // |have a   |good day!|
	assertNewLines(t, "have a good day!", 10, 2) // |have a    |good day! |
	assertNewLines(t, "have a good day!", 11, 2) // |have a good|day!       |
	assertNewLines(t, "have a good day!", 12, 2) // |have a good |day!        |
	assertNewLines(t, "have a good day!", 13, 2) // |have a good  |day!         |
	assertNewLines(t, "have a good day!", 14, 2) // |have a good   |day!          |
	assertNewLines(t, "have a good day!", 15, 2) // |have a good    |day!           |
	assertNewLines(t, "have a good day!", 16, 1) // |have a good day!|
	assertNewLines(t, "have a good day!", 17, 1) // |have a good day! |

	assertNewLines(t, "cc en direct du word wrapping des familles le tests ça v a va va v a va", 46, 2)
}
