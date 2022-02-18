package senpai

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/proxy"

	"git.sr.ht/~taiite/senpai/irc"
	"git.sr.ht/~taiite/senpai/ui"
	"github.com/gdamore/tcell/v2"
)

const eventChanSize = 64

func isCommand(input []rune) bool {
	// Command can't start with two slashes because that's an escape for
	// a literal slash in the message
	return len(input) >= 1 && input[0] == '/' && !(len(input) >= 2 && input[1] == '/')
}

type bound struct {
	first time.Time
	last  time.Time

	firstMessage string
	lastMessage  string
}

// Compare returns 0 if line is within bounds, -1 if before, 1 if after.
func (b *bound) Compare(line *ui.Line) int {
	at := line.At.Truncate(time.Second)
	if at.Before(b.first) {
		return -1
	}
	if at.After(b.last) {
		return 1
	}
	if at.Equal(b.first) && line.Body.String() != b.firstMessage {
		return -1
	}
	if at.Equal(b.last) && line.Body.String() != b.lastMessage {
		return -1
	}
	return 0
}

// Update updates the bounds to include the given line.
func (b *bound) Update(line *ui.Line) {
	if line.At.IsZero() {
		return
	}
	at := line.At.Truncate(time.Second)
	if b.first.IsZero() || at.Before(b.first) {
		b.first = at
		b.firstMessage = line.Body.String()
	} else if b.last.IsZero() || at.After(b.last) {
		b.last = at
		b.lastMessage = line.Body.String()
	}
}

// IsZero reports whether the bound is empty.
func (b *bound) IsZero() bool {
	return b.first.IsZero()
}

type event struct {
	src     string // "*" if UI, netID otherwise
	content interface{}
}

type boundKey struct {
	netID  string
	target string
}

type App struct {
	win      *ui.UI
	sessions map[string]*irc.Session
	pasting  bool
	events   chan event

	cfg        Config
	highlights []string

	lastQuery     string
	lastQueryNet  string
	messageBounds map[boundKey]bound
	lastNetID     string
	lastBuffer    string

	monitor map[string]map[string]struct{} // set of targets we want to monitor per netID, best-effort. netID->target->{}

	lastMessageTime time.Time
	lastCloseTime   time.Time
}

func NewApp(cfg Config) (app *App, err error) {
	app = &App{
		sessions:      map[string]*irc.Session{},
		events:        make(chan event, eventChanSize),
		cfg:           cfg,
		messageBounds: map[boundKey]bound{},
		monitor:       make(map[string]map[string]struct{}),
	}

	if cfg.Highlights != nil {
		app.highlights = make([]string, len(cfg.Highlights))
		for i := range app.highlights {
			app.highlights[i] = strings.ToLower(cfg.Highlights[i])
		}
	}

	mouse := cfg.Mouse

	app.win, err = ui.New(ui.Config{
		NickColWidth:   cfg.NickColWidth,
		ChanColWidth:   cfg.ChanColWidth,
		MemberColWidth: cfg.MemberColWidth,
		AutoComplete: func(cursorIdx int, text []rune) []ui.Completion {
			return app.completions(cursorIdx, text)
		},
		Mouse: mouse,
		MergeLine: func(former *ui.Line, addition ui.Line) {
			app.mergeLine(former, addition)
		},
	})
	if err != nil {
		return
	}
	app.win.SetPrompt(ui.Styled(">",
		tcell.
			StyleDefault.
			Foreground(tcell.Color(app.cfg.Colors.Prompt))),
	)

	app.initWindow()

	return
}

func (app *App) Close() {
	app.win.Exit()       // tell all instances of app.ircLoop to stop when possible
	app.events <- event{ // tell app.eventLoop to stop
		src:     "*",
		content: nil,
	}
	for _, session := range app.sessions {
		session.Close()
	}
}

func (app *App) SwitchToBuffer(netID, buffer string) {
	app.lastNetID = netID
	app.lastBuffer = buffer
}

func (app *App) Run() {
	if app.lastCloseTime.IsZero() {
		app.lastCloseTime = time.Now()
	}
	go app.uiLoop()
	go app.ircLoop("")
	app.eventLoop()
}

func (app *App) CurrentSession() *irc.Session {
	netID, _ := app.win.CurrentBuffer()
	return app.sessions[netID]
}

func (app *App) CurrentBuffer() (netID, buffer string) {
	return app.win.CurrentBuffer()
}

func (app *App) LastMessageTime() time.Time {
	return app.lastMessageTime
}

func (app *App) SetLastClose(t time.Time) {
	app.lastCloseTime = t
}

