package senpai

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~taiite/senpai/irc"
	"git.sr.ht/~taiite/senpai/ui"
	"github.com/gdamore/tcell/v2"
)

var (
	errOffline = fmt.Errorf("you are disconnected from the server, retry later")
)

type command struct {
	AllowHome bool
	MinArgs   int
	MaxArgs   int
	Usage     string
	Desc      string
	Handle    func(app *App, args []string) error
}

type commandSet map[string]*command

var commands commandSet

func init() {
	commands = commandSet{
		"HELP": {
			AllowHome: true,
			MaxArgs:   1,
			Usage:     "[command]",
			Desc:      "show the list of commands, or how to use the given one",
			Handle:    commandDoHelp,
		},
		"JOIN": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<channels> [keys]",
			Desc:      "join a channel",
			Handle:    commandDoJoin,
		},
		"ME": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<message>",
			Desc:      "send an action (reply to last query if sent from home)",
			Handle:    commandDoMe,
		},
		"MSG": {
			AllowHome: true,
			MinArgs:   2,
			MaxArgs:   2,
			Usage:     "<target> <message>",
			Desc:      "send a message to the given target",
			Handle:    commandDoMsg,
		},
		"NAMES": {
			Desc:   "show the member list of the current channel",
			Handle: commandDoNames,
		},
		"NICK": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<nickname>",
			Desc:      "change your nickname",
			Handle:    commandDoNick,
		},
		"MODE": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   5, // <channel> <flags> <limit> <user> <ban mask>
			Usage:     "[<nick/channel>] <flags> [args]",
			Desc:      "change channel or user modes",
			Handle:    commandDoMode,
		},
		"PART": {
			AllowHome: true,
			MaxArgs:   2,
			Usage:     "[channel] [reason]",
			Desc:      "part a channel",
			Handle:    commandDoPart,
		},
		"QUERY": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "[nick]",
			Desc:      "opens a buffer to a user",
			Handle:    commandDoQuery,
		},
		"QUIT": {
			AllowHome: true,
			MaxArgs:   1,
			Usage:     "[reason]",
			Desc:      "quit senpai",
			Handle:    commandDoQuit,
		},
		"QUOTE": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<raw message>",
			Desc:      "send raw protocol data",
			Handle:    commandDoQuote,
		},
		"REPLY": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<message>",
			Desc:      "reply to the last query",
			Handle:    commandDoR,
		},
		"TOPIC": {
			MaxArgs: 1,
			Usage:   "[topic]",
			Desc:    "show or set the topic of the current channel",
			Handle:  commandDoTopic,
		},
		"BUFFER": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<name>",
			Desc:      "switch to the buffer containing a substring",
			Handle:    commandDoBuffer,
		},
		"INVITE": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<name> [channel]",
			Desc:      "invite someone to a channel",
			Handle:    commandDoInvite,
		},
		"KICK": {
			AllowHome: false,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<nick> [channel]",
			Desc:      "eject someone from the channel",
			Handle:    commandDoKick,
		},
		"BAN": {
			AllowHome: false,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<nick> [channel]",
			Desc:      "ban someone from entering the channel",
			Handle:    commandDoBan,
		},
		"UNBAN": {
			AllowHome: false,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<nick> [channel]",
			Desc:      "remove effect of a ban from the user",
			Handle:    commandDoUnban,
		},
	}
}

func noCommand(app *App, content string) error {
	netID, buffer := app.win.CurrentBuffer()
	if buffer == "" {
		return fmt.Errorf("can't send message to this buffer")
	}
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}

	s.PrivMsg(buffer, content)
	if !s.HasCapability("echo-message") {
		buffer, line, _ := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          buffer,
			TargetIsChannel: s.IsChannel(buffer),
			Command:         "PRIVMSG",
			Content:         content,
			Time:            time.Now(),
		})
		app.win.AddLine(netID, buffer, ui.NotifyNone, line)
	}

	return nil
}

