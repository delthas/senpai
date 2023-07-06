.POSIX:
.SUFFIXES:

GO ?= go
RM ?= rm
SCDOC ?= scdoc
GOFLAGS ?=
PREFIX ?= /usr/local
BINDIR ?= bin
MANDIR ?= share/man

all: senpai doc/senpai.1 doc/senpai.5

senpai:
	$(GO) build $(GOFLAGS) ./cmd/senpai
doc/senpai.1: doc/senpai.1.scd
	$(SCDOC) < doc/senpai.1.scd > doc/senpai.1
doc/senpai.5: doc/senpai.5.scd
	$(SCDOC) < doc/senpai.5.scd > doc/senpai.5

clean:
	$(RM) -rf senpai doc/senpai.1 doc/senpai.5
install:
	mkdir -p $(DESTDIR)$(PREFIX)/$(BINDIR)
	mkdir -p $(DESTDIR)$(PREFIX)/$(MANDIR)/man1
	mkdir -p $(DESTDIR)$(PREFIX)/$(MANDIR)/man5
	cp -f senpai $(DESTDIR)$(PREFIX)/$(BINDIR)
	cp -f doc/senpai.1 $(DESTDIR)$(PREFIX)/$(MANDIR)/man1
	cp -f doc/senpai.5 $(DESTDIR)$(PREFIX)/$(MANDIR)/man5
uninstall:
	$(RM) $(DESTDIR)$(PREFIX)/$(BINDIR)/senpai
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man1/senpai.1
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man5/senpai.5

.PHONY: all senpai clean install uninstall
