package irc

import "time"

type Event interface{}

type ErrorEvent struct {
	Severity Severity
	Code     string
	Message  string
}

type RegisteredEvent struct{}

type SelfNickEvent struct {
	FormerNick string
}

type UserNickEvent struct {
	User       string
	FormerNick string
}

type SelfJoinEvent struct {
	Channel   string
	Requested bool // whether we recently requested to join that channel
	Topic     string
}

type UserJoinEvent struct {
	User    string
	Channel string
}

type SelfPartEvent struct {
	Channel string
}

type UserPartEvent struct {
	User    string
	Channel string
}

type UserQuitEvent struct {
	User     string
	Channels []string
}

type TopicChangeEvent struct {
	Channel string
	Topic   string
}

type ModeChangeEvent struct {
	Channel string
	Mode    string
}

type InviteEvent struct {
	Inviter string
	Invitee string
	Channel string
}

type MessageEvent struct {
	User            string
	Target          string
	TargetIsChannel bool
	MessageID       string
	Command         string
	Content         string
	Time            time.Time
}

type ReactEvent struct {
	User            string
	Target          string
	TargetIsChannel bool
	MessageID       string
	Content         string
}

type HistoryEvent struct {
	Target   string
	Messages []Event
}

type BouncerNetworkEvent struct {
	ID   string
	Name string
}
