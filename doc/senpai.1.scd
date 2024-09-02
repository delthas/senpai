senpai(1)

# NAME

senpai - your everyday IRC student

# SYNOPSIS

*senpai* [options...]

# OPTIONS

*-config* <path>
	Use a different path for the configuration file.

*-nickname* <nickname>
	Advanced. Nick name to connect as. Overrides the configuration item of the
	same name.

*-debug*
	Advanced. Show all IRC messages that are received from/sent to the server.

# DESCRIPTION

senpai is an IRC client made for bouncers.  It supports the newest IRC
extensions, such as:

- _CHATHISTORY_, senpai fetches history from the server instead of keeping logs,
- _@+typing_, senpai shows when others are typing a message,
- _BOUNCER_, senpai connects to all your networks at once automatically,
- and more to come!

# CONFIGURATION

senpai searches for it in the following location:

	$XDG_CONFIG_HOME/senpai/senpai.scfg

If unset, $XDG_CONFIG_HOME defaults to *~/.config*.

For information about the configuration format, see *senpai*(5).

If the configuration file does not exist, a setup assistant will create one for
you.

# USER INTERFACE

The user interface of senpai consists of 4 parts.  Starting from the bottom:

The *buffer list*, shows joined channels.  The special buffer *home* is where
server notices are shown.  This list can be put on the left of the screen with
the _chan-column-width_ configuration option. Buffers can be closed with the
mouse middle click, or the _part_ command.

On the row above, the *input field* is where you type in messages or commands
(see *COMMANDS*).  By default, when you type a message, senpai will inform
others in the channel that you are typing.

On the row above, the *status line* (or... just a line if nothing is
happening...) is where typing indicators are shown (e.g. "dan- is typing...").

Finally, the *timeline* is displayed on the rest of the screen.  Several types
of messages are in the timeline:

- User messages are shown with their nicknames,
- User actions (*/me*) are shown with an asterisk (*\**) followed by the user's
  nickname,
- Status messages, such as joins, parts, topics and name lists, are shown with
  two dashes (*--*),
- Notices are shown with an asterisk (*\**) followed by the user nickname and a
  colon

# SELECTING TEXT

In order to select text with a mouse, hold SHIFT while clicking and dragging
the mouse. *Clicking and dragging without holding SHIFT will not work, as
senpai eats these events for eg selecting channels.*

# OPENING LINKS

In order to open links, refer to your terminal manual. On most terminals,
opening links is done by holding CTRL, or SHIFT, while clicking the link.
On the *foot* terminal, links can be opened by pressing CTRL+SHIFT+O.

Additionally, by simply clicking an image link with the mouse, a small preview
will be shown, which can be closed by clicking again anywhere in the interface.

# KEYBOARD SHORTCUTS

*CTRL-A*
	Move the cursor to the beginning of the input field.

*CTRL-E*
	Move the cursor to the end of the input field.

*CTRL-C*
	Clear input line.

*CTRL-F*
	Prepare for search: add /search to input line.

*CTRL-K*
	Prepare for jumping to a buffer: add /buffer to input line.

*CTRL-U*, *PgUp*
	Go up in the timeline.

*CTRL-D*, *PgDown*
	Go down in the timeline.

*CTRL-N*, *ALT-RIGHT*
	Go to the next buffer.

*CTRL-P*, *ALT-LEFT*
	Go to the previous buffer.

*SHIFT-RIGHT*
	Go to the next unread buffer.

*SHIFT-LEFT*
	Go to the previous unread buffer.

*ALT-HOME*
	Go to the first buffer.

*ALT-END*
	Go to the last buffer.

*ALT-P*
	Go to the previous highlight

*ALT-N*
	Go to the next highlight, or to the (most recent) end of the timeline if
	there is none.

*ALT-{1..9}*
	Go to buffer by index.

*UP*, *DOWN*, *LEFT*, *RIGHT*, *HOME*, *END*, *BACKSPACE*, *DELETE*
	Edit the text in the input field.

*ENTER*
	Sends the contents of the input field.

*TAB*
	Open the auto-completion dialog. Choose auto-completion item with *UP* and
	*DOWN*, then press *TAB* again to confirm.

*CTRL-L*
	Refresh the window.

*F7*
	Show/hide the vertical channel list.

*F8*
	Show/hide the vertical member list.

# COMMANDS

