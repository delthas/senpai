# NOTICE me :senpai!

Welcome home, desune~

![a screenshot of your senpai feat. simon!](https://taiite.srht.site/senpai.png)

senpai is an IRC client that works best with bouncers:

- no logs are kept,
- history is fetched from the server via [CHATHISTORY],
- networks are fetched from the server via [bouncer-networks].

## How do I use this?

```shell
mkdir -p ~/.config/senpai
cat <<EOF >~/.config/senpai/senpai.scfg
address chat.sr.ht
nickname senpai
password "my password can't be this cute (2010)"
# alternatively, specify a command to fetch your password:
# password-cmd  gopass show irc/<username>
EOF
go run ./cmd/senpai
```

Then, type `/join #senpai` on [Libera.Chat] and have a... chat!

See `doc/senpai.1.scd` for more information and `doc/senpai.5.scd` for more
configuration options!

## Debugging errors, testing servers

If you run into errors and want to find the WHY OH WHY, or if you'd like to try
things out with an IRC server, then you have two options:

1. Run senpai with the `-debug` argument (or put `debug true`) in your config,
   it will then print in `home` all the data it sends and receives.
2. Run the test client, that uses the same IRC library but exposes a simpler
   interface, by running `go run ./cmd/test -help`.

## Contributing and stuff

Contributions are accepted as patches to [the mailing list][ml] and as pull
requests on [Github].

Browse tickets at <https://todo.sr.ht/~taiite/senpai>.

## License

This senpai is open source! Please use it under the ISC license.

Copyright (C) 2021 The senpai Contributors

[bouncer-networks]: https://git.sr.ht/~emersion/soju/tree/master/item/doc/ext/bouncer-networks.md
[CHATHISTORY]: https://ircv3.net/specs/extensions/chathistory
[Github]: https://github.com/hhirtz/senpai/pulls
[Libera.Chat]: https://libera.chat/
[ml]: https://lists.sr.ht/~delthas/senpai-dev
