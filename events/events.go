package events

import (
	"image"

	"git.sr.ht/~rockorager/vaxis"
)

type EventClickSetEvent interface {
	SetEvent(vaxis.Mouse)
}

type EventClick struct {
	Event  vaxis.Mouse
	NetID  string
	Buffer string
}

func (e *EventClick) SetEvent(ev vaxis.Mouse) {
	e.Event = ev
}

type EventClickNick struct {
	EventClick
	Nick string
}

type EventClickLink struct {
	EventClick
	Link  string
	Mouse bool
}

type EventImageLoaded struct {
	Image image.Image // nil if error
}

type EventFileUpload struct {
	Progress float64
	Location string
	Error    string
}
