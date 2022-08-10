package irc

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/time/rate"
)

type SASLClient interface {
	Early() bool
	Handshake() (mech string)
	Respond(challenge string) (res string, err error)
}

type SASLPlain struct {
	Username string
	Password string
}

func (auth *SASLPlain) Early() bool {
	return true
}

func (auth *SASLPlain) Handshake() (mech string) {
	mech = "PLAIN"
	return
}

func (auth *SASLPlain) Respond(challenge string) (res string, err error) {
	if challenge != "+" {
		err = errors.New("unexpected challenge")
		return
	}

	user := []byte(auth.Username)
	pass := []byte(auth.Password)
	payload := bytes.Join([][]byte{user, user, pass}, []byte{0})
	res = base64.StdEncoding.EncodeToString(payload)

	return
}

// SupportedCapabilities is the set of capabilities supported by this library.
var SupportedCapabilities = map[string]struct{}{
	"away-notify":   {},
	"batch":         {},
	"cap-notify":    {},
	"echo-message":  {},
	"invite-notify": {},
	"message-tags":  {},
	"multi-prefix":  {},
	"server-time":   {},
	"sasl":          {},
	"setname":       {},

	"draft/chathistory":               {},
	"draft/event-playback":            {},
	"draft/read-marker":               {},
	"soju.im/bouncer-networks-notify": {},
	"soju.im/bouncer-networks":        {},
	"soju.im/search":                  {},
}

// Values taken by the "@+typing=" client tag.  TypingUnspec means the value or
// tag is absent.
const (
	TypingUnspec = iota
	TypingActive
	TypingPaused
	TypingDone
)

// User is a known IRC user.
type User struct {
	Name         *Prefix // the nick, user and hostname of the user if known.
	Away         bool    // whether the user is away or not
	Disconnected bool    // can only be true for monitored users.
}

// Channel is a joined channel.
type Channel struct {
	Name      string           // the name of the channel.
	Members   map[*User]string // the set of members associated with their membership.
	Topic     string           // the topic of the channel, or "" if absent.
	TopicWho  *Prefix          // the name of the last user who set the topic.
	TopicTime time.Time        // the last time the topic has been changed.

	complete bool // whether this structure is fully initialized.
}

// SessionParams defines how to connect to an IRC server.
type SessionParams struct {
	Nickname string
	Username string
	RealName string
	NetID    string
	Auth     SASLClient
}

type Session struct {
	out          chan<- Message
	closed       bool
	registered   bool
	typings      *Typings               // incoming typing notifications.
	typingStamps map[string]typingStamp // user typing instants.

	nick   string
	nickCf string // casemapped nickname.
	user   string
	real   string
	acct   string
	host   string
	netID  string
	auth   SASLClient

	availableCaps map[string]string
	enabledCaps   map[string]struct{}

	// ISUPPORT features
	casemap       func(string) string
	chanmodes     [4]string
	chantypes     string
	linelen       int
	historyLimit  int
	prefixSymbols string
	prefixModes   string
	monitor       bool

	users          map[string]*User        // known users.
	channels       map[string]Channel      // joined channels.
	chBatches      map[string]HistoryEvent // channel history batches being processed.
	chReqs         map[string]struct{}     // set of targets for which history is currently requested.
	targetsBatchID string                  // ID of the channel history targets batch being processed.
	targetsBatch   HistoryTargetsEvent     // channel history targets batch being processed.
	searchBatchID  string                  // ID of the search targets batch being processed.
	searchBatch    SearchEvent             // search batch being processed.
	monitors       map[string]struct{}     // set of users we want to monitor (and keep even if they are disconnected).

	pendingChannels map[string]time.Time // set of join requests stamps for channels.
}

func NewSession(out chan<- Message, params SessionParams) *Session {
	s := &Session{
		out:             out,
		typings:         NewTypings(),
		typingStamps:    map[string]typingStamp{},
		nick:            params.Nickname,
		nickCf:          CasemapASCII(params.Nickname),
		user:            params.Username,
		real:            params.RealName,
		netID:           params.NetID,
		auth:            params.Auth,
		availableCaps:   map[string]string{},
		enabledCaps:     map[string]struct{}{},
		casemap:         CasemapRFC1459,
		chantypes:       "#&",
		linelen:         512,
		historyLimit:    100,
		prefixSymbols:   "@+",
		prefixModes:     "ov",
		users:           map[string]*User{},
		channels:        map[string]Channel{},
		chBatches:       map[string]HistoryEvent{},
		chReqs:          map[string]struct{}{},
		monitors:        map[string]struct{}{},
		pendingChannels: map[string]time.Time{},
	}

	s.out <- NewMessage("CAP", "LS", "302")
	for capability := range SupportedCapabilities {
		s.out <- NewMessage("CAP", "REQ", capability)
	}
	s.out <- NewMessage("NICK", s.nick)
	s.out <- NewMessage("USER", s.user, "0", "*", s.real)
	if s.auth != nil && s.auth.Early() {
		h := s.auth.Handshake()
		s.out <- NewMessage("AUTHENTICATE", h)
		res, err := s.auth.Respond("+")
		if err != nil {
			s.out <- NewMessage("AUTHENTICATE", "*")
		} else {
			s.out <- NewMessage("AUTHENTICATE", res)
		}
		s.auth = nil
	}

	if s.auth == nil {
		s.endRegistration()
	}

	return s
}