func commandDoBuffer(app *App, args []string) error {
	name := args[0]
	i, err := strconv.Atoi(name)
	if err == nil {
		if app.win.JumpBufferIndex(i) {
			return nil
		}
	}
	if !app.win.JumpBuffer(args[0]) {
		return fmt.Errorf("none of the buffers match %q", name)
	}

	return nil
}

func commandDoHelp(app *App, args []string) (err error) {
	t := time.Now()
	netID, buffer := app.win.CurrentBuffer()

	addLineCommand := func(sb *ui.StyledStringBuilder, name string, cmd *command) {
		sb.Reset()
		sb.Grow(len(name) + 1 + len(cmd.Usage))
		sb.SetStyle(tcell.StyleDefault.Bold(true))
		sb.WriteString(name)
		sb.SetStyle(tcell.StyleDefault)
		sb.WriteByte(' ')
		sb.WriteString(cmd.Usage)
		app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
			At:   t,
			Body: sb.StyledString(),
		})
		app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
			At:   t,
			Body: ui.PlainSprintf("  %s", cmd.Desc),
		})
		app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
			At: t,
		})
	}

	addLineCommands := func(names []string) {
		sort.Strings(names)
		var sb ui.StyledStringBuilder
		for _, name := range names {
			addLineCommand(&sb, name, commands[name])
		}
	}

	if len(args) == 0 {
		app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
			At:   t,
			Head: "--",
			Body: ui.PlainString("Available commands:"),
		})

		cmdNames := make([]string, 0, len(commands))
		for cmdName := range commands {
			cmdNames = append(cmdNames, cmdName)
		}
		addLineCommands(cmdNames)
	} else {
		search := strings.ToUpper(args[0])
		app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
			At:   t,
			Head: "--",
			Body: ui.PlainSprintf("Commands that match \"%s\":", search),
		})

		cmdNames := make([]string, 0, len(commands))
		for cmdName := range commands {
			if !strings.Contains(cmdName, search) {
				continue
			}
			cmdNames = append(cmdNames, cmdName)
		}
		if len(cmdNames) == 0 {
			app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
				At:   t,
				Body: ui.PlainSprintf("  no command matches %q", args[0]),
			})
		} else {
			addLineCommands(cmdNames)
		}
	}
	return nil
}

func commandDoJoin(app *App, args []string) (err error) {
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	channel := args[0]
	key := ""
	if len(args) == 2 {
		key = args[1]
	}
	s.Join(channel, key)
	return nil
}

func commandDoMe(app *App, args []string) (err error) {
	netID, buffer := app.win.CurrentBuffer()
	if buffer == "" {
		netID = app.lastQueryNet
		buffer = app.lastQuery
	}
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	content := fmt.Sprintf("\x01ACTION %s\x01", args[0])
	s.PrivMsg(buffer, content)
	if !s.HasCapability("echo-message") {
		buffer, line, _ := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          buffer,
			TargetIsChannel: s.IsChannel(buffer),
			Command:         "PRIVMSG",
			Content:         content,
			Time:            time.Now(),
		})
		app.win.AddLine(netID, buffer, ui.NotifyNone, line)
	}
	return nil
}

func commandDoMsg(app *App, args []string) (err error) {
	target := args[0]
	content := args[1]
	netID, _ := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	s.PrivMsg(target, content)
	if !s.HasCapability("echo-message") {
		buffer, line, _ := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          target,
			TargetIsChannel: s.IsChannel(target),
			Command:         "PRIVMSG",
			Content:         content,
			Time:            time.Now(),
		})
		if buffer != "" && !s.IsChannel(target) {
			app.monitor[netID][buffer] = struct{}{}
			s.MonitorAdd(buffer)
			app.win.AddBuffer(netID, "", buffer)
		}

		app.win.AddLine(netID, buffer, ui.NotifyNone, line)
	}
	return nil
}

