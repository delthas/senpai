package senpai

import (
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~delthas/senpai/ui"
)

const welcomeMessage = "Welcome to senpai! To get started, use the Help buttons, or enter /help for a list of commands."

func (app *App) initWindow() {
	app.win.AddBuffer("", "(home)", "")
	app.win.AddLine("", "", ui.Line{
		Head: "--",
		Body: ui.PlainString(welcomeMessage),
		At:   time.Now(),
	})
}

type statusLine struct {
	netID string
	line  ui.Line
}

func (app *App) queueStatusLine(netID string, line ui.Line) {
	if line.At.IsZero() {
		line.At = time.Now()
	}
	app.events <- event{
		src: "*",
		content: statusLine{
			netID: netID,
			line:  line,
		},
	}
}

func (app *App) addStatusLine(netID string, line ui.Line) {
	currentNetID, buffer := app.win.CurrentBuffer()
	if currentNetID == netID && buffer != "" {
		app.win.AddLine(netID, buffer, line)
	}
	app.win.AddLine(netID, "", line)
}

func (app *App) setStatus() {
	if app.imageLoading && app.win.ImageReady() {
		app.imageLoading = false
		app.imageOverlay = true
	}
	if app.imageLoading {
		app.win.SetStatus("Loading image...")
		return
	}

	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return
	}
	ts := s.Typings(buffer)
	status := ""
	if 3 < len(ts) {
		status = "several people are typing..."
	} else {
		verb := " is typing..."
		if 1 < len(ts) {
			verb = " are typing..."
			status = strings.Join(ts[:len(ts)-1], ", ") + " and "
		}
		if 0 < len(ts) {
			status += ts[len(ts)-1] + verb
		}
	}
	app.win.SetStatus(status)
}

func (app *App) setBufferNumbers() {
	input := app.win.InputContent()
	if !isCommand(input) {
		app.win.FilterBuffers(false, "")
		return
	}
	cmd, arg, _ := strings.Cut(string(input[1:]), " ")
	if cmd == "" || !strings.HasPrefix("buffer", cmd) {
		app.win.FilterBuffers(false, "")
		return
	}
	if _, err := strconv.Atoi(arg); err == nil {
		// Do not filter buffers if we are passing a buffer index
		arg = ""
	}
	app.win.FilterBuffers(true, arg)
}

func (app *App) clearBufferCommand() {
	input := app.win.InputContent()
	if !isCommand(input) {
		return
	}
	cmd, _, _ := strings.Cut(string(input[1:]), " ")
	if cmd == "" || !strings.HasPrefix("buffer", cmd) {
		return
	}
	app.win.InputClear()
}