// eventLoop retrieves events (in batches) from the event channel and handle
// them, then draws the interface after each batch is handled.
func (app *App) eventLoop() {
	defer app.win.Close()

	evs := make([]event, 0, eventChanSize)
	for !app.win.ShouldExit() {
		ev := <-app.events
		evs = evs[:0]
		evs = append(evs, ev)
	Batch:
		for i := 0; i < eventChanSize; i++ {
			select {
			case ev := <-app.events:
				evs = append(evs, ev)
			default:
				break Batch
			}
		}

		for _, ev := range evs {
			if ev.src == "*" {
				if ev.content == nil {
					return
				}
				if !app.handleUIEvent(ev.content) {
					return
				}
			} else {
				app.handleIRCEvent(ev.src, ev.content)
			}
		}

		if !app.pasting {
			if netID, buffer, timestamp := app.win.UpdateRead(); buffer != "" {
				s := app.sessions[netID]
				if s != nil {
					s.ReadSet(buffer, timestamp)
				}
			}
			app.setStatus()
			app.updatePrompt()
			app.setBufferNumbers()
			var currentMembers []irc.Member
			netID, buffer := app.win.CurrentBuffer()
			s := app.sessions[netID]
			if s != nil && buffer != "" {
				currentMembers = s.Names(buffer)
			}
			app.win.Draw(currentMembers)
		}
	}
}

// ircLoop maintains a connection to the IRC server by connecting and then
// forwarding IRC events to app.events repeatedly.
func (app *App) ircLoop(netID string) {
	var auth irc.SASLClient
	if app.cfg.Password != nil {
		auth = &irc.SASLPlain{
			Username: app.cfg.User,
			Password: *app.cfg.Password,
		}
	}
	params := irc.SessionParams{
		Nickname: app.cfg.Nick,
		Username: app.cfg.User,
		RealName: app.cfg.Real,
		NetID:    netID,
		Auth:     auth,
	}
	for !app.win.ShouldExit() {
		conn := app.connect(netID)
		in, out := irc.ChanInOut(conn)
		if app.cfg.Debug {
			out = app.debugOutputMessages(netID, out)
		}
		session := irc.NewSession(out, params)
		app.events <- event{
			src:     netID,
			content: session,
		}
		go func() {
			for stop := range session.TypingStops() {
				app.events <- event{
					src:     netID,
					content: stop,
				}
			}
		}()
		for msg := range in {
			if app.cfg.Debug {
				app.queueStatusLine(netID, ui.Line{
					At:   time.Now(),
					Head: "IN --",
					Body: ui.PlainString(msg.String()),
				})
			}
			app.events <- event{
				src:     netID,
				content: msg,
			}
		}
		app.events <- event{
			src:     netID,
			content: nil,
		}
		app.queueStatusLine(netID, ui.Line{
			Head:      "!!",
			HeadColor: tcell.ColorRed,
			Body:      ui.PlainString("Connection lost"),
		})
		if app.win.ShouldExit() {
			break
		}
		time.Sleep(10 * time.Second)
	}
}

func (app *App) connect(netID string) net.Conn {
	for {
		app.queueStatusLine(netID, ui.Line{
			Head: "--",
			Body: ui.PlainSprintf("Connecting to %s...", app.cfg.Addr),
		})
		conn, err := app.tryConnect()
		if err == nil {
			return conn
		}
		app.queueStatusLine(netID, ui.Line{
			Head:      "!!",
			HeadColor: tcell.ColorRed,
			Body:      ui.PlainSprintf("Connection failed: %v", err),
		})
		time.Sleep(1 * time.Minute)
	}
}

func (app *App) tryConnect() (conn net.Conn, err error) {
	addr := app.cfg.Addr
	colonIdx := strings.LastIndexByte(addr, ':')
	bracketIdx := strings.LastIndexByte(addr, ']')
	if colonIdx <= bracketIdx {
		// either colonIdx < 0, or the last colon is before a ']' (end
		// of IPv6 address. -> missing port
		if app.cfg.TLS {
			addr += ":6697"
		} else {
			addr += ":6667"
		}
	}

	conn, err = proxy.FromEnvironment().Dial("tcp", addr)
	if err != nil {
		return
	}

	if app.cfg.TLS {
		host, _, _ := net.SplitHostPort(addr) // should succeed since net.Dial did.
		conn = tls.Client(conn, &tls.Config{
			ServerName: host,
			NextProtos: []string{"irc"},
		})
		err = conn.(*tls.Conn).Handshake()
		if err != nil {
			conn.Close()
			return nil, err
		}
	}

	return
}