func commandDoNames(app *App, args []string) (err error) {
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	if !s.IsChannel(buffer) {
		return fmt.Errorf("this is not a channel")
	}
	var sb ui.StyledStringBuilder
	sb.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
	sb.WriteString("Names: ")
	for _, name := range s.Names(buffer) {
		if name.PowerLevel != "" {
			sb.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGreen))
			sb.WriteString(name.PowerLevel)
			sb.SetStyle(tcell.StyleDefault.Foreground(tcell.ColorGray))
		}
		sb.WriteString(name.Name.Name)
		sb.WriteByte(' ')
	}
	body := sb.StyledString()
	// TODO remove last space
	app.win.AddLine(netID, buffer, ui.NotifyNone, ui.Line{
		At:        time.Now(),
		Head:      "--",
		HeadColor: tcell.ColorGray,
		Body:      body,
	})
	return nil
}

func commandDoNick(app *App, args []string) (err error) {
	nick := args[0]
	if i := strings.IndexAny(nick, " :"); i >= 0 {
		return fmt.Errorf("illegal char %q in nickname", nick[i])
	}
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.ChangeNick(nick)
	return
}

func commandDoMode(app *App, args []string) (err error) {
	if strings.HasPrefix(args[0], "+") || strings.HasPrefix(args[0], "-") {
		// if we do eg /MODE +P, automatically insert the current channel: /MODE #<current-chan> +P
		_, channel := app.win.CurrentBuffer()
		args = append([]string{channel}, args...)
	}
	channel := args[0]
	flags := args[1]
	modeArgs := args[2:]

	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.ChangeMode(channel, flags, modeArgs)
	return nil
}

func commandDoPart(app *App, args []string) (err error) {
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	reason := ""
	if 0 < len(args) {
		if s.IsChannel(args[0]) {
			channel = args[0]
			if 1 < len(args) {
				reason = args[1]
			}
		} else {
			reason = args[0]
		}
	}

	if channel == "" {
		return fmt.Errorf("cannot part this buffer")
	}

	if s.IsChannel(channel) {
		s.Part(channel, reason)
	} else {
		app.win.RemoveBuffer(netID, channel)
	}
	return nil
}

func commandDoQuery(app *App, args []string) (err error) {
	netID, _ := app.win.CurrentBuffer()
	s := app.sessions[netID]
	target := args[0]
	if s.IsChannel(target) {
		return fmt.Errorf("cannot query a channel, use JOIN instead")
	}
	s.MonitorAdd(target)
	i, _ := app.win.AddBuffer(netID, "", target)
	s.NewHistoryRequest(target).WithLimit(200).Before(time.Now())
	app.win.JumpBufferIndex(i)
	return nil
}

func commandDoQuit(app *App, args []string) (err error) {
	reason := ""
	if 0 < len(args) {
		reason = args[0]
	}
	for _, session := range app.sessions {
		session.Quit(reason)
	}
	app.win.Exit()
	return nil
}

func commandDoQuote(app *App, args []string) (err error) {
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.SendRaw(args[0])
	return nil
}

func commandDoR(app *App, args []string) (err error) {
	s := app.sessions[app.lastQueryNet]
	if s == nil {
		return errOffline
	}
	s.PrivMsg(app.lastQuery, args[0])
	if !s.HasCapability("echo-message") {
		buffer, line, _ := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          app.lastQuery,
			TargetIsChannel: s.IsChannel(app.lastQuery),
			Command:         "PRIVMSG",
			Content:         args[0],
			Time:            time.Now(),
		})
		app.win.AddLine(app.lastQueryNet, buffer, ui.NotifyNone, line)
	}
	return nil
}

func commandDoTopic(app *App, args []string) (err error) {
	netID, buffer := app.win.CurrentBuffer()
	var ok bool
	if len(args) == 0 {
		ok = app.printTopic(netID, buffer)
	} else {
		s := app.sessions[netID]
		if s != nil {
			s.ChangeTopic(buffer, args[0])
			ok = true
		}
	}
	if !ok {
		return errOffline
	}
	return nil
}

