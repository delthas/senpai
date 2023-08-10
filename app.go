package senpai

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"git.sr.ht/~taiite/senpai/irc"
	"git.sr.ht/~taiite/senpai/ui"
	"github.com/gdamore/tcell/v2"
	"golang.org/x/net/context"
	"golang.org/x/net/proxy"
)

const eventChanSize = 1024

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
	sessions map[string]*irc.Session // map of network IDs to their current session
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

	networkLock sync.RWMutex        // locks networks
	networks    map[string]struct{} // set of network IDs we want to connect to; to be locked with networkLock

	lastMessageTime time.Time
	lastCloseTime   time.Time
}

func NewApp(cfg Config) (app *App, err error) {
	app = &App{
		networks: map[string]struct{}{
			"": {}, // add the master network by default
		},
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
		NickColWidth:     cfg.NickColWidth,
		ChanColWidth:     cfg.ChanColWidth,
		ChanColEnabled:   cfg.ChanColEnabled,
		MemberColWidth:   cfg.MemberColWidth,
		MemberColEnabled: cfg.MemberColEnabled,
		TextMaxWidth:     cfg.TextMaxWidth,
		AutoComplete: func(cursorIdx int, text []rune) []ui.Completion {
			return app.completions(cursorIdx, text)
		},
		Mouse: mouse,
		MergeLine: func(former *ui.Line, addition ui.Line) {
			app.mergeLine(former, addition)
		},
		Colors: cfg.Colors,
	})
	if err != nil {
		return
	}
	app.win.SetPrompt(ui.Styled(">",
		tcell.
			StyleDefault.
			Foreground(app.cfg.Colors.Prompt)),
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

	for !app.win.ShouldExit() {
		ev := <-app.events
		if !app.handleEvent(ev) {
			return
		}
		deadline := time.NewTimer(200 * time.Millisecond)
	outer:
		for {
			select {
			case <-deadline.C:
				break outer
			case ev := <-app.events:
				if !app.handleEvent(ev) {
					return
				}
			default:
				if !deadline.Stop() {
					<-deadline.C
				}
				break outer
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
			if netID != "" && buffer != "" {
				app.win.SetTitle(fmt.Sprintf("%s — senpai", buffer))
			} else {
				app.win.SetTitle(fmt.Sprintf("senpai"))
			}
		}
	}
	go func() {
		// drain events until we close
		for range app.events {
		}
	}()
}
func (app *App) handleEvent(ev event) bool {
	if ev.src == "*" {
		if ev.content == nil {
			return false
		}
		if !app.handleUIEvent(ev.content) {
			return false
		}
	} else {
		app.handleIRCEvent(ev.src, ev.content)
	}
	return true
}

func (app *App) wantsNetwork(netID string) bool {
	if app.win.ShouldExit() {
		return false
	}
	app.networkLock.RLock()
	_, ok := app.networks[netID]
	app.networkLock.RUnlock()
	return ok
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
	const throttleInterval = 6 * time.Second
	const throttleMax = 1 * time.Minute
	var delay time.Duration = 0
	for app.wantsNetwork(netID) {
		time.Sleep(delay)
		if delay < throttleMax {
			delay += throttleInterval
		}
		conn := app.connect(netID)
		if conn == nil {
			continue
		}
		delay = throttleInterval

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
	}
}

func (app *App) connect(netID string) net.Conn {
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
	return nil
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

	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)

	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	conn, err = proxy.FromEnvironmentUsing(dialer).(proxy.ContextDialer).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect: %v", err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(15 * time.Second)
	}

	if app.cfg.TLS {
		host, _, _ := net.SplitHostPort(addr) // should succeed since net.Dial did.
		conn = tls.Client(conn, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: app.cfg.TLSSkipVerify,
			NextProtos:         []string{"irc"},
		})
		err = conn.(*tls.Conn).HandshakeContext(ctx)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("tls handshake: %v", err)
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
	w, h := app.win.Size()
	if ev.Buttons()&tcell.WheelUp != 0 {
		if x < app.win.ChannelWidth() || (app.win.ChannelWidth() == 0 && y == h-1) {
			app.win.ScrollChannelUpBy(4)
		} else if x > w-app.win.MemberWidth() {
			app.win.ScrollMemberUpBy(4)
		} else {
			app.win.ScrollUpBy(4)
			app.requestHistory()
		}
	}
	if ev.Buttons()&tcell.WheelDown != 0 {
		if x < app.win.ChannelWidth() || (app.win.ChannelWidth() == 0 && y == h-1) {
			app.win.ScrollChannelDownBy(4)
		} else if x > w-app.win.MemberWidth() {
			app.win.ScrollMemberDownBy(4)
		} else {
			app.win.ScrollDownBy(4)
		}
	}
	if ev.Buttons()&tcell.ButtonPrimary != 0 {
		if x < app.win.ChannelWidth() {
			app.win.ClickBuffer(y + app.win.ChannelOffset())
		} else if app.win.ChannelWidth() == 0 && y == h-1 {
			app.win.ClickBuffer(app.win.HorizontalBufferOffset(x))
		} else if x > w-app.win.MemberWidth() {
			app.win.ClickMember(y + app.win.MemberOffset())
		}
	}
	if ev.Buttons() == 0 {
		if x < app.win.ChannelWidth() {
			if i := y + app.win.ChannelOffset(); i == app.win.ClickedBuffer() {
				app.win.GoToBufferNo(i)
			}
		} else if app.win.ChannelWidth() == 0 && y == h-1 {
			if i := app.win.HorizontalBufferOffset(x); i >= 0 && i == app.win.ClickedBuffer() {
				app.win.GoToBufferNo(i)
			}
		} else if x > w-app.win.MemberWidth() {
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
							s.NewHistoryRequest(buffer).WithLimit(500).Latest()
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
		app.win.HorizontalBufferScrollTo()
	case tcell.KeyCtrlP:
		app.win.PreviousBuffer()
		app.win.HorizontalBufferScrollTo()
	case tcell.KeyRight:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.NextBuffer()
			app.win.HorizontalBufferScrollTo()
		} else if ev.Modifiers() == tcell.ModShift {
			app.win.NextUnreadBuffer()
			app.win.HorizontalBufferScrollTo()
		} else if ev.Modifiers() == tcell.ModCtrl {
			app.win.InputRightWord()
		} else {
			app.win.InputRight()
		}
	case tcell.KeyLeft:
		if ev.Modifiers() == tcell.ModAlt {
			app.win.PreviousBuffer()
			app.win.HorizontalBufferScrollTo()
		} else if ev.Modifiers() == tcell.ModShift {
			app.win.PreviousUnreadBuffer()
			app.win.HorizontalBufferScrollTo()
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
		var ok bool
		if ev.Modifiers() == tcell.ModAlt {
			ok = app.win.InputDeleteWord()
		} else {
			ok = app.win.InputBackspace()
		}
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
	case tcell.KeyEscape:
		app.win.CloseOverlay()
	case tcell.KeyF7:
		app.win.ToggleChannelList()
	case tcell.KeyF8:
		app.win.ToggleMemberList()
	case tcell.KeyCR, tcell.KeyLF:
		netID, buffer := app.win.CurrentBuffer()
		input := app.win.InputEnter()
		err := app.handleInput(buffer, input)
		if err != nil {
			app.win.AddLine(netID, buffer, ui.Line{
				At:        time.Now(),
				Head:      "!!",
				HeadColor: tcell.ColorRed,
				Notify:    ui.NotifyUnread,
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
	if app.win.HasOverlay() {
		return
	}
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return
	}
	if app.win.IsAtTop() && buffer != "" {
		if bound, ok := app.messageBounds[boundKey{netID, buffer}]; ok {
			s.NewHistoryRequest(buffer).
				WithLimit(200).
				Before(bound.first)
		} else {
			s.NewHistoryRequest(buffer).
				WithLimit(200).
				Latest()
		}
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
		if !app.wantsNetwork(netID) {
			delete(app.sessions, netID)
			delete(app.monitor, netID)
			s.Close()
			return
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
		app.win.AddLine(netID, "", ui.Line{
			Head:      "!!",
			HeadColor: tcell.ColorRed,
			Notify:    ui.NotifyUnread,
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
		app.addStatusLine(netID, ui.Line{
			At:   msg.TimeOrNow(),
			Head: "--",
			Body: ui.PlainString(body),
		})
		for target := range app.monitor[s.NetID()] {
			// TODO: batch MONITOR +
			s.MonitorAdd(target)
		}
	case irc.SelfNickEvent:
		if !app.cfg.StatusEnabled {
			break
		}
		var body ui.StyledStringBuilder
		body.WriteString(fmt.Sprintf("%s\u2192%s", ev.FormerNick, s.Nick()))
		textStyle := tcell.StyleDefault.Foreground(app.cfg.Colors.Status)
		arrowStyle := tcell.StyleDefault
		body.AddStyle(0, textStyle)
		body.AddStyle(len(ev.FormerNick), arrowStyle)
		body.AddStyle(body.Len()-len(s.Nick()), textStyle)
		app.addStatusLine(netID, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body:      body.StyledString(),
			Highlight: true,
			Readable:  true,
		})
	case irc.UserNickEvent:
		if !app.cfg.StatusEnabled {
			break
		}
		line := app.formatEvent(ev)
		for _, c := range s.ChannelsSharedWith(ev.User) {
			app.win.AddLine(netID, c, line)
		}
	case irc.SelfJoinEvent:
		i, added := app.win.AddBuffer(netID, "", ev.Channel)
		if !ev.Read.IsZero() {
			app.win.SetRead(netID, ev.Channel, ev.Read)
		}
		bounds, ok := app.messageBounds[boundKey{netID, ev.Channel}]
		if added || !ok {
			if t, ok := msg.Time(); ok {
				s.NewHistoryRequest(ev.Channel).
					WithLimit(500).
					Before(t)
			} else {
				s.NewHistoryRequest(ev.Channel).
					WithLimit(500).
					Latest()
			}
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
			app.win.HorizontalBufferScrollTo()
			app.lastNetID = ""
			app.lastBuffer = ""
		}
	case irc.UserJoinEvent:
		if !app.cfg.StatusEnabled {
			break
		}
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, line)
	case irc.SelfPartEvent:
		app.win.RemoveBuffer(netID, ev.Channel)
		delete(app.messageBounds, boundKey{netID, ev.Channel})
	case irc.UserPartEvent:
		if !app.cfg.StatusEnabled {
			break
		}
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, line)
	case irc.UserQuitEvent:
		if !app.cfg.StatusEnabled {
			break
		}
		line := app.formatEvent(ev)
		for _, c := range ev.Channels {
			app.win.AddLine(netID, c, line)
		}
	case irc.TopicChangeEvent:
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, line)
		topic := ui.IRCString(ev.Topic).String()
		app.win.SetTopic(netID, ev.Channel, topic)
	case irc.ModeChangeEvent:
		if !app.cfg.StatusEnabled {
			break
		}
		line := app.formatEvent(ev)
		app.win.AddLine(netID, ev.Channel, line)
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
		app.win.AddLine(netID, buffer, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Notify:    notify,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(app.cfg.Colors.Status)),
			Highlight: notify == ui.NotifyHighlight,
			Readable:  true,
		})
	case irc.MessageEvent:
		buffer, line := app.formatMessage(s, ev)
		if line.IsZero() {
			break
		}
		if buffer != "" && !s.IsChannel(buffer) {
			if _, added := app.win.AddBuffer(netID, "", buffer); added {
				app.monitor[netID][buffer] = struct{}{}
				s.MonitorAdd(buffer)
				s.ReadGet(buffer)
				if t, ok := msg.Time(); ok {
					s.NewHistoryRequest(buffer).
						WithLimit(500).
						Before(t)
				} else {
					s.NewHistoryRequest(buffer).
						WithLimit(500).
						Latest()
				}
			}
		}
		app.win.AddLine(netID, buffer, line)
		if line.Notify == ui.NotifyHighlight {
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
		type target struct {
			name string
			last time.Time
		}
		// try to fetch the history of the last opened buffer first
		targets := make([]target, 0, len(ev.Targets))
		if app.lastNetID == netID {
			if last, ok := ev.Targets[app.lastBuffer]; ok {
				targets = append(targets, target{app.lastBuffer, last})
				delete(ev.Targets, app.lastBuffer)
			}
		}
		for name, last := range ev.Targets {
			targets = append(targets, target{name, last})
		}
		for _, target := range targets {
			if s.IsChannel(target.name) {
				continue
			}
			s.MonitorAdd(target.name)
			s.ReadGet(target.name)
			app.win.AddBuffer(netID, "", target.name)
			// CHATHISTORY BEFORE excludes its bound, so add 1ms
			// (precision of the time tag) to include that last message.
			target.last = target.last.Add(1 * time.Millisecond)
			s.NewHistoryRequest(target.name).
				WithLimit(500).
				Before(target.last)
		}
	case irc.HistoryEvent:
		var linesBefore []ui.Line
		var linesAfter []ui.Line
		bounds, hasBounds := app.messageBounds[boundKey{netID, ev.Target}]
		boundsNew := bounds
		for _, m := range ev.Messages {
			var line ui.Line
			switch ev := m.(type) {
			case irc.MessageEvent:
				_, line = app.formatMessage(s, ev)
			default:
				line = app.formatEvent(ev)
			}
			if line.IsZero() {
				continue
			}
			boundsNew.Update(&line)
			if _, ok := m.(irc.MessageEvent); !ok && !app.cfg.StatusEnabled {
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
		if !boundsNew.IsZero() {
			app.messageBounds[boundKey{netID, ev.Target}] = boundsNew
		}
	case irc.SearchEvent:
		app.win.OpenOverlay()
		lines := make([]ui.Line, 0, len(ev.Messages))
		for _, m := range ev.Messages {
			_, line := app.formatMessage(s, m)
			if line.IsZero() {
				continue
			}
			lines = append(lines, line)
		}
		app.win.AddLines("", ui.Overlay, lines, nil)
	case irc.ReadEvent:
		app.win.SetRead(netID, ev.Target, ev.Timestamp)
	case irc.BouncerNetworkEvent:
		if !ev.Delete {
			_, added := app.win.AddBuffer(ev.ID, ev.Name, "")
			if added {
				app.networkLock.Lock()
				app.networks[ev.ID] = struct{}{}
				app.networkLock.Unlock()
				go app.ircLoop(ev.ID)
			}
		} else {
			app.networkLock.Lock()
			delete(app.networks, ev.ID)
			app.networkLock.Unlock()
			// if a session was already opened, close it now.
			// otherwise, we'll close it when it sends a new session event.
			if s, ok := app.sessions[ev.ID]; ok {
				s.Close()
				delete(app.sessions, ev.ID)
				delete(app.monitor, ev.ID)
			}
			app.win.RemoveNetworkBuffers(ev.ID)
		}
	case irc.ErrorEvent:
		if isBlackListed(msg.Command) {
			break
		}
		if ev.Code == "372" {
			app.win.AddLine(netID, "", ui.Line{
				At:   msg.TimeOrNow(),
				Head: "MOTD --",
				Body: ui.PlainString(ev.Message),
			})
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
	case "002", "003", "004", "375", "376", "422":
		// useless connection messages
		return true
	}
	return false
}

func isWordBoundary(r rune) bool {
	switch r {
	case '-', '_', '|': // inspired from weechat.look.highlight_regex
		return false
	default:
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}
}

func isHighlight(text, nick string) bool {
	for {
		i := strings.Index(text, nick)
		if i < 0 {
			return false
		}

		left, _ := utf8.DecodeLastRuneInString(text[:i])
		right, _ := utf8.DecodeRuneInString(text[i+len(nick):])
		if isWordBoundary(left) && isWordBoundary(right) {
			return true
		}

		text = text[i+len(nick):]
	}
}

// isHighlight reports whether the given message content is a highlight.
func (app *App) isHighlight(s *irc.Session, content string) bool {
	contentCf := s.Casemap(content)
	if app.highlights == nil {
		return isHighlight(contentCf, s.NickCf())
	}
	for _, h := range app.highlights {
		if isHighlight(contentCf, s.Casemap(h)) {
			return true
		}
	}
	return false
}

// notifyHighlight executes the script at "on-highlight-path" according to the given
// message context.
func (app *App) notifyHighlight(buffer, nick, content string) {
	if app.cfg.OnHighlightBeep {
		app.win.Beep()
	}
	if buffer != nick {
		app.win.Notify(fmt.Sprintf("%s — %s", buffer, nick), content)
	} else {
		app.win.Notify(nick, content)
	}

	if app.cfg.Transient {
		return
	}

	path := app.cfg.OnHighlightPath
	if path == "" {
		defaultHighlightPath, err := DefaultHighlightPath()
		if err != nil {
			return
		}
		path = defaultHighlightPath
	}

	netID, curBuffer := app.win.CurrentBuffer()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		// only error out if the user specified a highlight path
		// if default path unreachable, simple bail
		if app.cfg.OnHighlightPath != "" {
			body := fmt.Sprintf("Unable to find on-highlight command at path: %q", path)
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
	cmd := exec.Command(path)
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
		textStyle := tcell.StyleDefault.Foreground(app.cfg.Colors.Status)
		arrowStyle := tcell.StyleDefault
		body.AddStyle(0, textStyle)
		body.AddStyle(len(ev.FormerNick), arrowStyle)
		body.AddStyle(body.Len()-len(ev.User), textStyle)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
			Readable:  true,
		}
	case irc.UserJoinEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGreen))
		body.WriteByte('+')
		body.SetStyle(tcell.StyleDefault.Foreground(app.cfg.Colors.Status))
		body.WriteString(ev.User)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
			Readable:  true,
		}
	case irc.UserPartEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorRed))
		body.WriteByte('-')
		body.SetStyle(tcell.StyleDefault.Foreground(app.cfg.Colors.Status))
		body.WriteString(ev.User)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
			Readable:  true,
		}
	case irc.UserQuitEvent:
		var body ui.StyledStringBuilder
		body.Grow(len(ev.User) + 1)
		body.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorRed))
		body.WriteByte('-')
		body.SetStyle(tcell.StyleDefault.Foreground(app.cfg.Colors.Status))
		body.WriteString(ev.User)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body:      body.StyledString(),
			Mergeable: true,
			Data:      []irc.Event{ev},
			Readable:  true,
		}
	case irc.TopicChangeEvent:
		topic := ui.IRCString(ev.Topic).String()
		body := fmt.Sprintf("Topic changed to: %s", topic)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Notify:    ui.NotifyUnread,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(app.cfg.Colors.Status)),
			Readable:  true,
		}
	case irc.ModeChangeEvent:
		body := fmt.Sprintf("[%s]", ev.Mode)
		// simple mode event: <+/-><mode> <nick>
		mergeable := len(strings.Split(ev.Mode, " ")) == 2
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body:      ui.Styled(body, tcell.StyleDefault.Foreground(app.cfg.Colors.Status)),
			Mergeable: mergeable,
			Data:      []irc.Event{ev},
			Readable:  true,
		}
	default:
		return ui.Line{}
	}
}

