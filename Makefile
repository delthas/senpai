.POSIX:
.SUFFIXES:

GO ?= go
RM ?= rm
SCDOC ?= scdoc
GIT ?= git
GOFLAGS ?=
PREFIX ?= /usr/local
BINDIR ?= bin
MANDIR ?= share/man
APPDIR ?= share/applications
ICONDIR ?= share/icons

ifeq (0, $(shell $(GIT) status >/dev/null 2>&1; echo $$?))
export SOURCE_DATE_EPOCH ?= $(shell $(GIT) log -1 --pretty=%ct)
endif

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

res: res/icon.128.png res/icon.48.png
res/icon.128.png: res/icon.svg
	rsvg-convert -o res/icon.128.png res/icon.svg
res/icon.48.png: res/icon.svg
	rsvg-convert -w 48 -h 48 -o res/icon.48.png res/icon.svg

clean:
	$(RM) -rf senpai doc/senpai.1 doc/senpai.5
install:
	mkdir -p $(DESTDIR)$(PREFIX)/$(BINDIR)
	mkdir -p $(DESTDIR)$(PREFIX)/$(MANDIR)/man1
	mkdir -p $(DESTDIR)$(PREFIX)/$(MANDIR)/man5
	mkdir -p $(DESTDIR)$(PREFIX)/$(APPDIR)
	mkdir -p $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/48x48/apps
	mkdir -p $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/scalable/apps
	cp -f senpai $(DESTDIR)$(PREFIX)/$(BINDIR)
	cp -f doc/senpai.1 $(DESTDIR)$(PREFIX)/$(MANDIR)/man1
	cp -f doc/senpai.5 $(DESTDIR)$(PREFIX)/$(MANDIR)/man5
	cp -f contrib/senpai.desktop $(DESTDIR)$(PREFIX)/$(APPDIR)/senpai.desktop
	cp -f res/icon.48.png $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/48x48/apps/senpai.png
	cp -f res/icon.svg $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/scalable/apps/senpai.svg
uninstall:
	$(RM) $(DESTDIR)$(PREFIX)/$(BINDIR)/senpai
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man1/senpai.1
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man5/senpai.5
	$(RM) $(DESTDIR)$(PREFIX)/$(APPDIR)/senpai.desktop

emoji:
	curl -sSfL -o emoji.json "https://raw.githubusercontent.com/github/gemoji/master/db/emoji.json"

.PHONY: all senpai doc res clean install uninstall emoji