func (app *App) debugOutputMessages(netID string, out chan<- irc.Message) chan<- irc.Message {
	debugOut := make(chan irc.Message, cap(out))
	go func() {
		for msg := range debugOut {
			app.queueStatusLine(netID, ui.Line{
				At:   time.Now(),
				Head: "OUT --",
				Body: ui.PlainString(msg.String()),
			})
			out <- msg
		}
		close(out)
	}()
	return debugOut
}

// uiLoop retrieves events from the UI and forwards them to app.events for
// handling in app.eventLoop().
func (app *App) uiLoop() {
	for ev := range app.win.Events {
		app.events <- event{
			src:     "*",
			content: ev,
		}
	}
}

func (app *App) handleUIEvent(ev interface{}) bool {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		app.win.Resize()
	case *tcell.EventPaste:
		app.pasting = ev.Start()
	case *tcell.EventMouse:
		app.handleMouseEvent(ev)
	case *tcell.EventKey:
		app.handleKeyEvent(ev)
	case *tcell.EventError:
		// happens when the terminal is closing: in which case, exit
		return false
	case statusLine:
		app.addStatusLine(ev.netID, ev.line)
	default:
		panic("unreachable")
	}
	return true
}

func (app *App) handleMouseEvent(ev *tcell.EventMouse) {
	x, y := ev.Position()
	w, _ := app.win.Size()
	if ev.Buttons()&tcell.WheelUp != 0 {
		if x < app.cfg.ChanColWidth {
			app.win.ScrollChannelUpBy(4)
		} else if x > w-app.cfg.MemberColWidth {
			app.win.ScrollMemberUpBy(4)
		} else {
			app.win.ScrollUpBy(4)
			app.requestHistory()
		}
	}
	if ev.Buttons()&tcell.WheelDown != 0 {
		if x < app.cfg.ChanColWidth {
			app.win.ScrollChannelDownBy(4)
		} else if x > w-app.cfg.MemberColWidth {
			app.win.ScrollMemberDownBy(4)
		} else {
			app.win.ScrollDownBy(4)
		}
	}
	if ev.Buttons()&tcell.ButtonPrimary != 0 {
		if x < app.cfg.ChanColWidth {
			app.win.ClickBuffer(y + app.win.ChannelOffset())
		} else if x > w-app.cfg.MemberColWidth {
			app.win.ClickMember(y + app.win.MemberOffset())
		}
	}
	if ev.Buttons() == 0 {
		if x < app.cfg.ChanColWidth {
			if i := y + app.win.ChannelOffset(); i == app.win.ClickedBuffer() {
				app.win.GoToBufferNo(i)
			}
		} else if x > w-app.cfg.MemberColWidth {
			if i := y + app.win.MemberOffset(); i == app.win.ClickedMember() {
				netID, target := app.win.CurrentBuffer()
				s := app.sessions[netID]
				if s != nil && target != "" {
					members := s.Names(target)
					if i < len(members) {
						buffer := members[i].Name.Name
						i, added := app.win.AddBuffer(netID, "", buffer)
						app.win.JumpBufferIndex(i)
						if added {
							s.MonitorAdd(buffer)
							s.ReadGet(buffer)
							s.NewHistoryRequest(buffer).WithLimit(500).Before(time.Now())
						}
					}
				}
			}
		}
		app.win.ClickBuffer(-1)
		app.win.ClickMember(-1)
	}
}

