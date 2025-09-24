//go:build !linux

package ui

import "fmt"

func (ui *UI) notify(target NotifyEvent, title, content string) int {
	ui.vx.Notify(title, content)
	return -1
}

func notifyClose(id int) {}

func Screenshot() error {
	return fmt.Errorf("failed to take screenshot: D-Bus is disabled")
}

func DBusStart(callback func(any)) {}

func DBusStop() {}