func (s *Session) Close() {
	if s.closed {
		return
	}
	s.closed = true
	s.typings.Close()
	close(s.out)
}

// HasCapability reports whether the given capability has been negotiated
// successfully.
func (s *Session) HasCapability(capability string) bool {
	_, ok := s.enabledCaps[capability]
	return ok
}

func (s *Session) Nick() string {
	return s.nick
}

func (s *Session) NetID() string {
	return s.netID
}

// NickCf is our casemapped nickname.
func (s *Session) NickCf() string {
	return s.nickCf
}

func (s *Session) IsMe(nick string) bool {
	return s.nickCf == s.casemap(nick)
}

func (s *Session) IsChannel(name string) bool {
	return strings.IndexAny(name, s.chantypes) == 0
}

func (s *Session) Casemap(name string) string {
	return s.casemap(name)
}

// Users returns the list of all known nicknames.
func (s *Session) Users() []string {
	users := make([]string, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u.Name.Name)
	}
	return users
}

// Names returns the list of users in the given target, or nil if the target
// is not a known channel or nick in the session.
// The list is sorted according to member name.
func (s *Session) Names(target string) []Member {
	var names []Member
	if s.IsChannel(target) {
		if c, ok := s.channels[s.Casemap(target)]; ok {
			names = make([]Member, 0, len(c.Members))
			for u, pl := range c.Members {
				names = append(names, Member{
					PowerLevel:   pl,
					Name:         u.Name.Copy(),
					Away:         u.Away,
					Disconnected: u.Disconnected,
				})
			}
		}
	} else if u, ok := s.users[s.Casemap(target)]; ok {
		names = append(names, Member{
			Name:         u.Name.Copy(),
			Away:         u.Away,
			Disconnected: u.Disconnected,
		})
		names = append(names, Member{
			Name: &Prefix{
				Name: s.nick,
			},
		})
	}
	sort.Sort(members(names))
	return names
}

// Typings returns the list of nickname who are currently typing.
func (s *Session) Typings(target string) []string {
	targetCf := s.casemap(target)
	res := s.typings.List(targetCf)
	for i := 0; i < len(res); i++ {
		if s.IsMe(res[i]) {
			res = append(res[:i], res[i+1:]...)
			i--
		} else if u, ok := s.users[res[i]]; ok {
			res[i] = u.Name.Name
		}
	}
	sort.Strings(res)
	return res
}

func (s *Session) TypingStops() <-chan Typing {
	return s.typings.Stops()
}

func (s *Session) ChannelsSharedWith(name string) []string {
	var user *User
	if u, ok := s.users[s.Casemap(name)]; ok && !u.Disconnected {
		user = u
	} else {
		return nil
	}
	var channels []string
	for _, c := range s.channels {
		if _, ok := c.Members[user]; ok {
			channels = append(channels, c.Name)
		}
	}
	return channels
}

func (s *Session) Topic(channel string) (topic string, who *Prefix, at time.Time) {
	channelCf := s.Casemap(channel)
	if c, ok := s.channels[channelCf]; ok {
		topic = c.Topic
		who = c.TopicWho
		at = c.TopicTime
	}
	return
}

func (s *Session) SendRaw(raw string) {
	s.out <- NewMessage(raw)
}

func (s *Session) Join(channel, key string) {
	channelCf := s.Casemap(channel)
	s.pendingChannels[channelCf] = time.Now()
	if key == "" {
		s.out <- NewMessage("JOIN", channel)
	} else {
		s.out <- NewMessage("JOIN", channel, key)
	}
}

func (s *Session) Part(channel, reason string) {
	s.out <- NewMessage("PART", channel, reason)
}

func (s *Session) ChangeTopic(channel, topic string) {
	s.out <- NewMessage("TOPIC", channel, topic)
}

func (s *Session) Quit(reason string) {
	s.out <- NewMessage("QUIT", reason)
}

func (s *Session) ChangeNick(nick string) {
	s.out <- NewMessage("NICK", nick)
}

func (s *Session) ChangeMode(channel, flags string, args []string) {
	if flags != "" {
		args = append([]string{channel, flags}, args...)
	} else {
		args = append([]string{channel}, args...)
	}
	s.out <- NewMessage("MODE", args...)
}

func (s *Session) Search(target, text string) {
	if _, ok := s.enabledCaps["soju.im/search"]; !ok {
		return
	}
	attrs := make(map[string]string)
	attrs["text"] = text
	if target != "" {
		attrs["in"] = target
	}
	s.out <- NewMessage("SEARCH", formatTags(attrs))
}

func splitChunks(s string, chunkLen int) (chunks []string) {
	if chunkLen <= 0 {
		return []string{s}
	}
	for chunkLen < len(s) {
		i := chunkLen
		min := chunkLen - utf8.UTFMax
		for min <= i && !utf8.RuneStart(s[i]) {
			i--
		}
		chunks = append(chunks, s[:i])
		s = s[i:]
	}
	if len(s) != 0 {
		chunks = append(chunks, s)
	}
	return
}

