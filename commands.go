package senpai

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/delthas/go-libnp"
	"golang.org/x/net/context"

	"git.sr.ht/~delthas/senpai/irc"
	"git.sr.ht/~delthas/senpai/ui"
)

var (
	errOffline = fmt.Errorf("you are disconnected from the server, retry later")
)

const maxArgsInfinite = -1

type command struct {
	AllowHome bool
	MinArgs   int
	MaxArgs   int
	Usage     string
	Desc      string
	Handle    func(app *App, args []string) error // nil = passthrough
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
		"BOUNCER": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<bouncer message>",
			Desc:      "send command to the bouncer service (only works with soju); e.g. /bouncer help",
			Handle:    commandDoBouncer,
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
			MinArgs: 1,
			MaxArgs: 1,
			Usage:   "<message>",
			Desc:    "send an action (reply to last query if sent from home)",
			Handle:  commandDoMe,
		},
		"NP": {
			Desc:   "send the current song that is being played on the system",
			Handle: commandDoNP,
		},
		"UPLOAD": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<file path>",
			Desc:      "upload a local file to the bouncer",
			Handle:    commandDoUpload,
		},
		"MSG": {
			AllowHome: true,
			MinArgs:   2,
			MaxArgs:   2,
			Usage:     "<target> <message>",
			Desc:      "send a message to the given target",
			Handle:    commandDoMsg,
		},
		"MOTD": {
			AllowHome: true,
			Desc:      "show the message of the day (MOTD)",
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
		"OPER": {
			AllowHome: true,
			MinArgs:   2,
			MaxArgs:   2,
			Usage:     "<username> <password>",
			Desc:      "log in to an operator account",
		},
		"MODE": {
			AllowHome: true,
			MaxArgs:   maxArgsInfinite,
			Usage:     "[<nick/channel>] [<flags>] [args]",
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
			MaxArgs:   2,
			Usage:     "[nick] [message]",
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
		"LIST": {
			AllowHome: true,
			MaxArgs:   1,
			Usage:     "[pattern]",
			Desc:      "list public channels",
			Handle:    commandDoList,
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
			Usage:     "<index|name>",
			Desc:      "switch to the buffer at the position or containing a substring",
			Handle:    commandDoBuffer,
		},
		"WHOIS": {
			AllowHome: true,
			MinArgs:   0,
			MaxArgs:   1,
			Usage:     "<nick>",
			Desc:      "get information about someone who is connected",
			Handle:    commandDoWhois,
		},
		"WHOWAS": {
			AllowHome: true,
			MinArgs:   0,
			MaxArgs:   1,
			Usage:     "<nick>",
			Desc:      "get information about someone who is disconnected",
			Handle:    commandDoWhowas,
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
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   3,
			Usage:     "<nick> [channel] [message]",
			Desc:      "eject someone from the channel",
			Handle:    commandDoKick,
		},
		"BAN": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<nick> [channel]",
			Desc:      "ban someone from entering the channel",
			Handle:    commandDoBan,
		},
		"UNBAN": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<nick> [channel]",
			Desc:      "remove effect of a ban from the user",
			Handle:    commandDoUnban,
		},
		"CONNECT": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   3,
			Usage:     "<target server> [<port> [remote server]]",
			Desc:      "connect a server to the network",
		},
		"SQUIT": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<server> [comment]",
			Desc:      "disconnect a server from the network",
		},
		"KILL": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<nick> [message]",
			Desc:      "eject someone from the server",
		},
		"SEARCH": {
			MaxArgs: 1,
			Usage:   "<text>",
			Desc:    "search messages in a target",
			Handle:  commandDoSearch,
		},
		"AWAY": {
			AllowHome: true,
			MinArgs:   0,
			MaxArgs:   1,
			Usage:     "[message]",
			Desc:      "mark yourself as away",
			Handle:    commandDoAway,
		},
		"BACK": {
			AllowHome: true,
			Desc:      "mark yourself as back from being away",
			Handle:    commandDoBack,
		},
		"SHRUG": {
			Desc:    "send a shrug to the current channel ¯\\_(ツ)_/¯",
			MaxArgs: maxArgsInfinite,
			Handle:  commandDoShrug,
		},
		"TABLEFLIP": {
			Desc:   "send a tableflip to the current channel (╯°□°)╯︵ ┻━┻",
			Handle: commandDoTableFlip,
		},
		"VERSION": {
			AllowHome: true,
			MaxArgs:   1,
			Usage:     "[target]",
			Desc:      "query the server software version",
		},
		"ADMIN": {
			AllowHome: true,
			MaxArgs:   1,
			Usage:     "[target]",
			Desc:      "query the server administrative information",
		},
		"LUSERS": {
			AllowHome: true,
			Desc:      "query the server user information",
		},
		"TIME": {
			AllowHome: true,
			MaxArgs:   1,
			Usage:     "[target]",
			Desc:      "query the server local time",
		},
		"STATS": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   2,
			Usage:     "<query> [target]",
			Desc:      "query server statistics",
		},
		"INFO": {
			AllowHome: true,
			Desc:      "query server information",
		},
		"REHASH": {
			AllowHome: true,
			Desc:      "make the server reload its configuration",
		},
		"RESTART": {
			AllowHome: true,
			Desc:      "make the server restart",
		},
		"LINKS": {
			AllowHome: true,
			Desc:      "query the servers of the network",
		},
		"WALLOPS": {
			AllowHome: true,
			MinArgs:   1,
			MaxArgs:   1,
			Usage:     "<text>",
			Desc:      "broadcast a message to all users",
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
		buffer, line := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          buffer,
			TargetIsChannel: s.IsChannel(buffer),
			Command:         "PRIVMSG",
			Content:         content,
			Time:            time.Now(),
		})
		app.win.AddLine(netID, buffer, line)
	}

	return nil
}

