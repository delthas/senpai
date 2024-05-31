//go:build !linux
// +build !linux

package ui

func (ui *UI) notify(target NotifyEvent, title, content string) int {
	ui.screen.Notify(title, content)
	return -1
}

func notifyClose(id int) {}

func NotifyStart(f func(event NotifyEvent)) {}