func (s *Session) PrivMsg(target, content string) {
	hostLen := len(s.host)
	if hostLen == 0 {
		hostLen = len("255.255.255.255")
	}
	maxMessageLen := s.linelen -
		len(":!@ PRIVMSG  :\r\n") -
		len(s.nick) -
		len(s.user) -
		hostLen -
		len(target)
	chunks := splitChunks(content, maxMessageLen)
	for _, chunk := range chunks {
		s.out <- NewMessage("PRIVMSG", target, chunk)
	}
	targetCf := s.Casemap(target)
	delete(s.typingStamps, targetCf)
}

func (s *Session) Typing(target string) {
	if !s.HasCapability("message-tags") {
		return
	}
	targetCf := s.casemap(target)
	now := time.Now()
	t, ok := s.typingStamps[targetCf]
	if ok && ((t.Type == TypingActive && now.Sub(t.Last).Seconds() < 3.0) || !t.Limit.Allow()) {
		return
	}
	if !ok {
		t.Limit = rate.NewLimiter(rate.Limit(1.0/3.0), 5)
		t.Limit.Reserve() // will always be OK
	}
	s.typingStamps[targetCf] = typingStamp{
		Last:  now,
		Type:  TypingActive,
		Limit: t.Limit,
	}
	s.out <- NewMessage("TAGMSG", target).WithTag("+typing", "active")
}

func (s *Session) TypingStop(target string) {
	if !s.HasCapability("message-tags") {
		return
	}
	targetCf := s.casemap(target)
	now := time.Now()
	t, ok := s.typingStamps[targetCf]
	if ok && (t.Type == TypingDone || !t.Limit.Allow()) {
		// don't send a +typing=done again if the last typing we sent was a +typing=done
		return
	}
	if !ok {
		t.Limit = rate.NewLimiter(rate.Limit(1), 5)
		t.Limit.Reserve() // will always be OK
	}
	s.typingStamps[targetCf] = typingStamp{
		Last:  now,
		Type:  TypingDone,
		Limit: t.Limit,
	}
	s.out <- NewMessage("TAGMSG", target).WithTag("+typing", "done")
}

func (s *Session) ReadGet(target string) {
	if _, ok := s.enabledCaps["draft/read-marker"]; ok {
		s.out <- NewMessage("MARKREAD", target)
	}
}

func (s *Session) ReadSet(target string, timestamp time.Time) {
	if _, ok := s.enabledCaps["draft/read-marker"]; ok {
		s.out <- NewMessage("MARKREAD", target, formatTimestamp(timestamp))
	}
}

func (s *Session) MonitorAdd(target string) {
	targetCf := s.casemap(target)
	if _, ok := s.monitors[targetCf]; !ok {
		s.monitors[targetCf] = struct{}{}
		if s.monitor {
			s.out <- NewMessage("MONITOR", "+", target)
		}
	}
}

func (s *Session) MonitorRemove(target string) {
	targetCf := s.casemap(target)
	if _, ok := s.monitors[targetCf]; ok {
		delete(s.monitors, targetCf)
		if s.monitor {
			s.out <- NewMessage("MONITOR", "-", target)
		}
	}
}

type HistoryRequest struct {
	s       *Session
	target  string
	command string
	bounds  []string
	limit   int
}

func formatTimestamp(t time.Time) string {
	return fmt.Sprintf("timestamp=%04d-%02d-%02dT%02d:%02d:%02d.%03dZ",
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e6)
}

func (r *HistoryRequest) WithLimit(limit int) *HistoryRequest {
	if limit < r.s.historyLimit {
		r.limit = limit
	} else {
		r.limit = r.s.historyLimit
	}
	return r
}

func (r *HistoryRequest) doRequest() {
	if !r.s.HasCapability("draft/chathistory") {
		return
	}

	targetCf := r.s.casemap(r.target)
	if _, ok := r.s.chReqs[targetCf]; ok {
		return
	}
	r.s.chReqs[targetCf] = struct{}{}

	args := make([]string, 0, len(r.bounds)+3)
	args = append(args, r.command)
	if r.target != "" {
		args = append(args, r.target)
	}
	args = append(args, r.bounds...)
	args = append(args, strconv.Itoa(r.limit))
	r.s.out <- NewMessage("CHATHISTORY", args...)
}

func (r *HistoryRequest) After(t time.Time) {
	r.command = "AFTER"
	r.bounds = []string{formatTimestamp(t)}
	r.doRequest()
}

func (r *HistoryRequest) Before(t time.Time) {
	r.command = "BEFORE"
	r.bounds = []string{formatTimestamp(t)}
	r.doRequest()
}

func (r *HistoryRequest) Targets(start time.Time, end time.Time) {
	r.command = "TARGETS"
	r.bounds = []string{formatTimestamp(start), formatTimestamp(end)}
	r.target = ""
	r.doRequest()
}

func (s *Session) NewHistoryRequest(target string) *HistoryRequest {
	return &HistoryRequest{
		s:      s,
		target: target,
		limit:  s.historyLimit,
	}
}

func (s *Session) Whois(nick string) {
	s.out <- NewMessage("WHOIS", nick)
}