func (app *App) handleKeyEvent(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		if app.win.InputClear() {
			app.typing()
		} else {
			app.win.InputSet("/quit")
		}
	case tcell.KeyCtrlL:
		app.win.Resize()
	case tcell.KeyCtrlU, tcell.KeyPgUp:
		app.win.ScrollUp()
		app.requestHistory()
	case tcell.KeyCtrlD, tcell.KeyPgDn:
		app.win.ScrollDown()
	case tcell.KeyCtrlN:
		app.win.NextBuffer()
	case tcell.KeyCtrlP:
		app.win.PreviousBuffer()
	case tcell.KeyRight:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.NextBuffer()
		} else if ev.Modifiers() == tcell.ModCtrl {
			app.win.InputRightWord()
		} else {
			app.win.InputRight()
		}
	case tcell.KeyLeft:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.PreviousBuffer()
		} else if ev.Modifiers() == tcell.ModCtrl {
			app.win.InputLeftWord()
		} else {
			app.win.InputLeft()
		}
	case tcell.KeyUp:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.PreviousBuffer()
		} else {
			app.win.InputUp()
		}
	case tcell.KeyDown:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.NextBuffer()
		} else {
			app.win.InputDown()
		}
	case tcell.KeyHome:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.GoToBufferNo(0)
		} else {
			app.win.InputHome()
		}
	case tcell.KeyEnd:
		if ev.Modifiers() == tcell.ModAlt {
			maxInt := int(^uint(0) >> 1)
			app.win.GoToBufferNo(maxInt)
		} else {
			app.win.InputEnd()
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		ok := app.win.InputBackspace()
		if ok {
			app.typing()
		}
	case tcell.KeyDelete:
		ok := app.win.InputDelete()
		if ok {
			app.typing()
		}
	case tcell.KeyCtrlW:
		ok := app.win.InputDeleteWord()
		if ok {
			app.typing()
		}
	case tcell.KeyCtrlR:
		app.win.InputBackSearch()
	case tcell.KeyTab:
		ok := app.win.InputAutoComplete(1)
		if ok {
			app.typing()
		}
	case tcell.KeyBacktab:
		ok := app.win.InputAutoComplete(-1)
		if ok {
			app.typing()
		}
	case tcell.KeyCR, tcell.KeyLF:
		netID, buffer := app.win.CurrentBuffer()
		input := app.win.InputEnter()
		err := app.handleInput(buffer, input)
		if err != nil {
			app.win.AddLine(netID, buffer, ui.NotifyUnread, ui.Line{
				At:        time.Now(),
				Head:      "!!",
				HeadColor: tcell.ColorRed,
				Body:      ui.PlainSprintf("%q: %s", input, err),
			})
		}
	case tcell.KeyRune:
		if ev.Modifiers() == tcell.ModAlt {
			switch ev.Rune() {
			case 'n':
				app.win.ScrollDownHighlight()
			case 'p':
				app.win.ScrollUpHighlight()
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				app.win.GoToBufferNo(int(ev.Rune()-'0') - 1)
			}
		} else {
			app.win.InputRune(ev.Rune())
			app.typing()
		}
	default:
		return
	}
}

// requestHistory is a wrapper around irc.Session.RequestHistory to only request
// history when needed.
func (app *App) requestHistory() {
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return
	}
	if app.win.IsAtTop() && buffer != "" {
		t := time.Now()
		if bound, ok := app.messageBounds[boundKey{netID, buffer}]; ok {
			t = bound.first
		}
		s.NewHistoryRequest(buffer).
			WithLimit(200).
			Before(t)
	}
}

