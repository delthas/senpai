package senpai

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"git.sr.ht/~rockorager/vaxis"
	"golang.org/x/net/context"
	"golang.org/x/net/proxy"

	"git.sr.ht/~delthas/senpai/events"
	"git.sr.ht/~delthas/senpai/irc"
	"git.sr.ht/~delthas/senpai/ui"
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

	complete bool
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

type pendingCompletion struct {
	id       int
	f        completionAsync
	deadline time.Time
}

type App struct {
	win              *ui.UI
	sessions         map[string]*irc.Session // map of network IDs to their current session
	pasting          bool
	pastingInputOnly bool // true is pasting started when the editor input was empty

	// events MUST NOT be posted to directly; instead, use App.postEvent.
	events chan event

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

	pendingCompletions    map[string][]pendingCompletion
	pendingCompletionsOff int

	lastMessageTime time.Time
	lastCloseTime   time.Time

	lastConfirm string

	imageLoading bool
	imageOverlay bool

	uploadingProgress *float64

	shownBouncerNotice bool

	closing atomic.Bool
}

func NewApp(cfg Config) (app *App, err error) {
	if cfg.Addr == "" {
		return nil, errors.New("address is required")
	}
	if cfg.Nick == "" {
		return nil, errors.New("nick is required")
	}
	if cfg.User == "" {
		cfg.User = cfg.Nick
	}
	if cfg.Real == "" {
		cfg.Real = cfg.Nick
	}

	app = &App{
		networks: map[string]struct{}{
			"": {}, // add the master network by default
		},
		pendingCompletions: make(map[string][]pendingCompletion),
		sessions:           map[string]*irc.Session{},
		events:             make(chan event, eventChanSize),
		cfg:                cfg,
		messageBounds:      map[boundKey]bound{},
		monitor:            make(map[string]map[string]struct{}),
	}

	if cfg.Highlights != nil {
		app.highlights = make([]string, len(cfg.Highlights))
		for i := range app.highlights {
			app.highlights[i] = strings.ToLower(cfg.Highlights[i])
		}
	}

	mouse := cfg.Mouse

	app.win, app.cfg.Colors, err = ui.New(ui.Config{
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
		Colors:            cfg.Colors,
		LocalIntegrations: cfg.LocalIntegrations,
		WithTTY:           cfg.WithTTY,
		WithConsole:       cfg.WithConsole,
	})
	if err != nil {
		return
	}

	ui.NotifyStart(func(ev *ui.NotifyEvent) {
		app.postEvent(event{
			src:     "*",
			content: ev,
		})
	})
	app.win.SetPrompt(ui.Styled(">", vaxis.Style{
		Foreground: app.cfg.Colors.Prompt,
	}),
	)

	app.initWindow()

	return
}

func (app *App) Close() {
	app.win.Exit()       // tell all instances of app.ircLoop to stop when possible
	app.postEvent(event{ // tell app.eventLoop to stop
		src:     "*",
		content: nil,
	})
	for _, session := range app.sessions {
		session.Close()
	}
	ui.NotifyStop()
	app.closing.Store(true)
	go func() {
		// drain remaining events
		for {
			select {
			case <-app.events:
			default:
				return
			}
		}
	}()
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
			if app.win.Focused() {
				if netID, buffer, timestamp := app.win.UpdateRead(); buffer != "" {
					s := app.sessions[netID]
					if s != nil {
						s.ReadSet(buffer, timestamp)
					}
				}
			}
			app.maybeRequestHistory()
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
			var title strings.Builder
			if higlights := app.win.Highlights(); higlights > 0 {
				fmt.Fprintf(&title, "(%d) ", higlights)
			}
			if netID != "" && buffer != "" {
				fmt.Fprintf(&title, "%s - ", buffer)
			}
			title.WriteString("senpai")
			app.win.SetTitle(title.String())
		}
	}
}

