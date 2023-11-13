package senpai

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed emoji.json
var emojiJSON []byte

type emoji struct {
	Emoji string
	Alias string
}

type emojis []emoji

func (e emojis) Len() int {
	return len(e)
}

func (e emojis) Less(i, j int) bool {
	return e[i].Alias < e[j].Alias
}

func (e emojis) Swap(i, j int) {
	e[i], e[j] = e[j], e[i]
}

var emojiData []emoji

func init() {
	type rawEmoji struct {
		Emoji   string   `json:"emoji"`
		Aliases []string `json:"aliases"`
	}
	var data []rawEmoji
	_ = json.Unmarshal(emojiJSON, &data)
	for _, e := range data {
		for _, alias := range e.Aliases {
			emojiData = append(emojiData, emoji{
				Emoji: e.Emoji,
				Alias: alias,
			})
		}
	}
	sort.Sort(emojis(emojiData))
}

func findEmoji(s string) []emoji {
	if len(emojiData) == 0 {
		return nil
	}
	i, ok := sort.Find(len(emojiData), func(i int) int {
		return strings.Compare(s, emojiData[i].Alias)
	})
	var b, e int
	for b = i; b >= 0; b-- {
		if !strings.HasPrefix(emojiData[b].Alias, s) {
			break
		}
	}
	b++
	for e = i; e < len(emojiData); e++ {
		if !strings.HasPrefix(emojiData[e].Alias, s) {
			break
		}
	}
	if b >= e {
		return nil
	}
	if ok {
		r := make([]emoji, 0, e-b)
		r = append(r, emojiData[i])
		r = append(r, emojiData[b:i]...)
		r = append(r, emojiData[i+1:e]...)
		return r
	} else {
		return emojiData[b:e]
	}
}
