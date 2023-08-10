# NOTICE me :senpai!

*Welcome home, desune~*

**A modern terminal IRC client.**

![a screenshot of your senpai feat. simon!](https://taiite.srht.site/senpai.png)

senpai is an IRC client that works best with bouncers:

- no logs are kept,
- history is fetched from the server via [CHATHISTORY],
- networks are fetched from the server via [bouncer-networks],
- messages can be searched in logs via [SEARCH].

## Installing

From source (requires Go):
```shell
git clone git.sr.ht/~taiite/senpai/cmd/senpai
cd senpai
make
sudo make install
```

For a simple Go local install:
```shell
git clone git.sr.ht/~taiite/senpai/cmd/senpai
cd senpai
go install ./cmd/senpai
```


## Running

From your terminal:
```shell
senpai
```
Senpai will guide you through a configuration assistant on your first run.

Then, type `/join #senpai` on [Libera.Chat] and have a... chat!

See `doc/senpai.1.scd` for more information and `doc/senpai.5.scd` for more
configuration options!

## Debugging errors, testing servers

To debug IRC traffic, run senpai with the `-debug` argument (or put `debug true`) in your config, it will then print in the `home` buffer all the data it sends and receives.

## Issue tracker

Browse tickets at <https://todo.sr.ht/~taiite/senpai>.

## Contributing

Sending patches to senpai is done [by email](https://lists.sr.ht/~delthas/senpai-dev), this is simple and built-in to Git.

Set up your system once by following the steps Installation and Configuration of [git-send-email.io](https://git-send-email.io/)

Then, run once in this repository:
```shell
git config sendemail.to "~delthas/senpai-dev@lists.sr.ht"
```

Then, to send a patch, make your commit, then run:
```shell
git send-email --base=HEAD~1 --annotate -1 -v1
```

It should then appear on [the mailing list](https://lists.sr.ht/~delthas/senpai-dev/patches).

## License

This senpai is open source! Please use it under the ISC license.

Copyright (C) 2021 The senpai Contributors

[bouncer-networks]: https://git.sr.ht/~emersion/soju/tree/master/item/doc/ext/bouncer-networks.md
[CHATHISTORY]: https://ircv3.net/specs/extensions/chathistory
[SEARCH]: https://github.com/ircv3/ircv3-specifications/pull/496
[Libera.Chat]: https://libera.chat/
[ml]: https://lists.sr.ht/~delthas/senpai-dev