func (app *App) postEvent(ev event) {
	if app.closing.Load() {
		return
	}
	app.events <- ev
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
		if !app.wantsNetwork(netID) {
			break
		}
		if delay < throttleMax {
			delay += throttleInterval
		}
		conn := app.connect(netID)
		if conn == nil {
			continue
		}
		if !app.wantsNetwork(netID) {
			conn.Close()
			break
		}
		delay = throttleInterval

		in, out := irc.ChanInOut(conn)
		if app.cfg.Debug {
			out = app.debugOutputMessages(netID, out)
		}
		session := irc.NewSession(out, params)
		app.postEvent(event{
			src:     netID,
			content: session,
		})
		go func() {
			for stop := range session.TypingStops() {
				app.postEvent(event{
					src:     netID,
					content: stop,
				})
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
			app.postEvent(event{
				src:     netID,
				content: msg,
			})
		}
		app.postEvent(event{
			src:     netID,
			content: nil,
		})
		app.queueStatusLine(netID, ui.Line{
			Head:      "!!",
			HeadColor: ui.ColorRed,
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
		HeadColor: ui.ColorRed,
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
		// of IPv6 address). -> missing port
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
			const placeholder = "<removed>"
			d := msg
			if msg.Command == "PASS" && len(d.Params) >= 1 {
				d.Params = append([]string{placeholder}, d.Params[1:]...)
			} else if msg.Command == "OPER" && len(d.Params) >= 2 {
				d.Params = append([]string{d.Params[0], placeholder}, d.Params[2:]...)
			} else if msg.Command == "AUTHENTICATE" && len(d.Params) >= 1 {
				switch d.Params[0] {
				case "*", "PLAIN":
				default:
					d.Params = append([]string{placeholder}, d.Params[1:]...)
				}
			}
			app.queueStatusLine(netID, ui.Line{
				At:   time.Now(),
				Head: "OUT --",
				Body: ui.PlainString(d.String()),
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
		app.postEvent(event{
			src:     "*",
			content: ev,
		})
	}
}

func (app *App) handleUIEvent(ev interface{}) bool {
	// TODO: when a no-modifier no-button mouse motion event is sent, just set the mouse cursor and avoid redrawing
	// TODO: eat QuitEvent here?
	switch ev := ev.(type) {
	case vaxis.Resize:
		app.win.Resize()
	case vaxis.PasteStartEvent:
		app.pasting = true
		app.pastingInputOnly = len(app.win.InputContent()) == 0
	case vaxis.PasteEndEvent:
		app.pasting = false
		if app.pastingInputOnly {
			app.pastingInputOnly = false

			path := string(app.win.InputContent())
			if _, err := os.Stat(path); err == nil {
				app.win.InputSet(fmt.Sprintf("/upload %v", path))
			}
		}
	case vaxis.Mouse:
		app.handleMouseEvent(ev)
	case vaxis.Key:
		app.handleKeyEvent(ev)
	case vaxis.FocusIn:
		app.win.SetFocused(true)
	case vaxis.FocusOut:
		app.win.SetFocused(false)
	case vaxis.ColorThemeUpdate:
		app.win.SetColorTheme(ev.Mode)
	case *ui.NotifyEvent:
		app.win.JumpBufferNetwork(ev.NetID, ev.Buffer)
	case statusLine:
		app.addStatusLine(ev.netID, ev.line)
	case *events.EventClickNick:
		app.handleNickEvent(ev)
	case *events.EventClickLink:
		app.handleLinkEvent(ev)
	case *events.EventClickChannel:
		app.handleChannelEvent(ev)
	case *events.EventImageLoaded:
		app.win.ShowImage(ev.Image)
		if ev.Image == nil {
			app.imageLoading = false
		}
	case *events.EventFileUpload:
		if ev.Location != "" {
			app.uploadingProgress = nil
			if len(app.win.InputContent()) == 0 {
				app.win.InputSet(ev.Location)
			} else {
				netID, buffer := app.win.CurrentBuffer()
				app.win.AddLine(netID, buffer, ui.Line{
					At:   time.Now(),
					Head: "--",
					Body: ui.PlainString(fmt.Sprintf("File uploaded at: %v", ev.Location)),
				})
			}
		} else if ev.Error != "" {
			app.uploadingProgress = nil
			netID, buffer := app.win.CurrentBuffer()
			app.win.AddLine(netID, buffer, ui.Line{
				At:        time.Now(),
				Head:      "!!",
				HeadColor: ui.ColorRed,
				Body:      ui.PlainString(fmt.Sprintf("File upload failed: %v", ev.Error)),
			})
		} else {
			app.uploadingProgress = &ev.Progress
		}
	default:
		// TODO: missing event types
	}
	return true
}

func (app *App) handleMouseEvent(ev vaxis.Mouse) {
	const memberItems = 3
	x, y := ev.Col, ev.Row
	w, h := app.win.Size()

	if app.imageOverlay && ev.Button == vaxis.MouseLeftButton {
		if ev.EventType == vaxis.EventPress {
			app.win.ShowImage(nil)
			app.imageOverlay = false
		}
		return
	}

	if ev.Button == vaxis.MouseLeftButton && (ev.EventType == vaxis.EventRelease || ev.EventType == vaxis.EventMotion) {
		if app.win.ChannelColClicked() {
			app.win.ResizeChannelCol(x + 1)
		} else if app.win.MemberColClicked() {
			app.win.ResizeMemberCol(w - x)
		}
	}

	if ev.EventType == vaxis.EventPress {
		if ev.Button == vaxis.MouseWheelUp {
			if x < app.win.ChannelWidth() || (app.win.ChannelWidth() == 0 && y == h-1) {
				app.win.ScrollChannelUpBy(4)
			} else if x > w-app.win.MemberWidth() && y < h-memberItems*2 {
				app.win.ScrollMemberUpBy(4)
			} else if y == 0 {
				app.win.ScrollTopicLeftBy(12)
			} else {
				app.win.ScrollUpBy(4)
			}
		}
		if ev.Button == vaxis.MouseWheelDown {
			if x < app.win.ChannelWidth() || (app.win.ChannelWidth() == 0 && y == h-1) {
				app.win.ScrollChannelDownBy(4)
			} else if x > w-app.win.MemberWidth() && y < h-memberItems*2 {
				app.win.ScrollMemberDownBy(4)
			} else if y == 0 {
				app.win.ScrollTopicRightBy(12)
			} else {
				app.win.ScrollDownBy(4)
			}
		}
		if ev.Button == vaxis.MouseLeftButton {
			if x == app.win.ChannelWidth()-1 {
				app.win.ClickChannelCol(true)
			} else if x < app.win.ChannelWidth() {
				app.win.ClickBuffer(app.win.VerticalBufferOffset(y))
			} else if app.win.ChannelWidth() == 0 && y == h-1 {
				app.win.ClickBuffer(app.win.HorizontalBufferOffset(x))
			} else if x == w-app.win.MemberWidth() {
				app.win.ClickMemberCol(true)
			} else if x > w-app.win.MemberWidth() && y >= 2 && y < h-memberItems*2 {
				app.win.ClickMember(y - 2 + app.win.MemberOffset())
			} else if x > w-app.win.MemberWidth() && y >= h-memberItems*2 && (y-(h-memberItems*2))%2 == 1 {
				netID, target := app.win.CurrentBuffer()
				var failed bool
				switch (y - (h - memberItems*2)) / 2 {
				case 0:
					muted := app.win.GetMuted(netID, target)
					if s := app.sessions[netID]; s != nil && target != "" {
						if !s.MutedSet(target, !muted) {
							failed = true
						}
					}
				case 1:
					pinned := app.win.GetPinned(netID, target)
					if s := app.sessions[netID]; s != nil && target != "" {
						if !s.PinnedSet(target, !pinned) {
							failed = true
						}
					}
				case 2:
					s := app.sessions[netID]
					if s != nil && s.IsChannel(target) {
						s.Part(target, "")
					} else {
						app.win.RemoveBuffer(netID, target)
					}
				}
				if failed {
					netID, buffer := app.win.CurrentBuffer()
					app.win.AddLine(netID, buffer, ui.Line{
						At:        time.Now(),
						Head:      "!!",
						HeadColor: ui.ColorRed,
						Body:      ui.PlainString(errNotSupported.Error()),
					})
				}
			} else {
				app.win.Click(x, y, ev)
			}
		}
		if ev.Button == vaxis.MouseMiddleButton {
			i := -1
			if x < app.win.ChannelWidth() {
				i = app.win.VerticalBufferOffset(y)
			} else if app.win.ChannelWidth() == 0 && y == h-1 {
				i = app.win.HorizontalBufferOffset(x)
			}
			netID, channel, ok := app.win.Buffer(i)
			if ok && channel != "" {
				s := app.sessions[netID]
				if s != nil && s.IsChannel(channel) {
					s.Part(channel, "")
				} else {
					app.win.RemoveBuffer(netID, channel)
				}
			}
		}
		if ev.Button == vaxis.MouseRightButton {
			app.win.Click(x, y, ev)
		}
	}
	if ev.EventType == vaxis.EventRelease {
		if x < app.win.ChannelWidth()-1 {
			if i := app.win.VerticalBufferOffset(y); i == app.win.ClickedBuffer() {
				app.win.GoToBufferNo(i)
				app.clearBufferCommand()
			}
		} else if app.win.ChannelWidth() == 0 && y == h-1 {
			if i := app.win.HorizontalBufferOffset(x); i >= 0 && i == app.win.ClickedBuffer() {
				app.win.GoToBufferNo(i)
				app.clearBufferCommand()
			}
		} else if x > w-app.win.MemberWidth() && y < h-memberItems*2 {
			if i := y - 2 + app.win.MemberOffset(); i >= 0 && i == app.win.ClickedMember() {
				netID, target := app.win.CurrentBuffer()
				if target == "" {
					switch y {
					case 2:
						if _, err := getBouncerService(app); err != nil {
							app.win.AddLine(netID, target, ui.Line{
								At:        time.Now(),
								Head:      "--",
								HeadColor: ui.ColorRed,
								Body:      ui.PlainSprintf("Adding networks is not available: %v", err),
							})
						} else {
							app.win.AddLine(netID, target, ui.Line{
								At:   time.Now(),
								Head: "--",
								Body: ui.PlainString("To join a network/server, use /bouncer network create -addr <address> [-name <name>]"),
							})
							app.win.AddLine(netID, target, ui.Line{
								At:   time.Now(),
								Head: "--",
								Body: ui.PlainString("For details, see /bouncer help network create"),
							})
							app.win.InputSet("/bouncer network create -addr ")
						}
					case 4:
						app.win.AddLine(netID, target, ui.Line{
							At:   time.Now(),
							Head: "--",
							Body: ui.PlainString("To join a channel, use /join <#channel> [<password>]"),
						})
						app.win.InputSet("/join ")
					case 6:
						app.win.AddLine(netID, target, ui.Line{
							At:   time.Now(),
							Head: "--",
							Body: ui.PlainString("To message a user, use /query <user> [<message>]"),
						})
						app.win.InputSet("/query ")
					}
				} else if s := app.sessions[netID]; s != nil {
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
		app.win.ClickChannelCol(false)
		app.win.ClickMemberCol(false)
	}
	if x == app.win.ChannelWidth()-1 || x == w-app.win.MemberWidth() {
		app.win.SetMouseShape(vaxis.MouseShapeResizeHorizontal)
	} else if x < app.win.ChannelWidth()-1 || x > w-app.win.MemberWidth() || app.win.HasEvent(x, y) {
		app.win.SetMouseShape(vaxis.MouseShapeClickable)
	} else {
		app.win.SetMouseShape(vaxis.MouseShapeDefault)
	}
}

func (app *App) handleKeyEvent(ev vaxis.Key) {
	switch ev.EventType {
	case vaxis.EventPress, vaxis.EventRepeat, vaxis.EventPaste:
	default:
		return
	}
	if ev.Text != "" {
		for _, r := range ev.Text {
			app.win.InputRune(r)
		}
		app.typing()
		return
	}

	if keyMatches(ev, 'c', vaxis.ModCtrl) {
		if app.win.InputClear() {
			app.typing()
		} else {
			app.win.InputSet("/quit")
		}
	} else if keyMatches(ev, 'f', vaxis.ModCtrl) {
		if len(app.win.InputContent()) == 0 {
			app.win.InputSet("/search ")
		}
	} else if keyMatches(ev, 'k', vaxis.ModCtrl) {
		if len(app.win.InputContent()) == 0 {
			app.win.InputSet("/buffer ")
		}
	} else if keyMatches(ev, 'a', vaxis.ModCtrl) {
		app.win.InputHome()
	} else if keyMatches(ev, 'e', vaxis.ModCtrl) {
		app.win.InputEnd()
	} else if keyMatches(ev, 'l', vaxis.ModCtrl) {
		app.win.Resize()
	} else if keyMatches(ev, 'u', vaxis.ModCtrl) || keyMatches(ev, vaxis.KeyPgUp, 0) {
		app.win.ScrollUp()
	} else if keyMatches(ev, 'd', vaxis.ModCtrl) || keyMatches(ev, vaxis.KeyPgDown, 0) {
		app.win.ScrollDown()
	} else if keyMatches(ev, 'n', vaxis.ModCtrl) {
		app.win.NextBuffer()
		app.win.ScrollToBuffer()
	} else if keyMatches(ev, 'p', vaxis.ModCtrl) {
		app.win.PreviousBuffer()
		app.win.ScrollToBuffer()
	} else if keyMatches(ev, vaxis.KeyRight, vaxis.ModAlt) {
		app.win.NextBuffer()
		app.win.ScrollToBuffer()
	} else if keyMatches(ev, vaxis.KeyRight, vaxis.ModShift) {
		app.win.NextUnreadBuffer()
		app.win.ScrollToBuffer()
	} else if keyMatches(ev, vaxis.KeyRight, vaxis.ModCtrl) {
		app.win.InputRightWord()
	} else if keyMatches(ev, vaxis.KeyRight, 0) {
		app.win.InputRight()
	} else if keyMatches(ev, vaxis.KeyLeft, vaxis.ModAlt) {
		app.win.PreviousBuffer()
		app.win.ScrollToBuffer()
	} else if keyMatches(ev, vaxis.KeyLeft, vaxis.ModShift) {
		app.win.PreviousUnreadBuffer()
		app.win.ScrollToBuffer()
	} else if keyMatches(ev, vaxis.KeyLeft, vaxis.ModCtrl) {
		app.win.InputLeftWord()
	} else if keyMatches(ev, vaxis.KeyLeft, 0) {
		app.win.InputLeft()
	} else if keyMatches(ev, vaxis.KeyUp, vaxis.ModAlt) {
		app.win.PreviousBuffer()
	} else if keyMatches(ev, vaxis.KeyUp, 0) {
		app.win.InputUp()
	} else if keyMatches(ev, vaxis.KeyDown, vaxis.ModAlt) {
		app.win.NextBuffer()
	} else if keyMatches(ev, vaxis.KeyDown, 0) {
		app.win.InputDown()
	} else if keyMatches(ev, vaxis.KeyHome, vaxis.ModAlt) {
		app.win.GoToBufferNo(0)
	} else if keyMatches(ev, vaxis.KeyHome, 0) {
		app.win.InputHome()
	} else if keyMatches(ev, vaxis.KeyEnd, vaxis.ModAlt) {
		maxInt := int(^uint(0) >> 1)
		app.win.GoToBufferNo(maxInt)
	} else if keyMatches(ev, vaxis.KeyEnd, 0) {
		app.win.InputEnd()
	} else if keyMatches(ev, vaxis.KeyBackspace, vaxis.ModAlt) {
		if app.win.InputDeleteWord() {
			app.typing()
		}
	} else if keyMatches(ev, vaxis.KeyBackspace, 0) || keyMatches(ev, vaxis.KeyBackspace, vaxis.ModShift) {
		if app.win.InputBackspace() {
			app.typing()
		}
	} else if keyMatches(ev, vaxis.KeyDelete, 0) {
		if app.win.InputDelete() {
			app.typing()
		}
	} else if keyMatches(ev, 'w', vaxis.ModCtrl) {
		if app.win.InputDeleteWord() {
			app.typing()
		}
	} else if keyMatches(ev, 'r', vaxis.ModCtrl) {
		app.win.InputBackSearch()
	} else if keyMatches(ev, vaxis.KeyTab, 0) {
		if app.win.InputAutoComplete() {
			app.typing()
		}
	} else if keyMatches(ev, vaxis.KeyEsc, 0) {
		app.win.CloseOverlay()
	} else if keyMatches(ev, vaxis.KeyF07, 0) {
		app.win.ToggleChannelList()
	} else if keyMatches(ev, vaxis.KeyF08, 0) {
		app.win.ToggleMemberList()
	} else if keyMatches(ev, '\n', 0) || keyMatches(ev, '\r', 0) || keyMatches(ev, 'j', vaxis.ModCtrl) || keyMatches(ev, vaxis.KeyKeyPadEnter, 0) {
		if ev.EventType == vaxis.EventPaste {
			app.win.InputRune('\n')
		} else if !app.win.InputEnter() {
			netID, buffer := app.win.CurrentBuffer()
			input := string(app.win.InputContent())
			var err error
			for _, part := range strings.Split(input, "\n") {
				if err = app.handleInput(buffer, part); err != nil {
					app.win.AddLine(netID, buffer, ui.Line{
						At:        time.Now(),
						Head:      "!!",
						HeadColor: ui.ColorRed,
						Notify:    ui.NotifyUnread,
						Body:      ui.PlainSprintf("%q: %s", input, err),
					})
					break
				}
			}
			if err == nil {
				app.win.InputFlush()
			}
		}
	} else if keyMatches(ev, 'n', vaxis.ModAlt) {
		app.win.ScrollDownHighlight()
	} else if keyMatches(ev, 'p', vaxis.ModAlt) {
		app.win.ScrollUpHighlight()
	} else if keyMatches(ev, '1', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad1, vaxis.ModAlt) {
		app.win.GoToBufferNo(0)
	} else if keyMatches(ev, '2', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad2, vaxis.ModAlt) {
		app.win.GoToBufferNo(1)
	} else if keyMatches(ev, '3', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad3, vaxis.ModAlt) {
		app.win.GoToBufferNo(2)
	} else if keyMatches(ev, '4', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad4, vaxis.ModAlt) {
		app.win.GoToBufferNo(3)
	} else if keyMatches(ev, '5', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad5, vaxis.ModAlt) {
		app.win.GoToBufferNo(4)
	} else if keyMatches(ev, '6', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad6, vaxis.ModAlt) {
		app.win.GoToBufferNo(5)
	} else if keyMatches(ev, '7', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad7, vaxis.ModAlt) {
		app.win.GoToBufferNo(6)
	} else if keyMatches(ev, '8', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad8, vaxis.ModAlt) {
		app.win.GoToBufferNo(7)
	} else if keyMatches(ev, '9', vaxis.ModAlt) || keyMatches(ev, vaxis.KeyKeyPad9, vaxis.ModAlt) {
		app.win.GoToBufferNo(8)
	}
}

func (app *App) handleNickEvent(ev *events.EventClickNick) {
	s := app.sessions[ev.NetID]
	if s == nil {
		return
	}
	i, added := app.win.AddBuffer(ev.NetID, "", ev.Nick)
	app.win.JumpBufferIndex(i)
	if added {
		s.MonitorAdd(ev.Nick)
		s.ReadGet(ev.Nick)
		s.NewHistoryRequest(ev.Nick).WithLimit(500).Latest()
	}
}

func (app *App) handleChannelEvent(ev *events.EventClickChannel) {
	s := app.sessions[ev.NetID]
	if s == nil {
		return
	}
	if !app.win.JumpBufferNetwork(ev.NetID, ev.Channel) {
		s.Join(ev.Channel, "")
	}
}

var patternOpenGraphImage = regexp.MustCompile(`<meta property="og:image" content="(.*?)"/?>`)
var patternOpenGraphVideo = regexp.MustCompile(`<meta property="og:video"`)

func (app *App) fetchImage(link string) (image.Image, error) {
	if u, err := url.Parse(link); err == nil {
		changed := true
		switch u.Host {
		case "twitter.com", "x.com":
			u.Host = "fixupx.com"
		default:
			changed = false
		}
		if changed {
			link = u.String()
		}
	}

	cHead := http.Client{
		Timeout: 1500 * time.Millisecond,
	}
	res, err := cHead.Head(link)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("unexpected content type: %v", res.Header.Get("Content-Type"))
	}
	var isHTML bool
	switch contentType {
	case "image/gif", "image/jpeg", "image/png": // Actual image, fetch
	case "text/html": // Might have an opengraph image, try fetching
		isHTML = true
	default:
		return nil, fmt.Errorf("unexpected content type: %v", contentType)
	}
	if isHTML {
		req, err := http.NewRequest("GET", link, nil)
		if err != nil {
			return nil, err
		}
		var previewSize int64 = 10 * 1024
		if res.Header.Get("Accept-Ranges") == "bytes" {
			req.Header.Set("Range", fmt.Sprintf("bytes=0-%v", previewSize))
		}
		res, err = cHead.Get(link)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, previewSize))
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("unexpected read error: %v", err)
		}
		if patternOpenGraphVideo.Match(b) {
			// Do not display image (previews) of video objects
			return nil, fmt.Errorf("video embed found")
		}
		m := patternOpenGraphImage.FindSubmatch(b)
		if len(m) < 2 {
			return nil, fmt.Errorf("image embed not found")
		}
		link = html.UnescapeString(string(m[1]))
	}
	cGet := http.Client{
		Timeout: 5 * time.Second,
	}
	res, err = cGet.Get(link)
	if err != nil {
		return nil, err
	}
	img, _, err := ui.DecodeImage(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, err
	}
	return img, nil
}

func (app *App) handleLinkEvent(ev *events.EventClickLink) {
	open := func() {
		if strings.HasPrefix(ev.Link, "-") {
			// Avoid injection of parameters.
			// Sadly xdg-open does not support "--"...
			return
		}
		cmd := exec.Command("xdg-open", ev.Link)
		cmd.Run()
	}

	if ev.Event.Modifiers == vaxis.ModCtrl {
		if ev.Mouse {
			// Only react to Ctrl+Click when mouse links are enabled.

			// Explicit external link open requested with Ctrl+Click:
			// just run xdg-open.
			go open()
		}
		return
	}

	// Regular link open requested:
	// Try fetching as an image and displaying a preview;
	// fall back to xdg-open if mouse links are enabled.

	app.imageLoading = true
	go func() {
		img, err := app.fetchImage(ev.Link)
		if err != nil {
			app.postEvent(event{
				src: "*",
				content: &events.EventImageLoaded{
					Image: nil,
				},
			})
			if ev.Mouse {
				open()
			}
		} else {
			app.postEvent(event{
				src: "*",
				content: &events.EventImageLoaded{
					Image: img,
				},
			})
		}
	}()
}

func (app *App) upload(url string, f *os.File, size int64) (string, error) {
	defer f.Close()
	c := http.Client{
		Timeout: 30 * time.Second,
	}
	r := ReadProgress{
		Reader: f,
		period: 250 * time.Millisecond,
		f: func(n int64) {
			app.postEvent(event{
				src: "*",
				content: &events.EventFileUpload{
					Progress: float64(n) / float64(size),
				},
			})
		},
	}
	req, err := http.NewRequest("POST", url, &r)
	if err != nil {
		return "", fmt.Errorf("creating upload request: %v", err)
	}
	if app.cfg.Password != nil {
		req.SetBasicAuth(app.cfg.User, *app.cfg.Password)
	}
	req.ContentLength = size
	req.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": filepath.Base(f.Name()),
	}))
	res, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading: %v", err)
	}
	if res.StatusCode == http.StatusRequestEntityTooLarge {
		var maxSize int64
		for _, entry := range strings.Split(res.Header.Get("Upload-Limit"), ",") {
			entry = strings.TrimSpace(entry)
			key, value, ok := strings.Cut(entry, "=")
			if !ok || key != "maxsize" {
				continue
			}
			if v, err := strconv.ParseInt(value, 10, 64); err == nil && v > 0 {
				maxSize = v
			}
		}
		if maxSize > 0 {
			return "", fmt.Errorf("uploading: file too large: maximum %v per file (file was %v)", formatSize(maxSize), formatSize(size))
		} else {
			return "", fmt.Errorf("uploading: file too large")
		}
	}
	if res.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("uploading: unexpected status code: %d", res.StatusCode)
	}
	location, err := res.Location()
	if err != nil {
		return "", fmt.Errorf("uploading: reading file URL: %v", err)
	}
	return location.String(), nil
}

