package ui

import (
	"strings"
	"sync/atomic"

	"git.sr.ht/~taiite/senpai/irc"

	"github.com/gdamore/tcell/v2"
)

type Config struct {
	NickColWidth   int
	ChanColWidth   int
	MemberColWidth int
	AutoComplete   func(cursorIdx int, text []rune) []Completion
	Mouse          bool
}

type UI struct {
	screen tcell.Screen
	Events chan tcell.Event
	exit   atomic.Value // bool
	config Config

	bs     BufferList
	e      Editor
	prompt StyledString
	status string

	channelOffset int
	memberOffset  int
}

func New(config Config) (ui *UI, err error) {
	ui = &UI{
		config: config,
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

	_, h := ui.screen.Size()
	ui.screen.Clear()
	ui.screen.ShowCursor(0, h-2)

	ui.exit.Store(false)

	ui.Events = make(chan tcell.Event, 128)
	go func() {
		for !ui.ShouldExit() {
			ui.Events <- ui.screen.PollEvent()
		}
	}()

	ui.bs = NewBufferList()
	ui.e = NewEditor(ui.config.AutoComplete)
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
	ui.screen.Fini()
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

func (ui *UI) ClickedBuffer() int {
	return ui.bs.clicked
}

func (ui *UI) ClickBuffer(i int) {
	if i < len(ui.bs.list) {
		ui.bs.clicked = i
	}
}

func (ui *UI) GoToBufferNo(i int) {
	if ui.bs.To(i) {
		ui.memberOffset = 0
	}
}

func (ui *UI) ShowBufferNumbers(enable bool) {
	ui.bs.ShowBufferNumbers(enable)
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

func (ui *UI) ChannelOffset() int {
	return ui.channelOffset
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

func (ui *UI) AddBuffer(netID, netName, title string) (i int, added bool) {
	return ui.bs.Add(netID, netName, title)
}

func (ui *UI) RemoveBuffer(title string) {
	_ = ui.bs.Remove(title)
	ui.memberOffset = 0
}

func (ui *UI) AddLine(netID, buffer string, notify NotifyType, line Line) {
	ui.bs.AddLine(netID, buffer, notify, line)
}

func (ui *UI) AddLines(netID, buffer string, before, after []Line) {
	ui.bs.AddLines(netID, buffer, before, after)
}

func (ui *UI) AppendLine(netID, title string, messageID string, body StyledString) {
	ui.bs.AppendLine(netID, title, messageID, body)
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

func (ui *UI) JumpBufferNetwork(netID, sub string) bool {
	subLower := strings.ToLower(sub)
	for i, b := range ui.bs.list {
		if b.netID == netID && strings.Contains(strings.ToLower(b.title), subLower) {
			if ui.bs.To(i) {
				ui.memberOffset = 0
			}
			return true
		}
	}
	return false
}

func (ui *UI) SetStatus(status string) {
	ui.status = status
}

func (ui *UI) SetPrompt(prompt StyledString) {
	ui.prompt = prompt
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

func (ui *UI) InputAutoComplete(offset int) (ok bool) {
	return ui.e.AutoComplete(offset)
}

func (ui *UI) InputEnter() (content string) {
	return ui.e.Flush()
}

func (ui *UI) InputClear() bool {
	return ui.e.Clear()
}

func (ui *UI) InputBackSearch() {
	ui.e.BackSearch()
}

func (ui *UI) Resize() {
	w, h := ui.screen.Size()
	innerWidth := w - 9 - ui.config.ChanColWidth - ui.config.NickColWidth - ui.config.MemberColWidth
	ui.e.Resize(innerWidth)
	if ui.config.ChanColWidth == 0 {
		ui.bs.ResizeTimeline(innerWidth, h-3)
	} else {
		ui.bs.ResizeTimeline(innerWidth, h-2)
	}
	ui.screen.Sync()
}

func (ui *UI) Size() (int, int) {
	return ui.screen.Size()
}

func (ui *UI) Draw(members []irc.Member) {
	w, h := ui.screen.Size()

	if ui.config.ChanColWidth == 0 {
		ui.e.Draw(ui.screen, 9+ui.config.NickColWidth, h-2)
	} else {
		ui.e.Draw(ui.screen, 9+ui.config.ChanColWidth+ui.config.NickColWidth, h-1)
	}

	ui.bs.DrawTimeline(ui.screen, ui.config.ChanColWidth, 0, ui.config.NickColWidth)
	if ui.config.ChanColWidth == 0 {
		ui.bs.DrawHorizontalBufferList(ui.screen, 0, h-1, w-ui.config.MemberColWidth)
	} else {
		ui.bs.DrawVerticalBufferList(ui.screen, 0, 0, ui.config.ChanColWidth, h, &ui.channelOffset)
	}
	if ui.config.MemberColWidth != 0 {
		drawVerticalMemberList(ui.screen, w-ui.config.MemberColWidth, 0, ui.config.MemberColWidth, h, members, &ui.memberOffset)
	}
	if ui.config.ChanColWidth == 0 {
		ui.drawStatusBar(ui.config.ChanColWidth, h-3, w-ui.config.MemberColWidth)
	} else {
		ui.drawStatusBar(ui.config.ChanColWidth, h-2, w-ui.config.ChanColWidth-ui.config.MemberColWidth)
	}

	if ui.config.ChanColWidth == 0 {
		for x := 0; x < 9+ui.config.NickColWidth; x++ {
			ui.screen.SetContent(x, h-2, ' ', nil, tcell.StyleDefault)
		}
		printIdent(ui.screen, 7, h-2, ui.config.NickColWidth, ui.prompt)
	} else {
		for x := ui.config.ChanColWidth; x < 9+ui.config.ChanColWidth+ui.config.NickColWidth; x++ {
			ui.screen.SetContent(x, h-1, ' ', nil, tcell.StyleDefault)
		}
		printIdent(ui.screen, ui.config.ChanColWidth+7, h-1, ui.config.NickColWidth, ui.prompt)
	}

	ui.screen.Show()
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

func drawVerticalMemberList(screen tcell.Screen, x0, y0, width, height int, members []irc.Member, offset *int) {
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

	for i, m := range members[*offset:] {
		x := x0
		y := y0 + i
		if m.PowerLevel != "" {
			powerLevelText := m.PowerLevel[:1]
			powerLevelSt := tcell.StyleDefault.Foreground(tcell.ColorGreen)
			printString(screen, &x, y, Styled(powerLevelText, powerLevelSt))
		} else {
			x++
		}

		var name StyledString
		nameText := truncate(m.Name.Name, width-1, "\u2026")
		if m.Away {
			name = Styled(nameText, tcell.StyleDefault.Foreground(tcell.ColorGray))
		} else {
			name = PlainString(nameText)
		}

		printString(screen, &x, y, name)
	}
}