func (app *App) handleIRCEvent(netID string, ev interface{}) {
	if ev == nil {
		if s, ok := app.sessions[netID]; ok {
			s.Close()
			delete(app.sessions, netID)
		}
		return
	}
	if s, ok := ev.(*irc.Session); ok {
		if s, ok := app.sessions[netID]; ok {
			s.Close()
		}
		app.sessions[netID] = s
		if _, ok := app.monitor[netID]; !ok {
			app.monitor[netID] = make(map[string]struct{})
		}
		return
	}
	if _, ok := ev.(irc.Typing); ok {
		// Just refresh the screen.
		return
	}

	msg, ok := ev.(irc.Message)
	if !ok {
		panic("unreachable")
	}
	s, ok := app.sessions[netID]
	if !ok {
		panic("unreachable")
	}

	// Mutate IRC state
	ev, err := s.HandleMessage(msg)
	if err != nil {
		app.win.AddLine(netID, "", ui.NotifyUnread, ui.Line{
			Head:      "!!",
			HeadColor: tcell.ColorRed,
			Body:      ui.PlainSprintf("Received corrupt message %q: %s", msg.String(), err),
		})
		return
	}
	t := msg.TimeOrNow()
	if t.After(app.lastMessageTime) {
		app.lastMessageTime = t
	}

	// Mutate UI state
	switch ev := ev.(type) {
	case irc.RegisteredEvent:
		for _, channel := range app.cfg.Channels {
			// TODO: group JOIN messages
			// TODO: support autojoining channels with keys
			s.Join(channel, "")
		}
		s.NewHistoryRequest("").
			WithLimit(1000).
			Targets(app.lastCloseTime, msg.TimeOrNow())
		body := "Connected to the server"
		if s.Nick() != app.cfg.Nick {
			body = fmt.Sprintf("Connected to the server as %s", s.Nick())
		}
		app.win.AddLine(netID, "", ui.NotifyNone, ui.Line{
			At:   msg.TimeOrNow(),
			Head: "--",
			Body: ui.PlainString(body),
		})
		for target := range app.monitor[s.NetID()] {
			// TODO: batch MONITOR +
			s.MonitorAdd(target)
		}
	case irc.SelfNickEvent:
		var body ui.StyledStringBuilder
		body.WriteString(fmt.Sprintf("%s\u2192%s", ev.FormerNick, s.Nick()))
		textStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
		arrowStyle := tcell.StyleDefault
		body.AddStyle(0, textStyle)
		body.AddStyle(len(ev.FormerNick), arrowStyle)
		body.AddStyle(body.Len()-len(s.Nick()), textStyle)
		app.addStatusLine(netID, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Highlight: true,
		})
	case irc.UserNickEvent:
		line := app.formatEvent(ev)
		for _, c := range s.ChannelsSharedWith(ev.User) {
			app.win.AddLine(netID, c, ui.NotifyNone, line)
		}
	case irc.SelfJoinEvent:
		i, added := app.win.AddBuffer(netID, "", ev.Channel)
		bounds, ok := app.messageBounds[boundKey{netID, ev.Channel}]
		if added || !ok {
			s.NewHistoryRequest(ev.Channel).
				WithLimit(500).
				Before(msg.TimeOrNow())
		} else {
			s.NewHistoryRequest(ev.Channel).
				WithLimit(1000).
				After(bounds.last)
		}
		if ev.Requested {
			app.win.JumpBufferIndex(i)
		}
		if ev.Topic != "" {
			topic := ui.IRCString(ev.Topic).String()
			app.win.SetTopic(netID, ev.Channel, topic)
		}

		// Restore last buffer
		if netID == app.lastNetID && ev.Channel == app.lastBuffer {
			app.win.JumpBufferNetwork(app.lastNetID, app.lastBuffer)
			app.lastNetID = ""
			app.lastBuffer = ""
		}
	case irc.UserJoinEvent:
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, ui.NotifyNone, line)
	case irc.SelfPartEvent:
		app.win.RemoveBuffer(netID, ev.Channel)
		delete(app.messageBounds, boundKey{netID, ev.Channel})
	case irc.UserPartEvent:
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, ui.NotifyNone, line)
	case irc.UserQuitEvent:
		line := app.formatEvent(ev)
		for _, c := range ev.Channels {
			app.win.AddLine(netID, c, ui.NotifyNone, line)
		}
	case irc.TopicChangeEvent:
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, ui.NotifyUnread, line)
		topic := ui.IRCString(ev.Topic).String()
		app.win.SetTopic(netID, ev.Channel, topic)
	case irc.ModeChangeEvent:
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, ui.NotifyNone, line)
	case irc.InviteEvent:
		var buffer string
		var notify ui.NotifyType
		var body string
		if s.IsMe(ev.Invitee) {
			buffer = ""
			notify = ui.NotifyHighlight
			body = fmt.Sprintf("%s invited you to join %s", ev.Inviter, ev.Channel)
		} else if s.IsMe(ev.Inviter) {
			buffer = ev.Channel
			notify = ui.NotifyNone
			body = fmt.Sprintf("You invited %s to join this channel", ev.Invitee)
		} else {
			buffer = ev.Channel
			notify = ui.NotifyUnread
			body = fmt.Sprintf("%s invited %s to join this channel", ev.Inviter, ev.Invitee)
		}
		app.win.AddLine(netID, buffer, notify, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(tcell.ColorGray)),
			Highlight: notify == ui.NotifyHighlight,
		})
	case irc.MessageEvent:
		buffer, line, notification := app.formatMessage(s, ev)
		if buffer != "" && !s.IsChannel(buffer) {
			if _, added := app.win.AddBuffer(netID, "", buffer); added {
				app.monitor[netID][buffer] = struct{}{}
				s.MonitorAdd(buffer)
				s.ReadGet(buffer)
				s.NewHistoryRequest(buffer).
					WithLimit(500).
					Before(msg.TimeOrNow())
			}
		}
		app.win.AddLine(netID, buffer, notification, line)
		if notification == ui.NotifyHighlight {
			app.notifyHighlight(buffer, ev.User, line.Body.String())
		}
		if !s.IsChannel(msg.Params[0]) && !s.IsMe(ev.User) {
			app.lastQuery = msg.Prefix.Name
			app.lastQueryNet = netID
		}
		bounds := app.messageBounds[boundKey{netID, ev.Target}]
		bounds.Update(&line)
		app.messageBounds[boundKey{netID, buffer}] = bounds
	case irc.HistoryTargetsEvent:
		for target, last := range ev.Targets {
			if s.IsChannel(target) {
				continue
			}
			s.MonitorAdd(target)
			s.ReadGet(target)
			app.win.AddBuffer(netID, "", target)
			// CHATHISTORY BEFORE excludes its bound, so add 1ms
			// (precision of the time tag) to include that last message.
			last = last.Add(1 * time.Millisecond)
			s.NewHistoryRequest(target).
				WithLimit(500).
				Before(last)
		}
	case irc.HistoryEvent:
		var linesBefore []ui.Line
		var linesAfter []ui.Line
		bounds, hasBounds := app.messageBounds[boundKey{netID, ev.Target}]
		for _, m := range ev.Messages {
			var line ui.Line
			switch ev := m.(type) {
			case irc.MessageEvent:
				_, line, _ = app.formatMessage(s, ev)
			default:
				line = app.formatEvent(ev)
			}
			if line.IsZero() {
				continue
			}
			if hasBounds {
				c := bounds.Compare(&line)
				if c < 0 {
					linesBefore = append(linesBefore, line)
				} else if c > 0 {
					linesAfter = append(linesAfter, line)
				}
			} else {
				linesBefore = append(linesBefore, line)
			}
		}
		app.win.AddLines(netID, ev.Target, linesBefore, linesAfter)
		if len(linesBefore) != 0 {
			bounds.Update(&linesBefore[0])
			bounds.Update(&linesBefore[len(linesBefore)-1])
		}
		if len(linesAfter) != 0 {
			bounds.Update(&linesAfter[0])
			bounds.Update(&linesAfter[len(linesAfter)-1])
		}
		if !bounds.IsZero() {
			app.messageBounds[boundKey{netID, ev.Target}] = bounds
		}
	case irc.ReadEvent:
		app.win.SetRead(netID, ev.Target, ev.Timestamp)
	case irc.BouncerNetworkEvent:
		_, added := app.win.AddBuffer(ev.ID, ev.Name, "")
		if added {
			go app.ircLoop(ev.ID)
		}
	case irc.ErrorEvent:
		if isBlackListed(msg.Command) {
			break
		}
		var head string
		var body string
		switch ev.Severity {
		case irc.SeverityFail:
			head = "--"
			body = fmt.Sprintf("Error (code %s): %s", ev.Code, ev.Message)
		case irc.SeverityWarn:
			head = "--"
			body = fmt.Sprintf("Warning (code %s): %s", ev.Code, ev.Message)
		case irc.SeverityNote:
			head = ev.Code + " --"
			body = ev.Message
		default:
			panic("unreachable")
		}
		app.addStatusLine(netID, ui.Line{
			At:   msg.TimeOrNow(),
			Head: head,
			Body: ui.PlainString(body),
		})
	}
}

