//go:build linux
// +build linux

package ui

import (
	"sync"

	"github.com/godbus/dbus/v5"
)

var notificationsLock sync.Mutex
var notifications = make(map[int]*NotifyEvent)

func notifyDBus(title, content string) int {
	conn, err := dbus.SessionBus()
	if err != nil {
		return -1
	}
	var r uint32
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	err = obj.Call("org.freedesktop.Notifications.Notify", 0, "senpai", uint32(0), "image-path", title, content, []string{
		"default", "Open",
	}, map[string]dbus.Variant{
		"category":      dbus.MakeVariant("im.received"),
		"desktop-entry": dbus.MakeVariant("senpai"),
		"image-path":    dbus.MakeVariant("senpai"),
		"urgency":       dbus.MakeVariant(uint8(1)), // Normal
	}, int32(-1)).Store(&r)
	if err != nil {
		return -1
	}
	return int(r)
}

func (ui *UI) notify(target NotifyEvent, title, content string) int {
	if ui.config.LocalIntegrations {
		id := notifyDBus(title, content)
		if id > 0 {
			notificationsLock.Lock()
			notifications[id] = &target
			notificationsLock.Unlock()
			return id
		}
	}

	ui.screen.Notify(title, content)
	return -1
}

func notifyClose(id int) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	obj.Call("org.freedesktop.Notifications.CloseNotification", 0, uint32(id))
}

func NotifyStart(opened func(*NotifyEvent)) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath("/org/freedesktop/Notifications"),
		dbus.WithMatchInterface("org.freedesktop.Notifications"),
		dbus.WithMatchSender("org.freedesktop.Notifications.ActionInvoked"),
	); err != nil {
		return
	}
	c := make(chan *dbus.Signal, 64)
	conn.Signal(c)
	go func() {
		for v := range c {
			switch v.Name {
			case "org.freedesktop.Notifications.NotificationClosed":
				id := int(v.Body[0].(uint32))
				notificationsLock.Lock()
				delete(notifications, id)
				notificationsLock.Unlock()
			case "org.freedesktop.Notifications.ActionInvoked":
				id := int(v.Body[0].(uint32))
				notificationsLock.Lock()
				target, ok := notifications[id]
				notificationsLock.Unlock()
				if ok {
					opened(target)
				}
			}
		}
	}()
}
