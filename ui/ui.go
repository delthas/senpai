package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/gdamore/tcell/v2"

	"git.sr.ht/~delthas/senpai/irc"
)

type Config struct {
	NickColWidth      int
	ChanColWidth      int
	ChanColEnabled    bool
	MemberColWidth    int
	MemberColEnabled  bool
	TextMaxWidth      int
	AutoComplete      func(cursorIdx int, text []rune) []Completion
	Mouse             bool
	MergeLine         func(former *Line, addition Line)
	Colors            ConfigColors
	LocalIntegrations bool
}

type ConfigColors struct {
	Status tcell.Color
	Prompt tcell.Color
	Unread tcell.Color
	Nicks  ColorScheme
}

type UI struct {
	screen tcell.Screen
	Events chan tcell.Event
	exit   atomic.Value // bool
	config Config

	bs          BufferList
	e           Editor
	prompt      StyledString
	status      string
	title       string
	overlayHint string

	channelOffset int
	memberClicked int
	memberOffset  int

	channelWidth int
	memberWidth  int

	channelColClicked bool
	memberColClicked  bool
}

func New(config Config) (ui *UI, err error) {
	ui = &UI{
		config: config,
	}
	if config.ChanColEnabled {
		ui.channelWidth = config.ChanColWidth
	}
	if config.MemberColEnabled {
		ui.memberWidth = config.MemberColWidth
	}

	ui.screen, err = tcell.NewScreen()
	if err != nil {
		return
	}

	err = ui.screen.Init()
	if err != nil {
		return
	}
	if ui.screen.HasMouse() && config.Mouse {
		ui.screen.EnableMouse()
	}
	ui.screen.EnablePaste()
	ui.screen.SetCursorStyle(tcell.CursorStyleSteadyBar)
	ui.screen.SetTitle("senpai")
	ui.screen.SetAppID("senpai")

	_, h := ui.screen.Size()
	ui.screen.Clear()
	ui.screen.ShowCursor(0, h-2)

	ui.exit.Store(false)

	ui.Events = make(chan tcell.Event, 128)
	go func() {
		for !ui.ShouldExit() {
			ev := ui.screen.PollEvent()
			if ev == nil {
				ui.Exit()
				break
			}
			ui.Events <- ev
		}
		close(ui.Events)
	}()

	ui.bs = NewBufferList(config.Colors, ui.config.MergeLine)
	ui.e = NewEditor(config.Colors, ui.config.AutoComplete)
	ui.Resize()

	return
}

func (ui *UI) ShouldExit() bool {
	return ui.exit.Load().(bool)
}

func (ui *UI) Exit() {
	ui.exit.Store(true)
}

func (ui *UI) Close() {
	// See: https://github.com/gdamore/tcell/issues/623
	ui.screen.SetCursorStyle(tcell.CursorStyleDefault)
	ui.screen.Sync()

	ui.screen.Fini()
}

func (ui *UI) Buffer(i int) (netID, title string, ok bool) {
	return ui.bs.Buffer(i)
}

func (ui *UI) CurrentBuffer() (netID, title string) {
	return ui.bs.Current()
}

func (ui *UI) NextBuffer() {
	ui.bs.Next()
	ui.memberOffset = 0
}

func (ui *UI) PreviousBuffer() {
	ui.bs.Previous()
	ui.memberOffset = 0
}

func (ui *UI) NextUnreadBuffer() {
	ui.bs.NextUnread()
	ui.memberOffset = 0
}

func (ui *UI) PreviousUnreadBuffer() {
	ui.bs.PreviousUnread()
	ui.memberOffset = 0
}

func (ui *UI) ClickedBuffer() int {
	return ui.bs.clicked
}

func (ui *UI) ClickBuffer(i int) {
	if i < len(ui.bs.list) {
		ui.bs.clicked = i
	}
}

func (ui *UI) ClickChannelCol(v bool) {
	ui.channelColClicked = v
}