func isBlackListed(command string) bool {
	switch command {
	case "002", "003", "004", "422":
		// useless connection messages
		return true
	}
	return false
}

// isHighlight reports whether the given message content is a highlight.
func (app *App) isHighlight(s *irc.Session, content string) bool {
	contentCf := s.Casemap(content)
	if app.highlights == nil {
		return strings.Contains(contentCf, s.NickCf())
	}
	for _, h := range app.highlights {
		if strings.Contains(contentCf, s.Casemap(h)) {
			return true
		}
	}
	return false
}

// notifyHighlight executes the script at "on-highlight-path" according to the given
// message context.
func (app *App) notifyHighlight(buffer, nick, content string) {
	path := app.cfg.OnHighlightPath
	if path == "" {
		defaultHighlightPath, err := DefaultHighlightPath()
		if err != nil {
			return
		}
		path = defaultHighlightPath
	}

	netID, curBuffer := app.win.CurrentBuffer()
	if _, err := os.Stat(app.cfg.OnHighlightPath); errors.Is(err, os.ErrNotExist) {
		// only error out if the user specified a highlight path
		// if default path unreachable, simple bail
		if app.cfg.OnHighlightPath != "" {
			body := fmt.Sprintf("Unable to find on-highlight command at path: %q", app.cfg.OnHighlightPath)
			app.addStatusLine(netID, ui.Line{
				At:        time.Now(),
				Head:      "!!",
				HeadColor: tcell.ColorRed,
				Body:      ui.PlainString(body),
			})
		}
		return
	}
	here := "0"
	if buffer == curBuffer { // TODO also check netID
		here = "1"
	}
	cmd := exec.Command(app.cfg.OnHighlightPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("BUFFER=%s", buffer),
		fmt.Sprintf("HERE=%s", here),
		fmt.Sprintf("SENDER=%s", nick),
		fmt.Sprintf("MESSAGE=%s", content),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		body := fmt.Sprintf("Failed to invoke on-highlight command at path: %v. Output: %q", err, string(output))
		app.addStatusLine(netID, ui.Line{
			At:        time.Now(),
			Head:      "!!",
			HeadColor: tcell.ColorRed,
			Body:      ui.PlainString(body),
		})
	}
}

