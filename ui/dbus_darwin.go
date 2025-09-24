//go:build darwin

package ui

import (
	"fmt"
	"os"
	"os/exec"
)

var callback func(any)

func (ui *UI) notify(target NotifyEvent, title, content string) int {
	ui.vx.Notify(title, content)
	return -1
}

func notifyClose(id int) {}

func Screenshot() error {
	f, err := os.CreateTemp("", "senpai-screenshot-*.png")
	if err != nil {
		return fmt.Errorf("opening screenshot file: %v", err)
	}
	f.Close()

	c := exec.Command("screencapture", "-i", f.Name())
	if err := c.Start(); err != nil {
		return err
	}
	go func() {
		if err := c.Wait(); err != nil {
			return
		}
		callback(&ScreenshotEvent{
			Path: f.Name(),
		})
	}()
	return nil
}

func DBusStart(f func(any)) {
	callback = f
}

func DBusStop() {}
