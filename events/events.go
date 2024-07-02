package events

import "image"

type EventClick struct {
	NetID  string
	Buffer string
}

type EventClickNick struct {
	EventClick
	Nick string
}

type EventClickLink struct {
	EventClick
	Link string
}

type EventImageLoaded struct {
	Image image.Image // nil if error
}