func (s *Session) Invite(nick, channel string) {
	s.out <- NewMessage("INVITE", nick, channel)
}

func (s *Session) Kick(nick, channel, comment string) {
	if comment == "" {
		s.out <- NewMessage("KICK", channel, nick)
	} else {
		s.out <- NewMessage("KICK", channel, nick, comment)
	}
}

func (s *Session) HandleMessage(msg Message) (Event, error) {
	if s.registered {
		return s.handleRegistered(msg)
	} else {
		return s.handleUnregistered(msg)
	}
}

func (s *Session) handleUnregistered(msg Message) (Event, error) {
	switch msg.Command {
	case errNicknameinuse:
		var nick string
		if err := msg.ParseParams(nil, &nick); err != nil {
			return nil, err
		}

		s.out <- NewMessage("NICK", nick+"_")
	case rplSaslsuccess:
		if s.auth != nil {
			s.endRegistration()
		}
	default:
		return s.handleRegistered(msg)
	}
	return nil, nil
}

func (s *Session) handleRegistered(msg Message) (Event, error) {
	if id, ok := msg.Tags["batch"]; ok {
		if id == s.targetsBatchID {
			var target, timestamp string
			if err := msg.ParseParams(nil, &target, &timestamp); err != nil {
				return nil, err
			}
			t, ok := parseTimestamp(timestamp)
			if !ok {
				return nil, nil
			}
			s.targetsBatch.Targets[target] = t
		} else if id == s.searchBatchID {
			ev, err := s.handleMessageRegistered(msg, true)
			if err != nil {
				return nil, err
			}
			if ev, ok := ev.(MessageEvent); ok {
				s.searchBatch.Messages = append(s.searchBatch.Messages, ev)
				return nil, nil
			}
		} else if b, ok := s.chBatches[id]; ok {
			ev, err := s.handleMessageRegistered(msg, true)
			if err != nil {
				return nil, err
			}
			if ev != nil {
				s.chBatches[id] = HistoryEvent{
					Target:   b.Target,
					Messages: append(b.Messages, ev),
				}
				return nil, nil
			}
		}
	}
	return s.handleMessageRegistered(msg, false)
}