func commandDoInvite(app *App, args []string) (err error) {
	nick := args[0]
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	if len(args) == 2 {
		channel = args[1]
	} else if channel == "" {
		return fmt.Errorf("either send this command from a channel, or specify the channel")
	}
	s.Invite(nick, channel)
	return nil
}

func commandDoKick(app *App, args []string) (err error) {
	nick := args[0]
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	if len(args) >= 2 {
		channel = args[1]
	} else if channel == "" {
		return fmt.Errorf("either send this command from a channel, or specify the channel")
	}
	comment := ""
	if len(args) == 3 {
		comment = args[2]
	}
	s.Kick(nick, channel, comment)
	return nil
}

func commandDoBan(app *App, args []string) (err error) {
	nick := args[0]
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	if len(args) == 2 {
		channel = args[1]
	} else if channel == "" {
		return fmt.Errorf("either send this command from a channel, or specify the channel")
	}
	s.ChangeMode(channel, "+b", []string{nick})
	return nil
}

func commandDoUnban(app *App, args []string) (err error) {
	nick := args[0]
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	if len(args) == 2 {
		channel = args[1]
	} else if channel == "" {
		return fmt.Errorf("either send this command from a channel, or specify the channel")
	}
	s.ChangeMode(channel, "-b", []string{nick})
	return nil
}

// implemented from https://golang.org/src/strings/strings.go?s=8055:8085#L310
func fieldsN(s string, n int) []string {
	s = strings.TrimSpace(s)
	if s == "" || n == 0 {
		return nil
	}
	if n == 1 {
		return []string{s}
	}
	n--
	// Start of the ASCII fast path.
	var a []string
	na := 0
	fieldStart := 0
	i := 0
	// Skip spaces in front of the input.
	for i < len(s) && s[i] == ' ' {
		i++
	}
	fieldStart = i
	for i < len(s) {
		if s[i] != ' ' {
			i++
			continue
		}
		a = append(a, s[fieldStart:i])
		na++
		i++
		// Skip spaces in between fields.
		for i < len(s) && s[i] == ' ' {
			i++
		}
		fieldStart = i
		if n <= na {
			a = append(a, s[fieldStart:])
			return a
		}
	}
	if fieldStart < len(s) {
		// Last field ends at EOF.
		a = append(a, s[fieldStart:])
	}
	return a
}

func parseCommand(s string) (command, args string, isCommand bool) {
	if s[0] != '/' {
		return "", s, false
	}
	if len(s) > 1 && s[1] == '/' {
		// Input starts with two slashes.
		return "", s[1:], false
	}

	i := strings.IndexByte(s, ' ')
	if i < 0 {
		i = len(s)
	}

	return strings.ToUpper(s[1:i]), strings.TrimLeft(s[i:], " "), true
}

func (app *App) handleInput(buffer, content string) error {
	if content == "" {
		return nil
	}

	cmdName, rawArgs, isCommand := parseCommand(content)
	if !isCommand {
		return noCommand(app, rawArgs)
	}
	if cmdName == "" {
		return fmt.Errorf("lone slash at the beginning")
	}

	var chosenCMDName string
	var found bool
	for key := range commands {
		if !strings.HasPrefix(key, cmdName) {
			continue
		}
		if found {
			return fmt.Errorf("ambiguous command %q (could mean %v or %v)", cmdName, chosenCMDName, key)
		}
		chosenCMDName = key
		found = true
	}
	if !found {
		return fmt.Errorf("command %q doesn't exist", cmdName)
	}

	cmd := commands[chosenCMDName]

	var args []string
	if rawArgs != "" && cmd.MaxArgs != 0 {
		args = fieldsN(rawArgs, cmd.MaxArgs)
	}

	if len(args) < cmd.MinArgs {
		return fmt.Errorf("usage: %s %s", chosenCMDName, cmd.Usage)
	}
	if buffer == "" && !cmd.AllowHome {
		return fmt.Errorf("command %s cannot be executed from a server buffer", chosenCMDName)
	}

	return cmd.Handle(app, args)
}