func (ui *UI) ChannelColClicked() bool {
	return ui.channelColClicked
}

func (ui *UI) ResizeChannelCol(x int) {
	if x < 6 {
		x = 6
	} else if x > 24 {
		x = 24
	}
	ui.channelWidth = x
	ui.Resize()
}

func (ui *UI) ClickMemberCol(v bool) {
	ui.memberColClicked = v
}

func (ui *UI) MemberColClicked() bool {
	return ui.memberColClicked
}

func (ui *UI) ResizeMemberCol(x int) {
	if x < 6 {
		x = 6
	} else if x > 24 {
		x = 24
	}
	ui.memberWidth = x
	ui.Resize()
}

func (ui *UI) GoToBufferNo(i int) {
	if ui.bs.To(i) {
		ui.memberOffset = 0
	}
}

func (ui *UI) ShowBufferNumbers(enable bool) {
	ui.bs.ShowBufferNumbers(enable)
}

func (ui *UI) ClickedMember() int {
	return ui.memberClicked
}

func (ui *UI) ClickMember(i int) {
	ui.memberClicked = i
}

func (ui *UI) ScrollUp() {
	ui.bs.ScrollUp(ui.bs.tlHeight / 2)
}

func (ui *UI) ScrollDown() {
	ui.bs.ScrollDown(ui.bs.tlHeight / 2)
}

func (ui *UI) ScrollUpBy(n int) {
	ui.bs.ScrollUp(n)
}

func (ui *UI) ScrollDownBy(n int) {
	ui.bs.ScrollDown(n)
}

func (ui *UI) ScrollUpHighlight() bool {
	return ui.bs.ScrollUpHighlight()
}

func (ui *UI) ScrollDownHighlight() bool {
	return ui.bs.ScrollDownHighlight()
}

func (ui *UI) ScrollChannelUpBy(n int) {
	ui.channelOffset -= n
	if ui.channelOffset < 0 {
		ui.channelOffset = 0
	}
}

func (ui *UI) ScrollChannelDownBy(n int) {
	ui.channelOffset += n
}

func (ui *UI) HorizontalBufferOffset(x int) int {
	return ui.bs.HorizontalBufferOffset(x, ui.channelOffset)
}

func (ui *UI) ChannelOffset() int {
	return ui.channelOffset
}

func (ui *UI) MemberOffset() int {
	return ui.memberOffset
}

func (ui *UI) ChannelWidth() int {
	return ui.channelWidth
}

func (ui *UI) MemberWidth() int {
	return ui.memberWidth
}

func (ui *UI) ToggleChannelList() {
	if ui.channelWidth == 0 {
		ui.channelWidth = ui.config.ChanColWidth
	} else {
		ui.channelWidth = 0
	}
	ui.Resize()
}

func (ui *UI) ToggleMemberList() {
	if ui.memberWidth == 0 {
		ui.memberWidth = ui.config.MemberColWidth
	} else {
		ui.memberWidth = 0
	}
	ui.Resize()
}

func (ui *UI) ScrollMemberUpBy(n int) {
	ui.memberOffset -= n
	if ui.memberOffset < 0 {
		ui.memberOffset = 0
	}
}

func (ui *UI) ScrollMemberDownBy(n int) {
	ui.memberOffset += n
}

func (ui *UI) IsAtTop() bool {
	return ui.bs.IsAtTop()
}

func (ui *UI) OpenOverlay(hint string) {
	ui.bs.OpenOverlay()
	ui.overlayHint = hint
}

func (ui *UI) CloseOverlay() {
	ui.bs.CloseOverlay()
}

func (ui *UI) HasOverlay() bool {
	return ui.bs.HasOverlay()
}

func (ui *UI) AddBuffer(netID, netName, title string) (i int, added bool) {
	i, added = ui.bs.Add(netID, netName, title)
	if added {
		ui.ScrollToBuffer()
	}
	return
}