If you type and send a message that starts with a slash (*/*), it will instead
be interpreted as a command:

	/_name_ argument1 argument2...

_name_ is matched case-insensitively.  It can be one of the following:

*HELP* [search]
	Show the list of command (or a commands that match the given search terms).

*JOIN* <channel>
	Join the given channel.

*PART* [channel] [reason]
	Part the given channel, defaults to the current one if omitted.

*QUIT* [reason]
	Quits senpai.

*MOTD*
	Show the message of the day (MOTD).

*NAMES*
	Show the member list of the current channel.  Powerlevels (such as _@_ for
	"operator", or _+_ for "voice") are shown in green.

*TOPIC* [topic]
	If _topic_ is omitted, show the topic of the current channel and, if
	available, the person who set it and the time when it has been set.

	Otherwise, change the topic of the current channel to _topic_.

*MUTE*
	Mute the current channel. This prevents being distracted from new messages
	on unimportant channels.

	This moves the channel to the end of the server, and greys its name out so
	that new messages do not change its color in the list. Highlights will still
	send a notification.

*UNMUTE*
	Unmute the current channel. See *MUTE*.

*PIN*
	Pin the current channel. This moves the channel to the start of the buffer
	list.

*UNPIN*
	Unpin the current channel. See *PIN*.

*MSG* <target> <content>
	Send _content_ to _target_.

*REPLY* <content>
	Reply to the last person who sent a private message.

*ME* <content>
	Send a message prefixed with your nick (a user action). If sent from home,
	reply to the last person who sent a private message.

*NP*
	Send the current song that is being played on the system. Uses DBus/MPRIS
	internally.

*UPLOAD* <file path>
	Upload a local file to the bouncer.

*QUOTE* <raw message>
	Send _raw message_ verbatim.

*LIST* [pattern]
	List public channels, optionally matching the specified pattern.

*BUFFER* <index|name>
	Switch to the buffer at the _index_ position, or containing _name_.
	The buffer list will be filtered according to the passed name; entering the
	command will select the first buffer in the list.

*WHOIS* <nickname>
	Get information about someone who is connected.

*WHOWAS* <nickname>
	Get information about someone who is disconnected.

*NICK* <nickname>
	Change your nickname.

*OPER* <username> <password>
	Log in to an operator account.

*MODE* <nick/channel> <flags> [args]
	Change channel or user modes.

*INVITE* <nick> [channel]
	Invite _nick_ to _channel_ (the current channel if not given).

*KICK* <nick> [channel] [message]
	Eject _nick_ from _channel_ (the current channel if not given) with an
	optional kick message/reason.

*BAN* <nick> [channel]
	Ban _nick_ from entering _channel_ (the current channel if not given).

*UNBAN* <nick> [channel]
	Allow _nick_ to enter _channel_ again (the current channel if not given).

*SEARCH* <text>
	Search messages matching the given text, in the current channel or server.
	This opens a temporary list, which can be closed with the escape key.

*AWAY* [message]
	Mark yourself as away, with an optional away message.

*BACK*
	Mark yourself as back from being away.

*VERSION* [target]
	Query the server software version.

*ADMIN* [target]
	Query the server administrative information.

*LUSERS*
	Query the server user information.

*TIME* [target]
	Query the server local time.

*INFO*
	Query server information.

*LINKS*
	Query the servers of the network.

*SHRUG*
	Send a shrug emoji to the current channel. ¯\\\_(ツ)\_/¯

*TABLEFLIP*
	Send a table flip emoji to the current channel. (╯°□°)╯︵ ┻━┻

*STATS* <query> [target]
	Query server statistics (advanced).

*CONNECT* <target server> [<port> [remote server]]
	Connect a server to the network (advanced).

*SQUIT* <server> [comment]
	Disconnects a server from the network (advanced).

*KILL* <nick> [message]
	Eject someone from the server (advanced).

*REHASH*
	Make the server reload its configuration (advanced).

*RESTART*
	Make the server restart (advanced).

*WALLOPS* [text]
	Broadcast a message to all users (advanced).

# SEE ALSO

*senpai*(5)

# AUTHORS

Creator/Sensei: Hubert Hirtz <hubert@hirtzfr.eu>!++
Current maintainer: delthas <delthas@dille.cc>.++
Sources are available at <https://git.sr.ht/~delthas/senpai>.++
Patches are welcome by email at <~delthas/senpai-dev@lists.sr.ht>.
