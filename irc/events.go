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
	Time       time.Time
}

type SelfJoinEvent struct {
	Channel   string
	Requested bool // whether we recently requested to join that channel
	Topic     string
}

type UserJoinEvent struct {
	User    string
	Channel string
	Time    time.Time
}

type SelfPartEvent struct {
	Channel string
}

type UserPartEvent struct {
	User    string
	Channel string
	Time    time.Time
}

type UserQuitEvent struct {
	User     string
	Channels []string
	Time     time.Time
}

type UserOnlineEvent struct {
	User string
}

type UserOfflineEvent struct {
	User string
}

type TopicChangeEvent struct {
	Channel string
	Topic   string
	Time    time.Time
}

type ModeChangeEvent struct {
	Channel string
	Mode    string
	Time    time.Time
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
	Command         string
	Content         string
	Time            time.Time
}

type HistoryEvent struct {
	Target   string
	Messages []Event
}

type HistoryTargetsEvent struct {
	Targets map[string]time.Time
}

type ReadEvent struct {
	Target    string
	Timestamp time.Time
}

type BouncerNetworkEvent struct {
	ID   string
	Name string
}
