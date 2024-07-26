.POSIX:
.SUFFIXES:

GO ?= go
RM ?= rm
INSTALL ?= install
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
	$(INSTALL) -D -T senpai $(DESTDIR)$(PREFIX)/$(BINDIR)/senpai
	$(INSTALL) -D -T -m644 doc/senpai.1 $(DESTDIR)$(PREFIX)/$(MANDIR)/man1/senpai.1
	$(INSTALL) -D -T -m644 doc/senpai.5 $(DESTDIR)$(PREFIX)/$(MANDIR)/man5/senpai.5
	$(INSTALL) -D -T -m644 contrib/senpai.desktop $(DESTDIR)$(PREFIX)/$(APPDIR)/senpai.desktop
	$(INSTALL) -D -T -m644 res/icon.48.png $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/48x48/apps/senpai.png
	$(INSTALL) -D -T -m644 res/icon.svg $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/scalable/apps/senpai.svg
uninstall:
	$(RM) $(DESTDIR)$(PREFIX)/$(BINDIR)/senpai
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man1/senpai.1
	$(RM) $(DESTDIR)$(PREFIX)/$(MANDIR)/man5/senpai.5
	$(RM) $(DESTDIR)$(PREFIX)/$(APPDIR)/senpai.desktop
	$(RM) $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/48x48/apps/senpai.png
	$(RM) $(DESTDIR)$(PREFIX)/$(ICONDIR)/hicolor/scalable/apps/senpai.svg

emoji:
	curl -sSfL -o emoji.json "https://raw.githubusercontent.com/github/gemoji/master/db/emoji.json"

.PHONY: all senpai doc res clean install uninstall emoji