func (ui *UI) RemoveBuffer(netID, title string) {
	_ = ui.bs.Remove(netID, title)
	ui.memberOffset = 0
}

func (ui *UI) RemoveNetworkBuffers(netID string) {
	ui.bs.RemoveNetwork(netID)
	ui.memberOffset = 0
}

func (ui *UI) AddLine(netID, buffer string, line Line) {
	ui.bs.AddLine(netID, buffer, line)

	curNetID, curBuffer := ui.bs.Current()
	_, b := ui.bs.at(netID, buffer)
	if b != nil && line.Notify == NotifyHighlight && (curNetID != netID || curBuffer != buffer) {
		var header string
		if buffer != line.Head {
			header = fmt.Sprintf("%s — %s", buffer, line.Head)
		} else {
			header = line.Head
		}
		id := ui.notify(NotifyEvent{
			NetID:  netID,
			Buffer: buffer,
		}, header, line.Body.String())
		if id >= 0 {
			b.notifications = append(b.notifications, id)
		}
	}
}

func (ui *UI) AddLines(netID, buffer string, before, after []Line) {
	ui.bs.AddLines(netID, buffer, before, after)
}

func (ui *UI) JumpBuffer(sub string) bool {
	subLower := strings.ToLower(sub)
	for i, b := range ui.bs.list {
		if strings.Contains(strings.ToLower(b.title), subLower) {
			if ui.bs.To(i) {
				ui.memberOffset = 0
			}
			return true
		}
	}

	return false
}

func (ui *UI) JumpBufferIndex(i int) bool {
	if i >= 0 && i < len(ui.bs.list) {
		if ui.bs.To(i) {
			ui.memberOffset = 0
		}
		return true
	}
	return false
}

func (ui *UI) JumpBufferNetwork(netID, buffer string) bool {
	for i, b := range ui.bs.list {
		if b.netID == netID && strings.ToLower(b.title) == strings.ToLower(buffer) {
			if ui.bs.To(i) {
				ui.memberOffset = 0
			}
			return true
		}
	}
	return false
}

func (ui *UI) SetTopic(netID, buffer string, topic string) {
	ui.bs.SetTopic(netID, buffer, topic)
}

func (ui *UI) SetRead(netID, buffer string, timestamp time.Time) {
	ui.bs.SetRead(netID, buffer, timestamp)
}

func (ui *UI) UpdateRead() (netID, buffer string, timestamp time.Time) {
	return ui.bs.UpdateRead()
}

func (ui *UI) SetStatus(status string) {
	ui.status = status
}

func (ui *UI) SetPrompt(prompt StyledString) {
	ui.prompt = prompt
}

func (ui *UI) SetTitle(title string) {
	if ui.title == title {
		return
	}
	ui.title = title
	ui.screen.SetTitle(title)
}

func (ui *UI) SetMouseShape(shape vaxis.MouseShape) {
	ui.screen.Vaxis().SetMouseShape(shape)
}

// InputContent result must not be modified.
func (ui *UI) InputContent() []rune {
	return ui.e.Content()
}

func (ui *UI) InputRune(r rune) {
	ui.e.PutRune(r)
}

func (ui *UI) InputRight() {
	ui.e.Right()
}

func (ui *UI) InputRightWord() {
	ui.e.RightWord()
}

func (ui *UI) InputLeft() {
	ui.e.Left()
}

func (ui *UI) InputLeftWord() {
	ui.e.LeftWord()
}

func (ui *UI) InputHome() {
	ui.e.Home()
}

func (ui *UI) InputEnd() {
	ui.e.End()
}

func (ui *UI) InputUp() {
	ui.e.Up()
}

func (ui *UI) InputDown() {
	ui.e.Down()
}

func (ui *UI) InputBackspace() (ok bool) {
	return ui.e.RemRune()
}

func (ui *UI) InputDelete() (ok bool) {
	return ui.e.RemRuneForward()
}

func (ui *UI) InputDeleteWord() (ok bool) {
	return ui.e.RemWord()
}

