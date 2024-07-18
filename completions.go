package senpai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.sr.ht/~delthas/senpai/ui"
)

func (app *App) completionsChannelMembers(cs []ui.Completion, cursorIdx int, text []rune) []ui.Completion {
	var start int
	for start = cursorIdx - 1; 0 <= start; start-- {
		if text[start] == ' ' {
			break
		}
	}
	start++
	word := text[start:cursorIdx]
	if len(word) == 0 {
		return cs
	}
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID] // is not nil
	wordCf := s.Casemap(string(word))
	for _, name := range s.Names(buffer) {
		if strings.HasPrefix(s.Casemap(name.Name.Name), wordCf) {
			nickComp := []rune(name.Name.Name)
			if start == 0 {
				nickComp = append(nickComp, ':')
			}
			nickComp = append(nickComp, ' ')
			c := make([]rune, len(text)+len(nickComp)-len(word))
			copy(c[:start], text[:start])
			if cursorIdx < len(text) {
				copy(c[start+len(nickComp):], text[cursorIdx:])
			}
			copy(c[start:], nickComp)
			cs = append(cs, ui.Completion{
				StartIdx:  start,
				EndIdx:    cursorIdx,
				Text:      c,
				Display:   []rune(name.Name.Name),
				CursorIdx: start + len(nickComp),
			})
		}
	}
	return cs
}

func (app *App) completionsChannelTopic(cs []ui.Completion, cursorIdx int, text []rune) []ui.Completion {
	if !hasPrefix(text, []rune("/topic ")) {
		return cs
	}
	netID, buffer := app.win.CurrentBuffer()
	s := app.sessions[netID] // is not nil
	topic, _, _ := s.Topic(buffer)
	if cursorIdx == len(text) {
		compText := append(text, []rune(topic)...)
		cs = append(cs, ui.Completion{
			StartIdx:  cursorIdx,
			EndIdx:    cursorIdx,
			Text:      compText,
			CursorIdx: len(compText),
		})
	}
	return cs
}

func (app *App) completionsMsg(cs []ui.Completion, cursorIdx int, text []rune) []ui.Completion {
	if !hasPrefix(text, []rune("/msg ")) {
		return cs
	}
	s := app.CurrentSession() // is not nil
	// Check if the first word (target) is already written and complete (in
	// which case we don't have completions to provide).
	var word string
	hasMetALetter := false
	for i := 5; i < cursorIdx; i += 1 {
		if hasMetALetter && text[i] == ' ' {
			return cs
		}
		if !hasMetALetter && text[i] != ' ' {
			word = s.Casemap(string(text[i:cursorIdx]))
			hasMetALetter = true
		}
	}
	if word == "" {
		return cs
	}
	for _, user := range s.Users() {
		if strings.HasPrefix(s.Casemap(user), word) {
			nickComp := append([]rune(user), ' ')
			c := make([]rune, len(text)+5+len(nickComp)-cursorIdx)
			copy(c[:5], []rune("/msg "))
			copy(c[5:], nickComp)
			if cursorIdx < len(text) {
				copy(c[5+len(nickComp):], text[cursorIdx:])
			}
			cs = append(cs, ui.Completion{
				StartIdx:  5,
				EndIdx:    cursorIdx,
				Text:      c,
				CursorIdx: 5 + len(nickComp),
			})
		}
	}
	return cs
}

func (app *App) completionsUpload(cs []ui.Completion, cursorIdx int, text []rune) []ui.Completion {
	if !hasPrefix(text, []rune("/upload ")) {
		return cs
	}
	if app.cfg.Transient || !app.cfg.LocalIntegrations {
		return cs
	}
	_, path, ok := strings.Cut(string(text[:cursorIdx]), " ")
	if !ok {
		return cs
	}

	dirPath := ""
	dirPrefix := ""
	if path == "" {
		if filepath.Separator != '/' {
			return cs
		}
		dirPath = "/"
	} else if strings.HasSuffix(path, string(filepath.Separator)) {
		dirPath = path
	} else {
		dirPath = filepath.Dir(path)
		dirPrefix = filepath.Base(path)
	}
	dir, err := os.ReadDir(dirPath)
	if err != nil {
		return cs
	}

	for _, e := range dir {
		if !strings.HasPrefix(e.Name(), dirPrefix) {
			continue
		}
		name := e.Name()
		var isDir bool
		if e.IsDir() {
			isDir = true
		} else if e.Type() == os.ModeSymlink {
			if fi, err := os.Stat(filepath.Join(dirPath, name)); err == nil && fi.IsDir() {
				isDir = true
			}
		}
		if isDir {
			name += string(filepath.Separator)
		}
		if path == "" {
			name = "/" + name
		}
		if name == dirPrefix {
			continue
		}
		cs = append(cs, ui.Completion{
			StartIdx:  cursorIdx - len([]rune(dirPrefix)),
			EndIdx:    cursorIdx,
			Text:      []rune(string(text[:cursorIdx]) + name[len(dirPrefix):] + string(text[cursorIdx:])),
			CursorIdx: cursorIdx + len([]rune(name)) - len([]rune(dirPrefix)),
		})
	}
	return cs
}

func (app *App) completionsCommands(cs []ui.Completion, cursorIdx int, text []rune) []ui.Completion {
	if !hasPrefix(text, []rune("/")) {
		return cs
	}
	for i := 0; i < cursorIdx; i++ {
		if text[i] == ' ' {
			return cs
		}
	}
	if cursorIdx < len(text) && text[cursorIdx] != ' ' {
		return cs
	}

	uText := strings.ToUpper(string(text[1:cursorIdx]))
	for name := range commands {
		if strings.HasPrefix(name, uText) {
			c := make([]rune, len(text)+len(name)-len(uText))
			copy(c[:1], []rune("/"))
			copy(c[1:], []rune(strings.ToLower(name)))
			copy(c[1+len(name):], text[cursorIdx:])

			cs = append(cs, ui.Completion{
				StartIdx:  0,
				EndIdx:    cursorIdx,
				Text:      c,
				CursorIdx: 1 + len(name),
			})
		}
	}
	return cs
}

func (app *App) completionsEmoji(cs []ui.Completion, cursorIdx int, text []rune) []ui.Completion {
	var start int
	for start = cursorIdx - 1; start >= 0; start-- {
		r := text[start]
		if r == ':' {
			break
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (r >= '0' && r <= '9')) {
			return cs
		}
	}
	if start < 0 {
		return cs
	}
	start++
	word := text[start:cursorIdx]
	if len(word) == 0 {
		return cs
	}
	w := strings.ToLower(string(word))
	for _, emoji := range findEmoji(w) {
		c := make([]rune, 0, len(text)+len([]rune(emoji.Emoji))-len(word)-1)
		c = append(c, text[:start-1]...)
		c = append(c, []rune(emoji.Emoji)...)
		if cursorIdx < len(text) {
			c = append(c, text[cursorIdx:]...)
		}
		cs = append(cs, ui.Completion{
			StartIdx:  start - 1,
			EndIdx:    cursorIdx,
			Text:      c,
			Display:   []rune(fmt.Sprintf("%v (%v)", emoji.Emoji, emoji.Alias)),
			CursorIdx: start - 1 + len([]rune(emoji.Emoji)),
		})
	}
	return cs
}

func hasPrefix(s, prefix []rune) bool {
	return len(prefix) <= len(s) && equal(prefix, s[:len(prefix)])
}

func equal(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
