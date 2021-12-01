package irc

// IRC replies.
const (
	rplWelcome  = "001" // :Welcome message
	rplYourhost = "002" // :Your host is...
	rplCreated  = "003" // :This server was created...
	rplMyinfo   = "004" // <servername> <version> <umodes> <chan modes> <chan modes with a parameter>
	rplIsupport = "005" // 1*13<TOKEN[=value]> :are supported by this server

	rplUmodeis       = "221" // <modes>
	rplLuserclient   = "251" // :<int> users and <int> services on <int> servers
	rplLuserop       = "252" // <int> :operator(s) online
	rplLuserunknown  = "253" // <int> :unknown connection(s)
	rplLuserchannels = "254" // <int> :channels formed
	rplLuserme       = "255" // :I have <int> clients and <int> servers
	rplAdminme       = "256" // <server> :Admin info
	rplAdminloc1     = "257" // :<info>
	rplAdminloc2     = "258" // :<info>
	rplAdminmail     = "259" // :<info>

	rplAway            = "301" // <nick> :<away message>
	rplUnaway          = "305" // :You are no longer marked as being away
	rplNowaway         = "306" // :You have been marked as being away
	rplWhoisuser       = "311" // <nick> <user> <host> * :<realname>
	rplWhoisserver     = "312" // <nick> <server> :<server info>
	rplWhoisoperator   = "313" // <nick> :is an IRC operator
	rplEndofwho        = "315" // <name> :End of WHO list
	rplWhoisidle       = "317" // <nick> <integer> [<integer>] :seconds idle [, signon time]
	rplEndofwhois      = "318" // <nick> :End of WHOIS list
	rplWhoischannels   = "319" // <nick> :*( (@/+) <channel> " " )
	rplList            = "322" // <channel> <# of visible members> <topic>
	rplListend         = "323" // :End of list
	rplChannelmodeis   = "324" // <channel> <modes> <mode params>
	rplNotopic         = "331" // <channel> :No topic set
	rplTopic           = "332" // <channel> <topic>
	rplTopicwhotime    = "333" // <channel> <nick> <setat>
	rplInviting        = "341" // <nick> <channel>
	rplInvitelist      = "346" // <channel> <invite mask>
	rplEndofinvitelist = "347" // <channel> :End of invite list
	rplExceptlist      = "348" // <channel> <exception mask>
	rplEndofexceptlist = "349" // <channel> :End of exception list
	rplVersion         = "351" // <version> <servername> :<comments>
	rplWhoreply        = "352" // <channel> <user> <host> <server> <nick> "H"/"G" ["*"] [("@"/"+")] :<hop count> <nick>
	rplNamreply        = "353" // <=/*/@> <channel> :1*(@/ /+user)
	rplEndofnames      = "366" // <channel> :End of names list
	rplBanlist         = "367" // <channel> <ban mask>
	rplEndofbanlist    = "368" // <channel> :End of ban list
	rplInfo            = "371" // :<info>
	rplMotd            = "372" // :- <text>
	rplEndofinfo       = "374" // :End of INFO
	rplMotdstart       = "375" // :- <servername> Message of the day -
	rplEndofmotd       = "376" // :End of MOTD command
	rplYoureoper       = "381" // :You are now an operator
	rplRehashing       = "382" // <config file> :Rehashing
	rplTime            = "391" // <servername> :<time in whatever format>

	errNosuchnick       = "401" // <nick> :No such nick/channel
	errNosuchchannel    = "403" // <channel> :No such channel
	errCannotsendtochan = "404" // <channel> :Cannot send to channel
	errInvalidcapcmd    = "410" // <command> :Unknown cap command
	errNorecipient      = "411" // :No recipient given
	errNotexttosend     = "412" // :No text to send
	errInputtoolong     = "417" // :Input line was too long
	errUnknowncommand   = "421" // <command> :Unknown command
	errNomotd           = "422" // :MOTD file missing
	errNonicknamegiven  = "431" // :No nickname given
	errErroneusnickname = "432" // <nick> :Erroneous nickname
	errNicknameinuse    = "433" // <nick> :Nickname in use
	errUsernotinchannel = "441" // <nick> <channel> :User not in channel
	errNotonchannel     = "442" // <channel> :You're not on that channel
	errUseronchannel    = "443" // <user> <channel> :is already on channel
	errNotregistered    = "451" // :You have not registered
	errNeedmoreparams   = "461" // <command> :Not enough parameters
	errAlreadyregistred = "462" // :Already registered
	errPasswdmismatch   = "464" // :Password incorrect
	errYourebannedcreep = "465" // :You're banned from this server
	errKeyset           = "467" // <channel> :Channel key already set
	errChannelisfull    = "471" // <channel> :Cannot join channel (+l)
	errUnknownmode      = "472" // <char> :Don't know this mode for <channel>
	errInviteonlychan   = "473" // <channel> :Cannot join channel (+I)
	errBannedfromchan   = "474" // <channel> :Cannot join channel (+b)
	errBadchankey       = "475" // <channel> :Cannot join channel (+k)
	errNopriviledges    = "481" // :Permission Denied- You're not an IRC operator
	errChanoprivsneeded = "482" // <channel> :You're not an operator

	errUmodeunknownflag = "501" // :Unknown mode flag
	errUsersdontmatch   = "502" // :Can't change mode for other users

	rplMononline     = "730" // <nick> :target[!user@host][,target[!user@host]]*
	rplMonoffline    = "731" // <nick> :target[,target2]*
	rplMonlist       = "732" // <nick> :target[,target2]*
	rplEndofmonlist  = "733" // <nick> :End of MONITOR list
	errMonlistisfull = "734" // <nick> <limit> <targets> :Monitor list is full.

	rplLoggedin    = "900" // <nick> <nick>!<ident>@<host> <account> :You are now logged in as <user>
	rplLoggedout   = "901" // <nick> <nick>!<ident>@<host> :You are now logged out
	errNicklocked  = "902" // :You must use a nick assigned to you
	rplSaslsuccess = "903" // :SASL authentication successful
	errSaslfail    = "904" // :SASL authentication failed
	errSasltoolong = "905" // :SASL message too long
	errSaslaborted = "906" // :SASL authentication aborted
	errSaslalready = "907" // :You have already authenticated using SASL
	rplSaslmechs   = "908" // <mechanisms> :are available SASL mechanisms
)
