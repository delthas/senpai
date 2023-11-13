.POSIX:
.SUFFIXES:

GO ?= go
RM ?= rm
SCDOC ?= scdoc
GOFLAGS ?=
PREFIX ?= /usr/local
BINDIR ?= bin
MANDIR ?= share/man
APPDIR ?= share/applications

all: senpai doc

senpai:
	$(GO) build $(GOFLAGS) ./cmd/senpai

ifeq (, $(shell which $(SCDOC) 2>/dev/null))
$(warning "$(SCDOC) not found, skipping building documentation")
doc:
else
doc: doc/senpai.1 doc/senpai.5
doc/senpai.1: doc/senpai.1.scd
	$(SCDOC) < doc/senpai.1.scd > doc/senpai.1
doc/senpai.5: doc/senpai.5.scd
	$(SCDOC) < doc/senpai.5.scd > doc/senpai.5
endif

clean:
	$(RM) -rf senpai doc/senpai.1 doc/senpai.5
install:
	mkdir -p $(DESTDIR)$(PREFIX)/$(BINDIR)
	mkdir -p $(DESTDIR)$(PREFIX)/$(MANDIR)/man1
	mkdir -p $(DESTDIR)$(PREFIX)/$(MANDIR)/man5
	mkdir -p $(DESTDIR)$(PREFIX)/$(APPDIR)
	cp -f senpai $(DESTDIR)$(PREFIX)/$(BINDIR)
	cp -f doc/senpai.1 $(DESTDIR)$(PREFIX)/$(MANDIR)/man1
	cp -f doc/senpai.5 $(DESTDIR)$(PREFIX)/$(MANDIR)/man5
	cp -f contrib/senpai.desktop $(DESTDIR)$(PREFIX)/$(APPDIR)/senpai.desktop
uninstall:
	$(RM) $(DESTDIR)$(PREFIX)/$(BINDIR)/senpai
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man1/senpai.1
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man5/senpai.5
	$(RM) $(DESTDIR)$(PREFIX)/$(APPDIR)/senpai.desktop

emoji:
	curl -sSfL -o emoji.json "https://raw.githubusercontent.com/github/gemoji/master/db/emoji.json"

.PHONY: all senpai doc clean install uninstall emoji