func (ui *UI) InputAutoComplete() (ok bool) {
	return ui.e.AutoComplete()
}

func (ui *UI) InputFlush() (content string) {
	return ui.e.Flush()
}

func (ui *UI) InputClear() bool {
	return ui.e.Clear()
}

func (ui *UI) InputSet(text string) {
	ui.e.Set(text)
}

func (ui *UI) InputBackSearch() {
	ui.e.BackSearch()
}

func (ui *UI) Resize() {
	w, h := ui.screen.Size()
	innerWidth := w - 9 - ui.channelWidth - ui.config.NickColWidth - ui.memberWidth
	if innerWidth <= 0 {
		innerWidth = 1 // will break display somewhat, but this is an edge case
	}
	ui.e.Resize(innerWidth)
	textWidth := innerWidth
	if ui.config.TextMaxWidth > 0 && ui.config.TextMaxWidth < textWidth {
		textWidth = ui.config.TextMaxWidth
	}
	if ui.channelWidth == 0 {
		ui.bs.ResizeTimeline(innerWidth, h-3, textWidth)
	} else {
		ui.bs.ResizeTimeline(innerWidth, h-2, textWidth)
	}
	ui.ScrollToBuffer()
	ui.screen.Sync()
}

func (ui *UI) Size() (int, int) {
	return ui.screen.Size()
}

func (ui *UI) Beep() {
	ui.screen.Beep()
}

func (ui *UI) Notify(title string, body string) {
	ui.screen.Notify(title, body)
}

func (ui *UI) Draw(members []irc.Member) {
	w, h := ui.screen.Size()

	ui.bs.DrawTimeline(ui.screen, ui.channelWidth, 0, ui.config.NickColWidth)
	if ui.channelWidth == 0 {
		ui.bs.DrawHorizontalBufferList(ui.screen, 0, h-1, w-ui.memberWidth, &ui.channelOffset)
	} else {
		ui.bs.DrawVerticalBufferList(ui.screen, 0, 0, ui.channelWidth, h, &ui.channelOffset)
	}
	if ui.memberWidth != 0 {
		ui.drawVerticalMemberList(ui.screen, w-ui.memberWidth, 0, ui.memberWidth, h, members, &ui.memberOffset)
	}
	if ui.channelWidth == 0 {
		ui.drawStatusBar(ui.channelWidth, h-3, w-ui.memberWidth)
	} else {
		ui.drawStatusBar(ui.channelWidth, h-2, w-ui.channelWidth-ui.memberWidth)
	}

	prompt := ui.prompt
	if ui.bs.HasOverlay() && ui.e.TextLen() == 0 {
		prompt = Styled(">", tcell.StyleDefault.Foreground(ui.config.Colors.Prompt))
	}
	if ui.channelWidth == 0 {
		for x := 0; x < 9+ui.config.NickColWidth; x++ {
			ui.screen.SetContent(x, h-2, ' ', nil, tcell.StyleDefault)
		}
		printIdent(ui.screen, 7, h-2, ui.config.NickColWidth, prompt)
	} else {
		for x := ui.channelWidth; x < 9+ui.channelWidth+ui.config.NickColWidth; x++ {
			ui.screen.SetContent(x, h-1, ' ', nil, tcell.StyleDefault)
		}
		printIdent(ui.screen, ui.channelWidth+7, h-1, ui.config.NickColWidth, prompt)
	}

	var hint string
	if ui.bs.HasOverlay() {
		hint = ui.overlayHint
	}
	if ui.channelWidth == 0 {
		ui.e.Draw(ui.screen, 9+ui.config.NickColWidth, h-2, hint)
	} else {
		ui.e.Draw(ui.screen, 9+ui.channelWidth+ui.config.NickColWidth, h-1, hint)
	}

	ui.screen.Show()
}