func (s *Session) handleMessageRegistered(msg Message, playback bool) (Event, error) {
	switch msg.Command {
	case "AUTHENTICATE":
		if s.auth == nil {
			break
		}

		var payload string
		if err := msg.ParseParams(&payload); err != nil {
			return nil, err
		}

		res, err := s.auth.Respond(payload)
		if err != nil {
			s.out <- NewMessage("AUTHENTICATE", "*")
		} else {
			s.out <- NewMessage("AUTHENTICATE", res)
		}
	case rplLoggedin:
		var nuh string
		if err := msg.ParseParams(nil, &nuh, &s.acct); err != nil {
			return nil, err
		}

		prefix := ParsePrefix(nuh)
		s.user = prefix.User
		s.host = prefix.Host
	case errNicklocked, errSaslfail, errSasltoolong, errSaslaborted, errSaslalready, rplSaslmechs:
		if s.auth != nil {
			s.endRegistration()
		}
		return ErrorEvent{
			Severity: SeverityFail,
			Code:     msg.Command,
			Message:  fmt.Sprintf("Registration failed: %s", strings.Join(msg.Params[1:], " ")),
		}, nil
	case rplWelcome:
		if err := msg.ParseParams(&s.nick); err != nil {
			return nil, err
		}

		s.nickCf = s.Casemap(s.nick)
		s.registered = true
		s.users[s.nickCf] = &User{Name: &Prefix{
			Name: s.nick, User: s.user, Host: s.host,
		}}
		if s.host == "" {
			s.out <- NewMessage("WHO", s.nick)
		}
	case rplIsupport:
		if len(msg.Params) < 3 {
			return nil, msg.errNotEnoughParams(3)
		}
		s.updateFeatures(msg.Params[1 : len(msg.Params)-1])
		return RegisteredEvent{}, nil
	case rplWhoreply:
		var nick, host, flags, username string
		if err := msg.ParseParams(nil, nil, &username, &host, nil, &nick, &flags, nil); err != nil {
			return nil, err
		}

		nickCf := s.Casemap(nick)
		away := flags[0] == 'G' // flags is not empty because it's not the trailing parameter

		if s.nickCf == nickCf {
			s.user = username
			s.host = host
		}

		if u, ok := s.users[nickCf]; ok {
			u.Away = away
		}
	case rplEndofwho:
		// do nothing
	case "CAP":
		var subcommand, caps string
		if err := msg.ParseParams(nil, &subcommand, &caps); err != nil {
			return nil, err
		}

		switch subcommand {
		case "ACK":
			for _, c := range ParseCaps(caps) {
				if c.Enable {
					s.enabledCaps[c.Name] = struct{}{}
				} else {
					delete(s.enabledCaps, c.Name)
				}

				if s.auth != nil && c.Name == "sasl" {
					h := s.auth.Handshake()
					s.out <- NewMessage("AUTHENTICATE", h)
				} else if len(s.channels) != 0 && c.Name == "multi-prefix" {
					// TODO merge NAMES commands
					for channel := range s.channels {
						s.out <- NewMessage("NAMES", channel)
					}
				}
			}
		case "NAK":
			// do nothing
		case "NEW":
			for _, c := range ParseCaps(caps) {
				s.availableCaps[c.Name] = c.Value
				if _, ok := SupportedCapabilities[c.Name]; !ok {
					continue
				}
				if _, ok := s.enabledCaps[c.Name]; ok {
					continue
				}
				s.out <- NewMessage("CAP", "REQ", c.Name)
			}
		case "DEL":
			for _, c := range ParseCaps(caps) {
				delete(s.availableCaps, c.Name)
				delete(s.enabledCaps, c.Name)
			}
		}
	case "JOIN":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var channel string
		if err := msg.ParseParams(&channel); err != nil {
			return nil, err
		}

		if playback {
			return UserJoinEvent{
				User:    msg.Prefix.Name,
				Channel: channel,
				Time:    msg.TimeOrNow(),
			}, nil
		}

		nickCf := s.Casemap(msg.Prefix.Name)
		channelCf := s.Casemap(channel)

		if s.IsMe(nickCf) {
			s.channels[channelCf] = Channel{
				Name:    msg.Params[0],
				Members: map[*User]string{},
			}
			if _, ok := s.enabledCaps["away-notify"]; ok {
				// Only try to know who is away if the list is
				// updated by the server via away-notify.
				// Otherwise, it'll become outdated over time.
				s.out <- NewMessage("WHO", channel)
			}
		} else if c, ok := s.channels[channelCf]; ok {
			if _, ok := s.users[nickCf]; !ok {
				s.users[nickCf] = &User{Name: msg.Prefix.Copy()}
			}
			c.Members[s.users[nickCf]] = ""
			return UserJoinEvent{
				User:    msg.Prefix.Name,
				Channel: c.Name,
				Time:    msg.TimeOrNow(),
			}, nil
		}
	case "PART":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var channel string
		if err := msg.ParseParams(&channel); err != nil {
			return nil, err
		}

		if playback {
			return UserPartEvent{
				User:    msg.Prefix.Name,
				Channel: channel,
				Time:    msg.TimeOrNow(),
			}, nil
		}

		nickCf := s.Casemap(msg.Prefix.Name)
		channelCf := s.Casemap(channel)

		if s.IsMe(nickCf) {
			if c, ok := s.channels[channelCf]; ok {
				delete(s.channels, channelCf)
				for u := range c.Members {
					s.cleanUser(u)
				}
				return SelfPartEvent{
					Channel: c.Name,
				}, nil
			}
		} else if c, ok := s.channels[channelCf]; ok {
			if u, ok := s.users[nickCf]; ok {
				delete(c.Members, u)
				s.cleanUser(u)
				s.typings.Done(channelCf, nickCf)
				return UserPartEvent{
					User:    u.Name.Name,
					Channel: c.Name,
					Time:    msg.TimeOrNow(),
				}, nil
			}
		}
	case "KICK":
		var channel, nick string
		if err := msg.ParseParams(&channel, &nick); err != nil {
			return nil, err
		}

		if playback {
			return UserPartEvent{
				User:    nick,
				Channel: channel,
				Time:    msg.TimeOrNow(),
			}, nil
		}

		nickCf := s.Casemap(nick)
		channelCf := s.Casemap(channel)

		if s.IsMe(nickCf) {
			if c, ok := s.channels[channelCf]; ok {
				delete(s.channels, channelCf)
				for u := range c.Members {
					s.cleanUser(u)
				}
				return SelfPartEvent{
					Channel: c.Name,
				}, nil
			}
		} else if c, ok := s.channels[channelCf]; ok {
			if u, ok := s.users[nickCf]; ok {
				delete(c.Members, u)
				s.cleanUser(u)
				s.typings.Done(channelCf, nickCf)
				return UserPartEvent{
					User:    nick,
					Channel: c.Name,
					Time:    msg.TimeOrNow(),
				}, nil
			}
		}
	case "QUIT":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		if playback {
			return UserQuitEvent{
				User: msg.Prefix.Name,
				Time: msg.TimeOrNow(),
			}, nil
		}

		nickCf := s.Casemap(msg.Prefix.Name)

		if u, ok := s.users[nickCf]; ok {
			u.Disconnected = true
			var channels []string
			for channelCf, c := range s.channels {
				if _, ok := c.Members[u]; ok {
					channels = append(channels, c.Name)
					delete(c.Members, u)
					s.cleanUser(u)
					s.typings.Done(channelCf, nickCf)
				}
			}
			return UserQuitEvent{
				User:     u.Name.Name,
				Channels: channels,
				Time:     msg.TimeOrNow(),
			}, nil
		}
	case rplMononline:
		for _, target := range strings.Split(msg.Params[1], ",") {
			prefix := ParsePrefix(target)
			if prefix == nil {
				continue
			}
			nickCf := s.casemap(prefix.Name)

			if _, ok := s.monitors[nickCf]; ok {
				u, ok := s.users[nickCf]
				if !ok {
					u = &User{
						Name: prefix,
					}
					s.users[nickCf] = u
				}
				if u.Disconnected {
					u.Disconnected = false
					return UserOnlineEvent{
						User: u.Name.Name,
					}, nil
				}
			}
		}
	case rplMonoffline:
		for _, target := range strings.Split(msg.Params[1], ",") {
			prefix := ParsePrefix(target)
			if prefix == nil {
				continue
			}
			nickCf := s.casemap(prefix.Name)

			if _, ok := s.monitors[nickCf]; ok {
				u, ok := s.users[nickCf]
				if !ok {
					u = &User{
						Name: prefix,
					}
					s.users[nickCf] = u
				}
				if !u.Disconnected {
					u.Disconnected = true
					return UserOfflineEvent{
						User: u.Name.Name,
					}, nil
				}
			}
		}
	case rplNamreply:
		var channel, names string
		if err := msg.ParseParams(nil, nil, &channel, &names); err != nil {
			return nil, err
		}

		channelCf := s.Casemap(channel)

		if c, ok := s.channels[channelCf]; ok {

			for _, name := range ParseNameReply(names, s.prefixSymbols) {
				nickCf := s.Casemap(name.Name.Name)

				if _, ok := s.users[nickCf]; !ok {
					s.users[nickCf] = &User{Name: name.Name.Copy()}
				}
				c.Members[s.users[nickCf]] = name.PowerLevel
			}

			s.channels[channelCf] = c
		}
	case rplEndofnames:
		var channel string
		if err := msg.ParseParams(nil, &channel); err != nil {
			return nil, err
		}

		channelCf := s.Casemap(channel)

		if c, ok := s.channels[channelCf]; ok && !c.complete {
			c.complete = true
			s.channels[channelCf] = c
			ev := SelfJoinEvent{
				Channel: c.Name,
				Topic:   c.Topic,
			}
			if stamp, ok := s.pendingChannels[channelCf]; ok && time.Since(stamp) < 5*time.Second {
				ev.Requested = true
			}
			return ev, nil
		}
	case rplTopic:
		var channel, topic string
		if err := msg.ParseParams(nil, &channel, &topic); err != nil {
			return nil, err
		}

		channelCf := s.Casemap(channel)

		if c, ok := s.channels[channelCf]; ok {
			c.Topic = topic
			s.channels[channelCf] = c
		}
	case rplTopicwhotime:
		var channel, topicWho, topicTime string
		if err := msg.ParseParams(nil, &channel, &topicWho, &topicTime); err != nil {
			return nil, err
		}

		channelCf := s.Casemap(channel)

		// ignore the error, we still have topicWho
		t, _ := strconv.ParseInt(topicTime, 10, 64)

		if c, ok := s.channels[channelCf]; ok {
			c.TopicWho = ParsePrefix(topicWho)
			c.TopicTime = time.Unix(t, 0)
			s.channels[channelCf] = c
		}
	case rplNotopic:
		var channel string
		if err := msg.ParseParams(nil, &channel); err != nil {
			return nil, err
		}

		channelCf := s.Casemap(channel)

		if c, ok := s.channels[channelCf]; ok {
			c.Topic = ""
			s.channels[channelCf] = c
		}
	case "TOPIC":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var channel, topic string
		if err := msg.ParseParams(&channel, &topic); err != nil {
			return nil, err
		}

		if playback {
			return TopicChangeEvent{
				Channel: channel,
				Topic:   topic,
				Time:    msg.TimeOrNow(),
			}, nil
		}

		channelCf := s.Casemap(channel)

		if c, ok := s.channels[channelCf]; ok {
			c.Topic = topic
			c.TopicWho = msg.Prefix.Copy()
			c.TopicTime = msg.TimeOrNow()
			s.channels[channelCf] = c
			return TopicChangeEvent{
				Channel: c.Name,
				Topic:   c.Topic,
				Time:    msg.TimeOrNow(),
			}, nil
		}
	case "MODE":
		var channel string
		if err := msg.ParseParams(&channel, nil); err != nil {
			return nil, err
		}
		mode := strings.Join(msg.Params[1:], " ")

		if playback {
			return ModeChangeEvent{
				Channel: channel,
				Mode:    mode,
				Time:    msg.TimeOrNow(),
			}, nil
		}

		channelCf := s.Casemap(channel)

		if c, ok := s.channels[channelCf]; ok {
			modeChanges, err := ParseChannelMode(msg.Params[1], msg.Params[2:], s.chanmodes, s.prefixModes)
			if err != nil {
				return nil, err
			}
			for _, change := range modeChanges {
				i := strings.IndexByte(s.prefixModes, change.Mode)
				if i < 0 {
					continue
				}
				nickCf := s.Casemap(change.Param)
				user := s.users[nickCf]
				membership, ok := c.Members[user]
				if !ok {
					continue
				}
				var newMembership []byte
				if change.Enable {
					newMembership = append([]byte(membership), s.prefixSymbols[i])
					sort.Slice(newMembership, func(i, j int) bool {
						i = strings.IndexByte(s.prefixSymbols, newMembership[i])
						j = strings.IndexByte(s.prefixSymbols, newMembership[j])
						return i < j
					})
				} else if j := strings.IndexByte(membership, s.prefixSymbols[i]); j >= 0 {
					newMembership = []byte(membership)
					newMembership = append(newMembership[:j], newMembership[j+1:]...)
				}
				c.Members[user] = string(newMembership)
			}
			s.channels[channelCf] = c
			return ModeChangeEvent{
				Channel: c.Name,
				Mode:    mode,
				Time:    msg.TimeOrNow(),
			}, nil
		}
	case "INVITE":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var nick, channel string
		if err := msg.ParseParams(&nick, &channel); err != nil {
			return nil, err
		}

		return InviteEvent{
			Inviter: msg.Prefix.Name,
			Invitee: nick,
			Channel: channel,
		}, nil
	case rplInviting:
		var nick, channel string
		if err := msg.ParseParams(nil, &nick, &channel); err != nil {
			return nil, err
		}

		return InviteEvent{
			Inviter: s.nick,
			Invitee: nick,
			Channel: channel,
		}, nil
	case "AWAY":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		nickCf := s.Casemap(msg.Prefix.Name)

		if u, ok := s.users[nickCf]; ok {
			u.Away = len(msg.Params) == 1
		}
	case "PRIVMSG", "NOTICE":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var target string
		if err := msg.ParseParams(&target); err != nil {
			return nil, err
		}

		if playback {
			return s.newMessageEvent(msg)
		}

		targetCf := s.casemap(target)
		nickCf := s.casemap(msg.Prefix.Name)
		s.typings.Done(targetCf, nickCf)

		return s.newMessageEvent(msg)
	case "TAGMSG":
		if playback {
			return nil, nil
		}

		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var target string
		if err := msg.ParseParams(&target); err != nil {
			return nil, err
		}

		targetCf := s.casemap(target)
		nickCf := s.casemap(msg.Prefix.Name)

		if s.IsMe(msg.Prefix.Name) {
			// TAGMSG from self
			break
		}

		if t, ok := msg.Tags["+typing"]; ok {
			switch t {
			case "active":
				s.typings.Active(targetCf, nickCf)
			case "paused", "done":
				s.typings.Done(targetCf, nickCf)
			}
		}
	case "BATCH":
		var id string
		if err := msg.ParseParams(&id); err != nil {
			return nil, err
		}
		if len(id) == 0 {
			return nil, fmt.Errorf("empty batch id")
		}

		batchStart := id[0] == '+'
		id = id[1:]

		if batchStart {
			var name string
			if err := msg.ParseParams(nil, &name); err != nil {
				return nil, err
			}

			switch name {
			case "chathistory":
				var target string
				if err := msg.ParseParams(nil, nil, &target); err != nil {
					return nil, err
				}

				s.chBatches[id] = HistoryEvent{Target: target}
			case "draft/chathistory-targets":
				s.targetsBatchID = id
				s.targetsBatch = HistoryTargetsEvent{Targets: make(map[string]time.Time)}
			case "soju.im/search":
				s.searchBatchID = id
				s.searchBatch = SearchEvent{}
			}
		} else {
			if b, ok := s.chBatches[id]; ok {
				delete(s.chBatches, id)
				delete(s.chReqs, s.Casemap(b.Target))
				return b, nil
			} else if s.targetsBatchID == id {
				s.targetsBatchID = ""
				delete(s.chReqs, "")
				return s.targetsBatch, nil
			} else if s.searchBatchID == id {
				s.searchBatchID = ""
				return s.searchBatch, nil
			}
		}
	case "NICK":
		if msg.Prefix == nil {
			return nil, errMissingPrefix
		}

		var nick string
		if err := msg.ParseParams(&nick); err != nil {
			return nil, err
		}

		if playback {
			return UserNickEvent{
				User:       nick,
				FormerNick: msg.Prefix.Name,
				Time:       msg.TimeOrNow(),
			}, nil
		}

		nickCf := s.Casemap(msg.Prefix.Name)
		newNick := nick
		newNickCf := s.Casemap(newNick)

		if formerUser, ok := s.users[nickCf]; ok {
			formerUser.Name.Name = newNick
			delete(s.users, nickCf)
			s.users[newNickCf] = formerUser
		} else {
			break
		}

		if s.IsMe(msg.Prefix.Name) {
			s.nick = newNick
			s.nickCf = newNickCf
			return SelfNickEvent{
				FormerNick: msg.Prefix.Name,
			}, nil
		} else {
			return UserNickEvent{
				User:       nick,
				FormerNick: msg.Prefix.Name,
				Time:       msg.TimeOrNow(),
			}, nil
		}
	case "MARKREAD":
		if len(msg.Params) < 2 {
			break
		}
		var target, timestamp string
		if err := msg.ParseParams(&target, &timestamp); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(timestamp, "timestamp=") {
			return nil, nil
		}
		timestamp = strings.TrimPrefix(timestamp, "timestamp=")
		t, ok := parseTimestamp(timestamp)
		if !ok {
			return nil, nil
		}
		return ReadEvent{
			Target:    target,
			Timestamp: t,
		}, nil
	case "BOUNCER":
		if len(msg.Params) < 3 {
			break
		}
		if msg.Params[0] != "NETWORK" || s.netID != "" {
			break
		}
		id := msg.Params[1]
		event := BouncerNetworkEvent{
			ID: id,
		}
		if msg.Params[2] != "*" {
			attrs := parseTags(msg.Params[2])
			event.Name = attrs["name"]
		} else {
			event.Delete = true
		}
		return event, nil
	case "PING":
		var payload string
		if err := msg.ParseParams(&payload); err != nil {
			return nil, err
		}

		s.out <- NewMessage("PONG", payload)
	case "ERROR":
		s.Close()
	case "FAIL", "WARN", "NOTE":
		var severity Severity
		var code string
		if err := msg.ParseParams(nil, &code); err != nil {
			return nil, err
		}

		switch msg.Command {
		case "FAIL":
			severity = SeverityFail
		case "WARN":
			severity = SeverityWarn
		case "NOTE":
			severity = SeverityNote
		}

		return ErrorEvent{
			Severity: severity,
			Code:     code,
			Message:  strings.Join(msg.Params[2:], " "),
		}, nil
	case errMonlistisfull:
		// silence monlist full error, we don't care because we do it best-effort
	case rplAway:
		// we display user away status, we don't care about automatic AWAY replies
	default:
		if msg.IsReply() {
			if len(msg.Params) < 2 {
				return nil, msg.errNotEnoughParams(2)
			}
			if msg.Command == errUnknowncommand && msg.Params[1] == "BOUNCER" {
				// ignore any error in response to unconditional BOUNCER LISTNETWORKS
				return nil, nil
			}
			return ErrorEvent{
				Severity: ReplySeverity(msg.Command),
				Code:     msg.Command,
				Message:  strings.Join(msg.Params[1:], " "),
			}, nil
		}
	}
	return nil, nil
}