func (app *App) handleUpload(url string, f *os.File, size int64) {
	var progress float64 = 0
	app.uploadingProgress = &progress
	go func() {
		location, err := app.upload(url, f, size)
		if err != nil {
			app.postEvent(event{
				src: "*",
				content: &events.EventFileUpload{
					Error: err.Error(),
				},
			})
		} else {
			app.postEvent(event{
				src: "*",
				content: &events.EventFileUpload{
					Location: location,
				},
			})
		}
	}()
}

// maybeRequestHistory is a wrapper around irc.Session.RequestHistory to only request
// history when needed.
func (app *App) maybeRequestHistory() {
	if app.win.HasOverlay() {
		return
	}
	netID, buffer := app.win.CurrentBuffer()
	if app.messageBounds[boundKey{netID, buffer}].complete {
		return
	}
	s := app.sessions[netID]
	if s == nil {
		return
	}
	_, h := app.win.Size()
	if l := app.win.LinesAboveOffset(); l < h*2 && buffer != "" {
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
		panic(fmt.Sprintf("cannot found session %q for message %q", netID, msg.String()))
	}

	// Mutate IRC state
	ev, err := s.HandleMessage(msg)
	if err != nil {
		app.win.AddLine(netID, "", ui.Line{
			Head:      "!!",
			HeadColor: ui.ColorRed,
			Notify:    ui.NotifyUnread,
			Body:      ui.PlainSprintf("Received corrupt message %q: %s", msg.String(), err),
		})
		return
	}
	t := msg.TimeOrNow()
	if t.After(app.lastMessageTime) {
		app.lastMessageTime = t
	}

	if cs, ok := app.pendingCompletions[netID]; ok {
		now := time.Now()
		for i := 0; i < len(cs); i++ {
			c := &cs[i]
			var r []ui.Completion
			eat := false
			if c.deadline.After(now) {
				r = c.f(ev)
				if r == nil {
					continue
				}
				eat = true
			}
			app.win.AsyncCompletions(c.id, r)
			copy(cs[i:], cs[i+1:])
			app.pendingCompletions[netID] = app.pendingCompletions[netID][:len(cs)-1]
			i--
			if eat {
				return
			}
		}
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
		if !app.shownBouncerNotice && !s.IsBouncer() {
			app.shownBouncerNotice = true
			for _, line := range []string{
				"senpai appears to be directly connected to an IRC server, rather than to an \x02IRC bouncer\x02. This is supported, but provides a limited IRC experience.",
				"In order to connect to multiple networks, keep message history, search through your messages, and upload files, use an \x02IRC bouncer\x02 and point senpai to the bouncer.",
				"Most senpai users use senpai with the IRC bouncer software \x02soju\x02.",
				"* You can self-host \x02soju\x02 yourself (it is free and open-source): https://soju.im/",
				"* You can also use a commercial hosted bouncer (uses \x02soju\x02 underneath), endorsed by senpai: \x02https://irctoday.com/\x02",
			} {
				app.addStatusLine(netID, ui.Line{
					At:   msg.TimeOrNow(),
					Head: "Bouncer --",
					Body: ui.IRCString(line),
				})
			}
		}
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
		textStyle := vaxis.Style{
			Foreground: app.cfg.Colors.Status,
		}
		var arrowStyle vaxis.Style
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
			topic := ui.IRCString(ev.Topic).ParseURLs()
			app.win.SetTopic(netID, ev.Channel, topic)
		}
		app.win.SetPinned(netID, ev.Channel, ev.Pinned)
		app.win.SetMuted(netID, ev.Channel, ev.Muted)

		// Restore last buffer
		if netID == app.lastNetID && ev.Channel == app.lastBuffer {
			app.win.JumpBufferNetwork(app.lastNetID, app.lastBuffer)
			app.win.ScrollToBuffer()
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
		topic := ui.IRCString(ev.Topic).ParseURLs()
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
			Body: ui.Styled(body, vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			}),
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
			curNetID, curBuffer := app.win.CurrentBuffer()
			current := app.win.Focused() && curNetID == netID && curBuffer == buffer
			app.notifyHighlight(buffer, ev.User, line.Body.String(), current)
		}
		if !s.IsChannel(msg.Params[0]) && !s.IsMe(ev.User) {
			app.lastQuery = msg.Prefix.Name
			app.lastQueryNet = netID
		}
		bounds := app.messageBounds[boundKey{netID, buffer}]
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
		if len(ev.Messages) < 10 {
			// We're getting a non-full page: mark as complete to avoid indefinitely fetching the history.
			// This should ideally be equal to the CHATHISTORY LIMIT, but it can be non advertised, or
			// a full page could sometimes be less than a limit (because it could be filtered).
			// It is also not zero, because bounds are inclusive, and not one, because we truncate based on
			// the second of the message (because some bouncers have a second-level resolution).
			// Be safe and pick 10 messages: less messages means that this was not a full page and we are done
			// with fetching the backlog.
			b := app.messageBounds[boundKey{netID, ev.Target}]
			b.complete = true
			app.messageBounds[boundKey{netID, ev.Target}] = b
		}
	case irc.SearchEvent:
		app.win.OpenOverlay("Press Escape to close the search results")
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
	case irc.MetadataChangeEvent:
		app.win.SetPinned(netID, ev.Target, ev.Pinned)
		app.win.SetMuted(netID, ev.Target, ev.Muted)
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
	case irc.ListEvent:
		for _, item := range ev {
			text := fmt.Sprintf("There are %4s users on channel %s", item.Count, item.Channel)
			if item.Topic != "" {
				text += " -- " + item.Topic
			}
			app.addStatusLine(netID, ui.Line{
				At:        msg.TimeOrNow(),
				Head:      "List --",
				HeadColor: app.cfg.Colors.Status,
				Body: ui.Styled(text, vaxis.Style{
					Foreground: app.cfg.Colors.Status,
				}),
			})
		}
	case irc.InfoEvent:
		var head string
		if ev.Prefix != "" {
			head = ev.Prefix + " --"
		} else {
			head = "--"
		}
		app.addStatusLine(netID, ui.Line{
			At:        msg.TimeOrNow(),
			Head:      head,
			HeadColor: app.cfg.Colors.Status,
			Body: ui.Styled(ev.Message, vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			}),
		})
		return
	case irc.ErrorEvent:
		var head string
		var body string
		switch ev.Severity {
		case irc.SeverityNote:
			app.addStatusLine(netID, ui.Line{
				At:        msg.TimeOrNow(),
				Head:      fmt.Sprintf("(%s) --", ev.Code),
				HeadColor: app.cfg.Colors.Status,
				Body: ui.Styled(ev.Message, vaxis.Style{
					Foreground: app.cfg.Colors.Status,
				}),
			})
			return
		case irc.SeverityFail:
			head = "--"
			body = fmt.Sprintf("Error (code %s): %s", ev.Code, ev.Message)
		case irc.SeverityWarn:
			head = "--"
			body = fmt.Sprintf("Warning (code %s): %s", ev.Code, ev.Message)
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
func (app *App) notifyHighlight(buffer, nick, content string, current bool) {
	if !current && app.cfg.OnHighlightBeep {
		app.win.Beep()
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

	netID, _ := app.win.CurrentBuffer()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		// only error out if the user specified a highlight path
		// if default path unreachable, simple bail
		if app.cfg.OnHighlightPath != "" {
			body := fmt.Sprintf("Unable to find on-highlight command at path: %q", path)
			app.addStatusLine(netID, ui.Line{
				At:        time.Now(),
				Head:      "!!",
				HeadColor: ui.ColorRed,
				Body:      ui.PlainString(body),
			})
		}
		return
	}
	here := "0"
	if current {
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
			HeadColor: ui.ColorRed,
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
	cs = app.completionsJoin(cs, cursorIdx, text)
	cs = app.completionsUpload(cs, cursorIdx, text)
	cs = app.completionsMsg(cs, cursorIdx, text)
	cs = app.completionsCommands(cs, cursorIdx, text)
	cs = app.completionsEmoji(cs, cursorIdx, text)

	for i := 0; i < len(cs); i++ {
		c := &cs[i]
		if c.Async == nil {
			continue
		}
		c.AsyncID = app.pendingCompletionsOff
		app.pendingCompletionsOff++
		app.pendingCompletions[netID] = append(app.pendingCompletions[netID], pendingCompletion{
			id:       c.AsyncID,
			f:        c.Async.(completionAsync),
			deadline: time.Now().Add(4 * time.Second),
		})
	}

	return cs
}

type mergedEvent struct {
	oldNick        string
	nick           string
	nickCf         string
	firstConnected int // -1: offline; 1: online
	lastConnected  int // -1: offline; 1: online
	modeSet        string
	modeUnset      string
	channelMode    string
}

// formatEvent returns a formatted ui.Line for an irc.Event.
func (app *App) formatEvent(ev irc.Event) ui.Line {
	switch ev := ev.(type) {
	case irc.UserNickEvent:
		var body ui.StyledStringBuilder
		body.WriteString(fmt.Sprintf("%s\u2192%s", ev.FormerNick, ev.User))
		textStyle := vaxis.Style{
			Foreground: app.cfg.Colors.Status,
		}
		var arrowStyle vaxis.Style
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
		body.SetStyle(vaxis.Style{
			Foreground: ui.ColorGreen,
		})
		body.WriteByte('+')
		body.SetStyle(vaxis.Style{
			Foreground: app.cfg.Colors.Status,
		})
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
		body.SetStyle(vaxis.Style{
			Foreground: ui.ColorRed,
		})
		body.WriteByte('-')
		body.SetStyle(vaxis.Style{
			Foreground: app.cfg.Colors.Status,
		})
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
		body.SetStyle(vaxis.Style{
			Foreground: ui.ColorRed,
		})
		body.WriteByte('-')
		body.SetStyle(vaxis.Style{
			Foreground: app.cfg.Colors.Status,
		})
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
		who := ui.IRCString(ev.Who).String()
		body := fmt.Sprintf("Topic changed by %s to: %s", who, topic)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Notify:    ui.NotifyUnread,
			Body: ui.Styled(body, vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			}),
			Readable: true,
		}
	case irc.ModeChangeEvent:
		body := fmt.Sprintf("[%s]", ev.Mode)
		return ui.Line{
			At:        ev.Time,
			Head:      "--",
			HeadColor: app.cfg.Colors.Status,
			Body: ui.Styled(body, vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			}),
			Mergeable: true,
			Data:      []irc.Event{ev},
			Readable:  true,
		}
	case *mergedEvent:
		var body ui.StyledStringBuilder
		if ev.nick != "" && ((ev.firstConnected != 0 && ev.firstConnected == ev.lastConnected) || ev.modeSet != "" || ev.modeUnset != "" || (ev.oldNick != "" && ev.oldNick != ev.nick)) {
			if ev.firstConnected != 0 && ev.firstConnected == ev.lastConnected {
				if ev.firstConnected == -1 {
					body.SetStyle(vaxis.Style{
						Foreground: ui.ColorRed,
					})
					body.WriteByte('-')
				} else {
					body.SetStyle(vaxis.Style{
						Foreground: ui.ColorGreen,
					})
					body.WriteByte('+')
				}
			}
			if ev.modeSet != "" || ev.modeUnset != "" {
				body.SetStyle(vaxis.Style{
					Foreground: app.cfg.Colors.Status,
				})
				body.WriteByte('[')
				if ev.modeSet != "" {
					body.WriteByte('+')
					body.WriteString(ev.modeSet)
				}
				if ev.modeUnset != "" {
					body.WriteByte('-')
					body.WriteString(ev.modeSet)
				}
				body.WriteByte(']')
			}
			if ev.oldNick != "" && ev.oldNick != ev.nick {
				body.SetStyle(vaxis.Style{
					Foreground: app.cfg.Colors.Status,
				})
				body.WriteString(ev.oldNick)
				body.SetStyle(vaxis.Style{})
				body.WriteString("\u2192")
			}
			body.SetStyle(vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			})
			body.WriteString(ev.nick)
		} else if ev.nick == "" && ev.channelMode != "" {
			body.SetStyle(vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			})
			fmt.Fprintf(&body, "[%s]", ev.channelMode)
		} else {
			return ui.Line{}
		}
		return ui.Line{
			// Only the Body is used for merged events
			Body: body.StyledString(),
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

	if !ev.TargetIsChannel && (isNotice || ev.User == s.BouncerService()) {
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
	var headColor vaxis.Color
	if isAction || isNotice {
		head = "*"
	} else {
		headColor = app.win.IdentColor(app.cfg.Colors.Nicks, head, isFromSelf)
	}

	var body ui.StyledStringBuilder
	if isNotice {
		color := app.win.IdentColor(app.cfg.Colors.Nicks, ev.User, isFromSelf)
		body.SetStyle(vaxis.Style{
			Foreground: color,
		})
		body.WriteString(ev.User)
		body.SetStyle(vaxis.Style{})
		body.WriteString(": ")
		body.WriteStyledString(ui.IRCString(content))
	} else if isAction {
		color := app.win.IdentColor(app.cfg.Colors.Nicks, ev.User, isFromSelf)
		body.SetStyle(vaxis.Style{
			Foreground: color,
		})
		body.WriteString(ev.User)
		body.SetStyle(vaxis.Style{})
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
	flows := make([]*mergedEvent, 0, len(events))
	flowNick := func(nick string) *mergedEvent {
		nickCf := strings.ToLower(nick)
		for _, f := range flows {
			if f.nickCf == nickCf {
				return f
			}
		}
		return nil
	}

	for _, ev := range events {
		switch ev := ev.(type) {
		case irc.UserNickEvent:
			if f := flowNick(ev.User); f != nil {
				// Drop any existing flow on the target user, effectively replacing it
				// with this new nick. Not very "accurate", but handles disconnects/reconnects/alternate nicks
				// quietly enough.
				for i, ff := range flows {
					if f == ff {
						flows = append(flows[:i], flows[i+1:]...)
						break
					}
				}
			}
			f := flowNick(ev.FormerNick)
			if f != nil {
				f.nick = ev.User
				f.nickCf = strings.ToLower(ev.User)
			} else {
				flows = append(flows, &mergedEvent{
					oldNick: ev.FormerNick,
					nick:    ev.User,
					nickCf:  strings.ToLower(ev.User),
				})
			}
		case irc.UserJoinEvent:
			f := flowNick(ev.User)
			if f != nil {
				if f.firstConnected == 0 {
					f.firstConnected = 1
				}
				f.lastConnected = 1
				f.modeSet = ""
				f.modeUnset = ""
			} else {
				flows = append(flows, &mergedEvent{
					nick:           ev.User,
					nickCf:         strings.ToLower(ev.User),
					firstConnected: 1,
					lastConnected:  1,
				})
			}
		case irc.UserPartEvent:
			f := flowNick(ev.User)
			if f != nil {
				if f.firstConnected == 0 {
					f.firstConnected = -1
				}
				f.lastConnected = -1
				f.modeSet = ""
				f.modeUnset = ""
			} else {
				flows = append(flows, &mergedEvent{
					nick:           ev.User,
					nickCf:         strings.ToLower(ev.User),
					firstConnected: -1,
					lastConnected:  -1,
				})
			}
		case irc.UserQuitEvent:
			f := flowNick(ev.User)
			if f != nil {
				if f.firstConnected == 0 {
					f.firstConnected = -1
				}
				f.lastConnected = -1
				f.modeSet = ""
				f.modeUnset = ""
			} else {
				flows = append(flows, &mergedEvent{
					nick:           ev.User,
					nickCf:         strings.ToLower(ev.User),
					firstConnected: -1,
					lastConnected:  -1,
				})
			}
		case irc.ModeChangeEvent:
			// best-effort heuristic for guessing simple user mode changes:
			// expect "<+/-><chars> <args...>" with as many chars as args

			mode := strings.Split(ev.Mode, " ")
			modeStr := mode[0]
			modeArgs := mode[1:]
			if len(modeStr) > 0 && (modeStr[0] == '+' || modeStr[0] == '-') && len(modeArgs) == len(modeStr)-1 {
				set := modeStr[0] == '+'
				for i, nick := range modeArgs {
					f := flowNick(nick)
					if f == nil {
						f = &mergedEvent{
							nick:   nick,
							nickCf: strings.ToLower(nick),
						}
						flows = append(flows, f)
					}

					mode := string(modeStr[i+1])
					if set {
						if strings.Contains(f.modeUnset, mode) {
							f.modeUnset = strings.Replace(f.modeUnset, mode, "", -1)
						} else if !strings.Contains(f.modeSet, mode) {
							f.modeSet += mode
						}
					} else {
						if strings.Contains(f.modeSet, mode) {
							f.modeSet = strings.Replace(f.modeSet, mode, "", -1)
						} else if !strings.Contains(f.modeUnset, mode) {
							f.modeUnset += mode
						}
					}
				}
			} else {
				if f := flowNick(""); f != nil && f.channelMode == ev.Mode {
					// setting the same channel mode string, ignore
				} else {
					flows = append(flows, &mergedEvent{
						channelMode: ev.Mode,
					})
				}
			}
		}
	}

	newBody := new(ui.StyledStringBuilder)
	newBody.Grow(128)
	first := true
	for _, f := range flows {
		l := app.formatEvent(f)
		if l.IsZero() {
			continue
		}
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
		prompt = ui.Styled(">", vaxis.Style{
			Foreground: app.cfg.Colors.Prompt,
		})
	} else if s == nil {
		prompt = ui.Styled("<offline>", vaxis.Style{
			Foreground: ui.ColorRed,
		})
	} else {
		prompt = app.win.IdentString(app.cfg.Colors.Nicks, s.Nick(), true)
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
		body = fmt.Sprintf("Topic (set by %s on %s): %s", who.Name, at.Local().Format("January 2 2006 at 15:04:05"), topic)
	}
	app.win.AddLine(netID, buffer, ui.Line{
		At:        time.Now(),
		Head:      "--",
		HeadColor: app.cfg.Colors.Status,
		Body: ui.Styled(body, vaxis.Style{
			Foreground: app.cfg.Colors.Status,
		}),
	})
	return true
}

func keyMatches(k vaxis.Key, r rune, mods vaxis.ModifierMask) bool {
	m := k.Modifiers
	m &^= vaxis.ModCapsLock
	m &^= vaxis.ModNumLock
	if k.Keycode == r && mods == m {
		// ctrl+a and user pressed ctrl+a
		// ctrl+. and user pressed ctrl+. on a US keyboard
		return true
	}
	if m&vaxis.ModShift != 0 {
		m &^= vaxis.ModShift
		if k.ShiftedCode == r && mods == m {
			// ctrl+. and user pressed ctrl+shift+; on a French keyboard
			return true
		}
	}
	return false
}

func formatSize(v int64) string {
	suffixes := []string{"B", "kB", "MB", "GB"}
	for i, suffix := range suffixes {
		if v < 1024 || i == len(suffixes)-1 {
			return fmt.Sprintf("%v%v", v, suffix)
		}
		v /= 1024
	}
	panic("unreachable")
}

func BuildVersion() (string, bool) {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.Main.Version, true
	} else {
		return "", false
	}
}

type ReadProgress struct {
	io.Reader
	period time.Duration
	f      func(int64)

	n    int64
	last time.Time
}

func (r *ReadProgress) Read(buf []byte) (int, error) {
	n, err := r.Reader.Read(buf)
	r.n += int64(n)
	now := time.Now()
	if now.Sub(r.last) > r.period {
		r.last = now
		r.f(r.n)
	}
	return n, err
}