func commandDoBuffer(app *App, args []string) error {
	name := args[0]
	i, err := strconv.Atoi(name)
	if err == nil {
		if app.win.JumpBufferIndex(i - 1) {
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
		sb.SetStyle(vaxis.Style{
			Attribute: vaxis.AttrBold,
		})
		sb.WriteString(name)
		sb.SetStyle(vaxis.Style{})
		sb.WriteByte(' ')
		sb.WriteString(cmd.Usage)
		app.win.AddLine(netID, buffer, ui.Line{
			At:   t,
			Body: sb.StyledString(),
		})
		app.win.AddLine(netID, buffer, ui.Line{
			At:   t,
			Body: ui.PlainSprintf("  %s", cmd.Desc),
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
		app.win.AddLine(netID, buffer, ui.Line{
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
		app.win.AddLine(netID, buffer, ui.Line{
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
			app.win.AddLine(netID, buffer, ui.Line{
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
		buffer, line := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          buffer,
			TargetIsChannel: s.IsChannel(buffer),
			Command:         "PRIVMSG",
			Content:         content,
			Time:            time.Now(),
		})
		app.win.AddLine(netID, buffer, line)
	}
	return nil
}

func commandDoNP(app *App, args []string) (err error) {
	song, err := getSong()
	if err != nil {
		return fmt.Errorf("failed detecting the song: %v", err)
	}
	if song == "" {
		return fmt.Errorf("no song was detected")
	}
	return commandDoMe(app, []string{fmt.Sprintf("np: %s", song)})
}

func commandDoUpload(app *App, args []string) (err error) {
	if app.cfg.Transient || !app.cfg.LocalIntegrations {
		return fmt.Errorf("usage of UPLOAD is disabled")
	}
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	upload := s.UploadURL()
	if upload == "" {
		return fmt.Errorf("file upload is not supported on this server; try using soju and enabling file upload")
	}
	path := args[0]

	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("opening file: %v", err)
	}
	if fi.Size() > 50*1024*1024 {
		// Best-effort limit, taking from current soju
		return fmt.Errorf("file too large: maximum 50MB per file")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening file: %v", err)
	}

	app.handleUpload(upload, f, fi.Size())
	return nil
}

func commandDoMsg(app *App, args []string) (err error) {
	target := args[0]
	content := args[1]
	return commandSendMessage(app, target, content)
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
	sb.SetStyle(vaxis.Style{
		Foreground: app.cfg.Colors.Status,
	})
	sb.WriteString("Names: ")
	for _, name := range s.Names(buffer) {
		if name.PowerLevel != "" {
			sb.SetStyle(vaxis.Style{
				Foreground: vaxis.IndexColor(2),
			})
			sb.WriteString(name.PowerLevel)
			sb.SetStyle(vaxis.Style{
				Foreground: app.cfg.Colors.Status,
			})
		}
		sb.WriteString(name.Name.Name)
		sb.WriteByte(' ')
	}
	body := sb.StyledString()
	// TODO remove last space
	app.win.AddLine(netID, buffer, ui.Line{
		At:        time.Now(),
		Head:      "--",
		HeadColor: app.cfg.Colors.Status,
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
	_, target := app.win.CurrentBuffer()
	if len(args) > 0 && !strings.HasPrefix(args[0], "+") && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		args = args[1:]
	}
	flags := ""
	if len(args) > 0 {
		flags = args[0]
		args = args[1:]
	}
	modeArgs := args

	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.ChangeMode(target, flags, modeArgs)
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
	if s == nil {
		return errOffline
	}
	target := args[0]
	if s.IsChannel(target) {
		return fmt.Errorf("cannot query a channel, use JOIN instead")
	}
	i, added := app.win.AddBuffer(netID, "", target)
	app.win.JumpBufferIndex(i)
	if len(args) > 1 {
		if err := commandSendMessage(app, target, args[1]); err != nil {
			return err
		}
	}
	if added {
		s.MonitorAdd(target)
		s.ReadGet(target)
		s.NewHistoryRequest(target).WithLimit(200).Latest()
	}
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

func commandDoBouncer(app *App, args []string) (err error) {
	b, err := getBouncerService(app)
	if err != nil {
		return err
	}
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.PrivMsg(b, args[0])
	return nil
}

func commandDoQuote(app *App, args []string) (err error) {
	if app.cfg.Transient {
		return fmt.Errorf("usage of QUOTE is disabled")
	}
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.SendRaw(args[0])
	return nil
}

func commandDoList(app *App, args []string) (err error) {
	if app.cfg.Transient {
		return fmt.Errorf("usage of LIST is disabled")
	}
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	var pattern string
	if len(args) > 0 {
		pattern = args[0]
	}
	s.List(pattern)
	return nil
}

func commandDoR(app *App, args []string) (err error) {
	s := app.sessions[app.lastQueryNet]
	if s == nil {
		return errOffline
	}
	s.PrivMsg(app.lastQuery, args[0])
	if !s.HasCapability("echo-message") {
		buffer, line := app.formatMessage(s, irc.MessageEvent{
			User:            s.Nick(),
			Target:          app.lastQuery,
			TargetIsChannel: s.IsChannel(app.lastQuery),
			Command:         "PRIVMSG",
			Content:         args[0],
			Time:            time.Now(),
		})
		app.win.AddLine(app.lastQueryNet, buffer, line)
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

func commandDoWhois(app *App, args []string) (err error) {
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	var nick string
	if len(args) == 0 {
		if channel == "" || s.IsChannel(channel) {
			return fmt.Errorf("either send this command from a DM, or specify the user")
		}
		nick = channel
	} else {
		nick = args[0]
	}
	s.Whois(nick)
	return nil
}

func commandDoWhowas(app *App, args []string) (err error) {
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	var nick string
	if len(args) == 0 {
		if channel == "" || s.IsChannel(channel) {
			return fmt.Errorf("either send this command from a DM, or specify the user")
		}
		nick = channel
	} else {
		nick = args[0]
	}
	s.Whowas(nick)
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
	// Check whether the argument after the user is a channel, to accept both:
	// - KICK user #chan you are mean
	// - KICK user you are mean
	comment := ""
	if len(args) >= 2 {
		if s.IsChannel(args[1]) {
			channel = args[1]
		} else {
			comment = args[1] + " "
		}
	}
	if channel == "" {
		return fmt.Errorf("either send this command from a channel, or specify the channel")
	}
	if len(args) == 3 {
		comment += args[2]
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

func commandDoSearch(app *App, args []string) (err error) {
	if len(args) == 0 {
		app.win.CloseOverlay()
		return nil
	}
	text := args[0]
	netID, channel := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	if !s.HasCapability("soju.im/search") {
		return errors.New("server does not support searching")
	}
	s.Search(channel, text)
	return nil
}

func commandDoAway(app *App, args []string) (err error) {
	reason := "Away"
	if len(args) > 0 {
		reason = args[0]
	}
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.Away(reason)
	return nil
}

func commandDoBack(app *App, args []string) (err error) {
	s := app.CurrentSession()
	if s == nil {
		return errOffline
	}
	s.Away("")
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
		if n != maxArgsInfinite && na+1 >= n {
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
	if len(s) == 0 || s[0] != '/' {
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

func commandSendMessage(app *App, target string, content string) error {
	netID, _ := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return errOffline
	}
	s.PrivMsg(target, content)
	if !s.HasCapability("echo-message") {
		buffer, line := app.formatMessage(s, irc.MessageEvent{
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
			s.ReadGet(buffer)
			app.win.AddBuffer(netID, "", buffer)
		}

		app.win.AddLine(netID, buffer, line)
	}
	return nil
}

func commandDoShrug(app *App, args []string) (err error) {
	_, buffer := app.win.CurrentBuffer()
	return commandSendMessage(app, buffer, `¯\_(ツ)_/¯`)
}

func commandDoTableFlip(app *App, args []string) (err error) {
	_, buffer := app.win.CurrentBuffer()
	return commandSendMessage(app, buffer, `(╯°□°)╯︵ ┻━┻`)
}

func (app *App) handleInput(buffer, content string) error {
	confirmed := content == app.lastConfirm
	app.lastConfirm = content

	if content == "" {
		return nil
	}

	cmdName, rawArgs, isCommand := parseCommand(content)
	if !isCommand {
		if _, _, command := parseCommand(strings.TrimSpace(content)); !confirmed && command {
			// " /FOO BAR"
			return fmt.Errorf("this message looks like a command; remove the spaces at the start, or press enter again to send the message as is")
		}
		return noCommand(app, rawArgs)
	}
	if cmdName == "" {
		return fmt.Errorf("lone slash at the beginning")
	}
	if strings.HasPrefix("BUFFER", cmdName) {
		cmdName = "BUFFER"
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
		if confirmed {
			if s := app.CurrentSession(); s != nil {
				if rawArgs != "" {
					s.SendRaw(fmt.Sprintf("%s %s", cmdName, rawArgs))
				} else {
					s.SendRaw(cmdName)
				}
				return nil
			} else {
				return errOffline
			}
		} else {
			return fmt.Errorf("the senpai command %q does not exist; press enter again to pass the command as is to the server", cmdName)
		}
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

	if cmd.Handle != nil {
		return cmd.Handle(app, args)
	} else {
		if s := app.CurrentSession(); s != nil {
			if rawArgs != "" {
				s.Send(cmdName, args...)
			} else {
				s.Send(cmdName)
			}
			return nil
		} else {
			return errOffline
		}
	}
}

func getSong() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	info, err := libnp.GetInfo(ctx)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", nil
	}
	if info.Title == "" {
		return "", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "\x02%s\x02", info.Title)
	if len(info.Artists) > 0 {
		fmt.Fprintf(&sb, " by \x02%s\x02", info.Artists[0])
	}
	if info.Album != "" {
		fmt.Fprintf(&sb, " from \x02%s\x02", info.Album)
	}
	if u, err := url.Parse(info.URL); err == nil {
		switch u.Scheme {
		case "http", "https":
			fmt.Fprintf(&sb, " — %s", info.URL)
		}
	}
	return sb.String(), nil
}

func getBouncerService(app *App) (service string, err error) {
	if app.cfg.Transient {
		return "", fmt.Errorf("usage of BOUNCER is disabled")
	}
	s := app.CurrentSession()
	if s == nil {
		return "", errOffline
	}
	b := s.BouncerService()
	if b == "" {
		return "", fmt.Errorf("no bouncer service found on this server; try using soju")
	}
	return b, nil
}