// formatMessage sets how a given message must be formatted.
//
// It computes three things:
// - which buffer the message must be added to,
// - the UI line.
func (app *App) formatMessage(s *irc.Session, ev irc.MessageEvent) (buffer string, line ui.Line) {
	isFromSelf := s.IsMe(ev.User)
	isToSelf := s.IsMe(ev.Target)
	isHighlight := ev.TargetIsChannel && app.isHighlight(s, ev.Content)
	isQuery := !ev.TargetIsChannel && ev.Command == "PRIVMSG"
	isNotice := ev.Command == "NOTICE"

	content := strings.TrimSuffix(ev.Content, "\x01")
	content = strings.TrimRightFunc(content, unicode.IsSpace)

	isAction := false
	if strings.HasPrefix(ev.Content, "\x01") {
		parts := strings.SplitN(ev.Content[1:], " ", 2)
		if len(parts) < 2 {
			return
		}
		switch parts[0] {
		case "ACTION":
			isAction = true
		default:
			return
		}
		content = parts[1]
	}

	if !ev.TargetIsChannel && isNotice {
		curNetID, curBuffer := app.win.CurrentBuffer()
		if curNetID == s.NetID() {
			buffer = curBuffer
		}
	} else if isToSelf {
		buffer = ev.User
	} else {
		buffer = ev.Target
	}

	var notification ui.NotifyType
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
		headColor = ui.IdentColor(app.cfg.Colors.Nicks, head)
	}

	var body ui.StyledStringBuilder
	if isNotice {
		color := ui.IdentColor(app.cfg.Colors.Nicks, ev.User)
		body.SetStyle(tcell.StyleDefault.Foreground(color))
		body.WriteString(ev.User)
		body.SetStyle(tcell.StyleDefault)
		body.WriteString(": ")
		body.WriteStyledString(ui.IRCString(content))
	} else if isAction {
		color := ui.IdentColor(app.cfg.Colors.Nicks, ev.User)
		body.SetStyle(tcell.StyleDefault.Foreground(color))
		body.WriteString(ev.User)
		body.SetStyle(tcell.StyleDefault)
		body.WriteString(" ")
		body.WriteStyledString(ui.IRCString(content))
	} else {
		body.WriteStyledString(ui.IRCString(content))
	}

	line = ui.Line{
		At:        ev.Time,
		Head:      head,
		HeadColor: headColor,
		Notify:    notification,
		Body:      body.StyledString(),
		Highlight: hlLine,
		Readable:  true,
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
				Foreground(app.cfg.Colors.Prompt),
		)
	} else if s == nil {
		prompt = ui.Styled("<offline>",
			tcell.
				StyleDefault.
				Foreground(tcell.ColorRed),
		)
	} else {
		prompt = ui.IdentString(app.cfg.Colors.Nicks, s.Nick())
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
	app.win.AddLine(netID, buffer, ui.Line{
		At:        time.Now(),
		Head:      "--",
		HeadColor: app.cfg.Colors.Status,
		Body:      ui.Styled(body, tcell.StyleDefault.Foreground(app.cfg.Colors.Status)),
	})
	return true
}