// typing sends typing notifications to the IRC server according to the user
// input.
func (app *App) typing() {
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil || !app.cfg.Typings {
		return
	}
	if buffer == "" {
		return
	}
	input := app.win.InputContent()
	if len(input) == 0 {
		s.TypingStop(buffer)
	} else if !isCommand(input) {
		s.Typing(buffer)
	}
}

// completions computes the list of completions given the input text and the
// cursor position.
func (app *App) completions(cursorIdx int, text []rune) []ui.Completion {
	if len(text) == 0 {
		return nil
	}
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return nil
	}

	var cs []ui.Completion
	if buffer != "" {
		cs = app.completionsChannelTopic(cs, cursorIdx, text)
		cs = app.completionsChannelMembers(cs, cursorIdx, text)
	}
	cs = app.completionsMsg(cs, cursorIdx, text)
	cs = app.completionsCommands(cs, cursorIdx, text)

	if cs != nil {
		cs = append(cs, ui.Completion{
			Text:      text,
			CursorIdx: cursorIdx,
		})
	}

	return cs
}

// formatEvent returns a formatted ui.Line for an irc.Event.
func (app *App) formatEvent(ev irc.Event) ui.Line {
	switch ev := ev.(type) {
	case irc.UserNickEvent:
		var body ui.StyledStringBuilder
		body.WriteString(fmt.Sprintf("%s\u2192%s", ev.FormerNick, ev.User))
		textStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
		arrowStyle := tcell.StyleDefault
		body.AddStyle(0, textStyle)
		body.AddStyle(len(ev.FormerNick), arrowStyle)
		body.AddStyle(body.Len()-len(ev.User), textStyle)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
		}
	case irc.UserJoinEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGreen))
		body.WriteByte('+')
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		body.WriteString(ev.User)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
		}
	case irc.UserPartEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorRed))
		body.WriteByte('-')
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		body.WriteString(ev.User)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
		}
	case irc.UserQuitEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorRed))
		body.WriteByte('-')
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		body.WriteString(ev.User)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
		}
	case irc.TopicChangeEvent:
		topic := ui.IRCString(ev.Topic).String()
		body := fmt.Sprintf("Topic changed to: %s", topic)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(tcell.ColorGray)),
		}
	case irc.ModeChangeEvent:
		body := fmt.Sprintf("[%s]", ev.Mode)
		// simple mode event: <+/-><mode> <nick>
		mergeable := len(strings.Split(ev.Mode, " ")) == 2
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(tcell.ColorGray)),
			Mergeable: mergeable,
			Data:      []irc.Event{ev},
		}
	default:
		return ui.Line{}
	}
}

// formatMessage sets how a given message must be formatted.
//
// It computes three things:
// - which buffer the message must be added to,
// - the UI line,
// - what kind of notification senpai should send.
func (app *App) formatMessage(s *irc.Session, ev irc.MessageEvent) (buffer string, line ui.Line, notification ui.NotifyType) {
	isFromSelf := s.IsMe(ev.User)
	isToSelf := s.IsMe(ev.Target)
	isHighlight := app.isHighlight(s, ev.Content)
	isAction := strings.HasPrefix(ev.Content, "\x01ACTION")
	isQuery := !ev.TargetIsChannel && ev.Command == "PRIVMSG"
	isNotice := ev.Command == "NOTICE"

	if !ev.TargetIsChannel && isNotice {
		curNetID, curBuffer := app.win.CurrentBuffer()
		if curNetID == s.NetID() {
			buffer = curBuffer
		} else {
			isHighlight = true
		}
	} else if isToSelf {
		buffer = ev.User
	} else {
		buffer = ev.Target
	}

	hlLine := ev.TargetIsChannel && isHighlight && !isFromSelf
	if isFromSelf {
		notification = ui.NotifyNone
	} else if isHighlight || isQuery {
		notification = ui.NotifyHighlight
	} else {
		notification = ui.NotifyUnread
	}

	head := ev.User
	headColor := tcell.ColorWhite
	if isAction || isNotice {
		head = "*"
	} else {
		headColor = identColor(head)
	}

	content := strings.TrimSuffix(ev.Content, "\x01")
	content = strings.TrimRightFunc(content, unicode.IsSpace)
	if isAction {
		content = content[7:]
	}
	var body ui.StyledStringBuilder
	if isNotice {
		color := identColor(ev.User)
		body.SetStyle(tcell.StyleDefault.Foreground(color))
		body.WriteString(ev.User)
		body.SetStyle(tcell.StyleDefault)
		body.WriteString(": ")
		body.WriteStyledString(ui.IRCString(content))
	} else if isAction {
		color := identColor(ev.User)
		body.SetStyle(tcell.StyleDefault.Foreground(color))
		body.WriteString(ev.User)
		body.SetStyle(tcell.StyleDefault)
		body.WriteStyledString(ui.IRCString(content))
	} else {
		body.WriteStyledString(ui.IRCString(content))
	}

	line = ui.Line{
		At:        ev.Time,
		Head:      head,
		HeadColor: headColor,
		Body:      body.StyledString(),
		Highlight: hlLine,
	}
	return
}

