package senpai

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

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
	if line.At.Before(b.first) {
		return -1
	}
	if line.At.After(b.last) {
		return 1
	}
	if line.At.Equal(b.first) && line.Body.String() != b.firstMessage {
		return -1
	}
	if line.At.Equal(b.last) && line.Body.String() != b.lastMessage {
		return -1
	}
	return 0
}

// Update updates the bounds to include the given line.
func (b *bound) Update(line *ui.Line) {
	if line.At.IsZero() {
		return
	}
	if b.first.IsZero() || line.At.Before(b.first) {
		b.first = line.At
		b.firstMessage = line.Body.String()
	} else if b.last.IsZero() || line.At.After(b.last) {
		b.last = line.At
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

type App struct {
	win      *ui.UI
	sessions map[string]*irc.Session
	pasting  bool
	events   chan event

	cfg        Config
	highlights []string

	lastQuery     string
	lastQueryNet  string
	messageBounds map[string]bound
	lastNetID     string
	lastBuffer    string
}

func NewApp(cfg Config) (app *App, err error) {
	app = &App{
		sessions:      map[string]*irc.Session{},
		events:        make(chan event, eventChanSize),
		cfg:           cfg,
		messageBounds: map[string]bound{},
	}

	if cfg.Highlights != nil {
		app.highlights = make([]string, len(cfg.Highlights))
		for i := range app.highlights {
			app.highlights[i] = strings.ToLower(cfg.Highlights[i])
		}
	}

	mouse := true
	if cfg.Mouse != nil {
		mouse = *cfg.Mouse
	}

	app.win, err = ui.New(ui.Config{
		NickColWidth:   cfg.NickColWidth,
		ChanColWidth:   cfg.ChanColWidth,
		MemberColWidth: cfg.MemberColWidth,
		AutoComplete: func(cursorIdx int, text []rune) []ui.Completion {
			return app.completions(cursorIdx, text)
		},
		Mouse: mouse,
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
	app.win.Close()
	for _, session := range app.sessions {
		session.Close()
	}
}

func (app *App) SwitchToBuffer(netID, buffer string) {
	app.lastNetID = netID
	app.lastBuffer = buffer
}

func (app *App) Run() {
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

// eventLoop retrieves events (in batches) from the event channel and handle
// them, then draws the interface after each batch is handled.
func (app *App) eventLoop() {
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

		app.handleEvents(evs)
		if !app.pasting {
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
		if app.cfg.NoTLS {
			addr += ":6667"
		} else {
			addr += ":6697"
		}
	}

	conn, err = net.Dial("tcp", addr)
	if err != nil {
		return
	}

	if !app.cfg.NoTLS {
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

// handleEvents handles a batch of events.
func (app *App) handleEvents(evs []event) {
	for _, ev := range evs {
		if ev.src == "*" {
			app.handleUIEvent(ev.content)
		} else {
			app.handleIRCEvent(ev.src, ev.content)
		}
	}
}

func (app *App) handleUIEvent(ev interface{}) {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		app.win.Resize()
	case *tcell.EventPaste:
		app.pasting = ev.Start()
	case *tcell.EventMouse:
		app.handleMouseEvent(ev)
	case *tcell.EventKey:
		app.handleKeyEvent(ev)
	case statusLine:
		app.addStatusLine(ev.netID, ev.line)
	default:
		panic("unreachable")
	}
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
	if ev.Buttons()&tcell.ButtonPrimary != 0 && x < app.cfg.ChanColWidth {
		app.win.ClickBuffer(y + app.win.ChannelOffset())
	}
	if ev.Buttons() == 0 {
		if x < app.cfg.ChanColWidth {
			if i := y + app.win.ChannelOffset(); i == app.win.ClickedBuffer() {
				app.win.GoToBufferNo(i)
			}
		}
		app.win.ClickBuffer(-1)
	}
}

func (app *App) handleKeyEvent(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		if app.win.InputClear() {
			app.typing()
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
		if bound, ok := app.messageBounds[buffer]; ok {
			t = bound.first
		}
		s.NewHistoryRequest(buffer).
			WithLimit(100).
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

	// Mutate UI state
	switch ev := ev.(type) {
	case irc.RegisteredEvent:
		for _, channel := range app.cfg.Channels {
			// TODO: group JOIN messages
			// TODO: support autojoining channels with keys
			s.Join(channel, "")
		}
		body := "Connected to the server"
		if s.Nick() != app.cfg.Nick {
			body = fmt.Sprintf("Connected to the server as %s", s.Nick())
		}
		app.win.AddLine(netID, "", ui.NotifyNone, ui.Line{
			At:   msg.TimeOrNow(),
			Head: "--",
			Body: ui.PlainString(body),
		})
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
		var body ui.StyledStringBuilder
		body.WriteString(fmt.Sprintf("%s\u2192%s", ev.FormerNick, ev.User))
		textStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
		arrowStyle := tcell.StyleDefault
		body.AddStyle(0, textStyle)
		body.AddStyle(len(ev.FormerNick), arrowStyle)
		body.AddStyle(body.Len()-len(ev.User), textStyle)
		for _, c := range s.ChannelsSharedWith(ev.User) {
			app.win.AddLine(netID, c, ui.NotifyNone, ui.Line{
				At:        msg.TimeOrNow(),
				Head:      "--",
				HeadColor: tcell.ColorGray,
				Body:      body.StyledString(),
				Mergeable: true,
			})
		}
	case irc.SelfJoinEvent:
		i, added := app.win.AddBuffer(netID, "", ev.Channel)
		bounds, ok := app.messageBounds[ev.Channel]
		if added || !ok {
			s.NewHistoryRequest(ev.Channel).
				WithLimit(200).
				Before(msg.TimeOrNow())
		} else {
			s.NewHistoryRequest(ev.Channel).
				WithLimit(200).
				After(bounds.last)
		}
		if ev.Requested {
			app.win.JumpBufferIndex(i)
		}
		if ev.Topic != "" {
			app.printTopic(netID, ev.Channel)
		}

		// Restore last buffer
		if netID == app.lastNetID && ev.Channel == app.lastBuffer {
			app.win.JumpBufferNetwork(app.lastNetID, app.lastBuffer)
			app.lastNetID = ""
			app.lastBuffer = ""
		}
	case irc.UserJoinEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGreen))
		body.WriteByte('+')
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		body.WriteString(ev.User)
		app.win.AddLine(netID, ev.Channel, ui.NotifyNone, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Mergeable: true,
		})
	case irc.SelfPartEvent:
		app.win.RemoveBuffer(ev.Channel)
		delete(app.messageBounds, ev.Channel)
	case irc.UserPartEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorRed))
		body.WriteByte('-')
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		body.WriteString(ev.User)
		app.win.AddLine(netID, ev.Channel, ui.NotifyNone, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      body.StyledString(),
			Mergeable: true,
		})
	case irc.UserQuitEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorRed))
		body.WriteByte('-')
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		body.WriteString(ev.User)
		for _, c := range ev.Channels {
			app.win.AddLine(netID, c, ui.NotifyNone, ui.Line{
				At:        msg.TimeOrNow(),
				Head:      "--",
				HeadColor: tcell.ColorGray,
				Body:      body.StyledString(),
				Mergeable: true,
			})
		}
	case irc.TopicChangeEvent:
		topic := ui.IRCString(ev.Topic).String()
		body := fmt.Sprintf("Topic changed to: %s", topic)
		app.win.AddLine(netID, ev.Channel, ui.NotifyUnread, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(tcell.ColorGray)),
		})
	case irc.ModeChangeEvent:
		body := fmt.Sprintf("Mode change: %s", ev.Mode)
		app.win.AddLine(netID, ev.Channel, ui.NotifyUnread, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: tcell.ColorGray,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(tcell.ColorGray)),
		})
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
		app.win.AddLine(netID, buffer, notification, line)
		if notification == ui.NotifyHighlight {
			app.notifyHighlight(buffer, ev.User, line.Body.String())
		}
		if !s.IsChannel(msg.Params[0]) && !s.IsMe(ev.User) {
			app.lastQuery = msg.Prefix.Name
			app.lastQueryNet = netID
		}
		bounds := app.messageBounds[ev.Target]
		bounds.Update(&line)
		app.messageBounds[ev.Target] = bounds
	case irc.ReactEvent:
		buffer := ev.Target
		if !ev.TargetIsChannel {
			buffer = ""
		}
		var ssb ui.StyledStringBuilder
		ssb.WriteStyledString(ui.Styled(" ", tcell.StyleDefault))
		ssb.WriteStyledString(ui.Styled(ev.Content, tcell.StyleDefault.Background(tcell.ColorGreen)))
		app.win.AppendLine(s.NetID(), buffer, ev.MessageID, ssb.StyledString())
	case irc.HistoryEvent:
		var linesBefore []ui.Line
		var linesAfter []ui.Line
		bounds, hasBounds := app.messageBounds[ev.Target]
		for _, m := range ev.Messages {
			switch ev := m.(type) {
			case irc.MessageEvent:
				_, line, _ := app.formatMessage(s, ev)
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
			app.messageBounds[ev.Target] = bounds
		}
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

// notifyHighlight executes the "on-highlight" command according to the given
// message context.
func (app *App) notifyHighlight(buffer, nick, content string) {
	if app.cfg.OnHighlight == "" {
		return
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		return
	}
	netID, curBuffer := app.win.CurrentBuffer()
	here := "0"
	if buffer == curBuffer { // TODO also check netID
		here = "1"
	}
	cmd := exec.Command(sh, "-c", app.cfg.OnHighlight)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("BUFFER=%s", buffer),
		fmt.Sprintf("HERE=%s", here),
		fmt.Sprintf("SENDER=%s", nick),
		fmt.Sprintf("MESSAGE=%s", content),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		body := fmt.Sprintf("Failed to invoke on-highlight command: %v. Output: %q", err, string(output))
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
	if s == nil || app.cfg.NoTypings {
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

	if cs != nil {
		cs = append(cs, ui.Completion{
			Text:      text,
			CursorIdx: cursorIdx,
		})
	}

	return cs
}

// formatMessage sets how a given message must be formatted.
//
// It computes three things:
// - which buffer the message must be added to,
// - the UI line,
// - what kind of notification senpai should send.
func (app *App) formatMessage(s *irc.Session, ev irc.MessageEvent) (buffer string, line ui.Line, notification ui.NotifyType) {
	isFromSelf := s.IsMe(ev.User)
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
	} else if !ev.TargetIsChannel {
		buffer = ""
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
	if isFromSelf && isQuery {
		head = "\u2192 " + ev.Target
		headColor = identColor(ev.Target)
	} else if isAction || isNotice {
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
		MessageID: ev.MessageID,
	}
	return
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