func (s *Session) newMessageEvent(msg Message) (ev MessageEvent, err error) {
	if msg.Prefix == nil {
		return ev, errMissingPrefix
	}

	var target, content string
	if err := msg.ParseParams(&target, &content); err != nil {
		return ev, err
	}

	ev = MessageEvent{
		User:    msg.Prefix.Name, // TODO correctly casemap
		Target:  target,          // TODO correctly casemap
		Command: msg.Command,
		Content: content,
		Time:    msg.TimeOrNow(),
	}

	if s.IsMe(target) {
		if context := msg.Tags["+draft/channel-context"]; context != "" {
			target = context
		}
	}
	targetCf := s.Casemap(target)
	if c, ok := s.channels[targetCf]; ok {
		ev.Target = c.Name
		ev.TargetIsChannel = true
	}

	return ev, nil
}

func (s *Session) cleanUser(parted *User) {
	nameCf := s.Casemap(parted.Name.Name)
	if _, ok := s.monitors[nameCf]; ok {
		return
	}
	for _, c := range s.channels {
		if _, ok := c.Members[parted]; ok {
			return
		}
	}
	delete(s.users, nameCf)
}

func (s *Session) updateFeatures(features []string) {
	for _, f := range features {
		if f == "" || f == "-" || f == "=" || f == "-=" {
			continue
		}

		var (
			add   bool
			key   string
			value string
		)

		if strings.HasPrefix(f, "-") {
			add = false
			f = f[1:]
		} else {
			add = true
		}

		kv := strings.SplitN(f, "=", 2)
		key = strings.ToUpper(kv[0])
		if len(kv) > 1 {
			value = kv[1]
		}

		if !add {
			// TODO support ISUPPORT negations
			continue
		}

	Switch:
		switch key {
		case "BOUNCER_NETID":
			s.netID = value
		case "CASEMAPPING":
			switch value {
			case "ascii":
				s.casemap = CasemapASCII
			default:
				s.casemap = CasemapRFC1459
			}
		case "CHANMODES":
			// We only care about the first four params
			types := strings.SplitN(value, ",", 5)
			for i := 0; i < len(types) && i < len(s.chanmodes); i++ {
				s.chanmodes[i] = types[i]
			}
		case "CHANTYPES":
			s.chantypes = value
		case "CHATHISTORY":
			historyLimit, err := strconv.Atoi(value)
			if err == nil {
				s.historyLimit = historyLimit
			}
		case "LINELEN":
			linelen, err := strconv.Atoi(value)
			if err == nil && linelen != 0 {
				s.linelen = linelen
			}
		case "MONITOR":
			monitor, err := strconv.Atoi(value)
			if err == nil && monitor > 0 {
				s.monitor = true
			}
		case "PREFIX":
			if value == "" {
				s.prefixModes = ""
				s.prefixSymbols = ""
			}
			if len(value)%2 != 0 {
				break Switch
			}
			for i := 0; i < len(value); i++ {
				if unicode.MaxASCII < value[i] {
					break Switch
				}
			}
			numPrefixes := len(value)/2 - 1
			s.prefixModes = value[1 : numPrefixes+1]
			s.prefixSymbols = value[numPrefixes+2:]
		}
	}
}

func (s *Session) endRegistration() {
	if s.registered {
		return
	}
	if s.netID != "" {
		s.out <- NewMessage("BOUNCER", "BIND", s.netID)
		s.out <- NewMessage("CAP", "END")
	} else {
		s.out <- NewMessage("CAP", "END")
		s.out <- NewMessage("BOUNCER", "LISTNETWORKS")
	}
}