func (app *App) mergeLine(former *ui.Line, addition ui.Line) {
	events := append(former.Data.([]irc.Event), addition.Data.([]irc.Event)...)
	type flow struct {
		hide  bool
		state int // -1: newly offline; 1: newly online
	}
	flows := make(map[string]*flow)

	eventFlows := make([]*flow, len(events))

	for i, ev := range events {
		switch ev := ev.(type) {
		case irc.UserNickEvent:
			userCf := strings.ToLower(ev.User)
			f, ok := flows[strings.ToLower(ev.FormerNick)]
			if ok {
				flows[userCf] = f
				delete(flows, strings.ToLower(ev.FormerNick))
				eventFlows[i] = f
			} else {
				f = &flow{}
				flows[userCf] = f
				eventFlows[i] = f
			}
		case irc.UserJoinEvent:
			userCf := strings.ToLower(ev.User)
			f, ok := flows[userCf]
			if ok {
				if f.state == -1 {
					f.hide = true
					delete(flows, userCf)
				}
			} else {
				f = &flow{
					state: 1,
				}
				flows[userCf] = f
				eventFlows[i] = f
			}
		case irc.UserPartEvent:
			userCf := strings.ToLower(ev.User)
			f, ok := flows[userCf]
			if ok {
				if f.state == 1 {
					f.hide = true
					delete(flows, userCf)
				}
			} else {
				f = &flow{
					state: -1,
				}
				flows[userCf] = f
				eventFlows[i] = f
			}
		case irc.UserQuitEvent:
			userCf := strings.ToLower(ev.User)
			f, ok := flows[userCf]
			if ok {
				if f.state == 1 {
					f.hide = true
					delete(flows, userCf)
				}
			} else {
				f = &flow{
					state: -1,
				}
				flows[userCf] = f
				eventFlows[i] = f
			}
		case irc.ModeChangeEvent:
			userCf := strings.ToLower(strings.Split(ev.Mode, " ")[1])
			f, ok := flows[userCf]
			if ok {
				eventFlows[i] = f
			} else {
				f = &flow{}
				flows[userCf] = f
				eventFlows[i] = f
			}
		}
	}

	newBody := new(ui.StyledStringBuilder)
	newBody.Grow(128)
	first := true
	for i, ev := range events {
		if f := eventFlows[i]; f == nil || f.hide {
			continue
		}
		l := app.formatEvent(ev)
		if first {
			first = false
		} else {
			newBody.WriteString("  ")
		}
		newBody.WriteStyledString(l.Body)
	}
	former.Body = newBody.StyledString()
	former.Data = events
}

// updatePrompt changes the prompt text according to the application context.
func (app *App) updatePrompt() {
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	command := isCommand(app.win.InputContent())
	var prompt ui.StyledString
	if buffer == "" || command {
		prompt = ui.Styled(">",
			tcell.
				StyleDefault.
				Foreground(tcell.Color(app.cfg.Colors.Prompt)),
		)
	} else if s == nil {
		prompt = ui.Styled("<offline>",
			tcell.
				StyleDefault.
				Foreground(tcell.ColorRed),
		)
	} else {
		prompt = identString(s.Nick())
	}
	app.win.SetPrompt(prompt)
}

func (app *App) printTopic(netID, buffer string) (ok bool) {
	var body string
	s := app.sessions[netID]
	if s == nil {
		return false
	}
	topic, who, at := s.Topic(buffer)
	topic = ui.IRCString(topic).String()
	if who == nil {
		body = fmt.Sprintf("Topic: %s", topic)
	} else {
		body = fmt.Sprintf("Topic (by %s, %s): %s", who, at.Local().Format("Mon Jan 2 15:04:05"), topic)
	}
	app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
		At:        time.Now(),
		Head:      "--",
		HeadColor: tcell.ColorGray,
		Body:      ui.Styled(body, tcell.StyleDefault.Foreground(tcell.ColorGray)),
	})
	return true
}
