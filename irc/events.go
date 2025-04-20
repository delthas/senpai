package irc

import "time"

type Event interface{}

type InfoEvent struct {
	Prefix  string
	Message string
}

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
	Read      time.Time
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
	Who     string
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

type ListItem struct {
	Channel string
	Count   string
	Topic   string
}

type ListEvent []ListItem

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

type SearchEvent struct {
	Messages []MessageEvent
}

type MetadataChangeEvent struct {
	Target string
	Pinned bool
	Muted  bool
}

type BouncerNetworkEvent struct {
	ID     string
	Name   string
	Delete bool
}