func (ui *UI) ScrollToBuffer() {
	if ui.bs.current < ui.channelOffset {
		ui.channelOffset = ui.bs.current
		return
	}

	w, h := ui.screen.Size()
	var first int
	if ui.channelWidth > 0 {
		first = ui.bs.current - h + 1
	} else {
		first = ui.bs.GetLeftMost(w - ui.memberWidth)
	}
	if ui.channelOffset < first {
		ui.channelOffset = first
	}
}

func (ui *UI) drawStatusBar(x0, y, width int) {
	clearArea(ui.screen, x0, y, width, 1)

	if ui.status == "" {
		return
	}

	var s StyledStringBuilder
	s.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
	s.WriteString("--")

	x := x0 + 5 + ui.config.NickColWidth
	printString(ui.screen, &x, y, s.StyledString())
	x += 2

	s.Reset()
	s.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
	s.WriteString(ui.status)

	printString(ui.screen, &x, y, s.StyledString())
}

func (ui *UI) drawVerticalMemberList(screen tcell.Screen, x0, y0, width, height int, members []irc.Member, offset *int) {
	if y0+len(members)-*offset < height {
		*offset = y0 + len(members) - height
		if *offset < 0 {
			*offset = 0
		}
	}

	drawVerticalLine(screen, x0, y0, height)
	x0++
	width--
	clearArea(screen, x0, y0, width, height)

	if _, channel := ui.bs.Current(); channel == "" {
		x := x0 + 1
		printString(screen, &x, y0, Styled("Help", tcell.StyleDefault.Foreground(ui.config.Colors.Status)))
		drawHorizontalLine(screen, x0, y0+1, width)
		y0 += 2

		lines := []string{
			"→Add network",
			"→Join channel",
			"→Message user",
		}
		for i, line := range lines {
			reverse := i*2 == ui.memberClicked
			x := x0
			printString(screen, &x, y0, Styled(line, tcell.StyleDefault.Reverse(reverse)))
			drawHorizontalLine(screen, x0, y0+1, width)
			y0 += 2
		}
		return
	}

	if ui.memberClicked >= len(members) {
		ui.memberClicked = len(members) - 1
	}

	if len(members) > 0 {
		var memberString string
		if len(members) > 1 {
			memberString = fmt.Sprintf("%d members", len(members))
		} else {
			memberString = fmt.Sprintf("%d member", len(members))
		}
		memberString = truncate(memberString, width-1, "\u2026")
		xMembers := x0 + 1
		printString(screen, &xMembers, y0, Styled(memberString, tcell.StyleDefault.Foreground(ui.config.Colors.Status)))
	}
	y0++
	height--
	drawHorizontalLine(screen, x0, y0, width)
	y0++
	height--

	padding := 1
	for _, m := range members {
		if m.Disconnected {
			padding = runeWidth(0x274C)
			break
		}
	}

	for i, m := range members[*offset:] {
		reverse := i+*offset == ui.memberClicked
		x := x0
		y := y0 + i
		if m.Disconnected {
			disconnectedSt := tcell.StyleDefault.Foreground(tcell.ColorRed).Reverse(reverse)
			printString(screen, &x, y, Styled("\u274C", disconnectedSt))
		} else if m.PowerLevel != "" {
			x += padding - 1
			powerLevelText := m.PowerLevel[:1]
			powerLevelSt := tcell.StyleDefault.Foreground(tcell.ColorGreen).Reverse(reverse)
			printString(screen, &x, y, Styled(powerLevelText, powerLevelSt))
		} else {
			x += padding
		}

		var name StyledString
		nameText := truncate(m.Name.Name, width-1, "\u2026")
		if m.Away {
			name = Styled(nameText, tcell.StyleDefault.Foreground(tcell.ColorGray).Reverse(reverse))
		} else {
			color := IdentColor(ui.config.Colors.Nicks, m.Name.Name, m.Self)
			name = Styled(nameText, tcell.StyleDefault.Foreground(color).Reverse(reverse))
		}

		printString(screen, &x, y, name)
	}
}
