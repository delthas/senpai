package ui

import (
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

func assertIRCString(t *testing.T, input string, expected StyledString) {
	actual := IRCString(input)
	if actual.string != expected.string {
		t.Errorf("%q: expected string %q, got %q", input, expected.string, actual.string)
	}
	if len(actual.styles) != len(expected.styles) {
		t.Errorf("%q: expected %d styles, got %d", input, len(expected.styles), len(actual.styles))
		return
	}
	for i := range actual.styles {
		if actual.styles[i] != expected.styles[i] {
			t.Errorf("%q: style #%d expected to be %+v, got %+v", input, i, expected.styles[i], actual.styles[i])
		}
	}
}

func TestIRCString(t *testing.T) {
	assertIRCString(t, "", StyledString{
		string: "",
		styles: nil,
	})

	assertIRCString(t, "hello", StyledString{
		string: "hello",
		styles: nil,
	})
	assertIRCString(t, "\x02hello", StyledString{
		string: "hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Attribute: vaxis.AttrBold}},
		},
	})
	assertIRCString(t, "\x035hello", StyledString{
		string: "hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1)}},
		},
	})
	assertIRCString(t, "\x0305hello", StyledString{
		string: "hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1)}},
		},
	})
	assertIRCString(t, "\x0305,0hello", StyledString{
		string: "hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1), Background: vaxis.IndexColor(15)}},
		},
	})
	assertIRCString(t, "\x035,00hello", StyledString{
		string: "hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1), Background: vaxis.IndexColor(15)}},
		},
	})
	assertIRCString(t, "\x0305,00hello", StyledString{
		string: "hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1), Background: vaxis.IndexColor(15)}},
		},
	})

	assertIRCString(t, "\x035,hello", StyledString{
		string: ",hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1)}},
		},
	})
	assertIRCString(t, "\x0305,hello", StyledString{
		string: ",hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1)}},
		},
	})
	assertIRCString(t, "\x03050hello", StyledString{
		string: "0hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1)}},
		},
	})
	assertIRCString(t, "\x0305,000hello", StyledString{
		string: "0hello",
		styles: []rangedStyle{
			{Start: 0, Style: vaxis.Style{Foreground: vaxis.IndexColor(1), Background: vaxis.IndexColor(15)}},
		},
	})
}
