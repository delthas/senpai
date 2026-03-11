package senpai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"git.sr.ht/~delthas/senpai/events"
	"git.sr.ht/~delthas/senpai/ui"
)

// Rules disabled for informal IM context.
// Full list: https://writewithharper.com/docs/rules
var harperDisabledLinters = map[string]bool{
	// Formality
	"SentenceCapitalization":     false,
	"CapitalizePersonalPronouns": false,
	"LongSentences":              false,
	"UnclosedQuotes":             false,
	"OxfordComma":                false,
	"Spaces":                     false,
	"NoFrenchSpaces":             false,
	"AvoidCurses":                false,
	"Hedging":                    false,
	"FillerWords":                false,
	"DiscourseMarkers":           false,

	// Initialisms (btw, imo, ttyl, brb, etc.)
	"ByTheWay":           false,
	"InMyOpinion":        false,
	"InMyHumbleOpinion":  false,
	"TalkToYouLater":     false,
	"ForWhatItsWorth":    false,
	"AsFarAsIKnow":       false,
	"BeRightBack":        false,
	"OhMyGod":            false,
	"AsSoonAsPossible":   false,
	"InRealLife":         false,
	"ForYourInformation": false,
	"IDontKnow":          false,
	"IfYouKnowYouKnow":   false,
	"InCaseYouMissedIt":  false,
	"ToBeHonest":         false,
	"PleaseTakeALook":    false,
	"NeverMind":          false,
	"IfIRecallCorrectly": false,
	"Really":             false,
	"ExplainLikeImFive":  false,
	"RedundantIIRC":      false,

	// Casual abbreviations
	"ExpandThrough":                false,
	"ExpandWith":                   false,
	"ExpandWithout":                false,
	"ExpandForward":                false,
	"ExpandBecause":                false,
	"ExpandPrevious":               false,
	"ExpandControl":                false,
	"ExpandMinimum":                false,
	"ExpandTimeShorthands":         false,
	"ExpandMemoryShorthands":       false,
	"ExpandParameter":              false,
	"ExpandDependencies":           false,
	"ExpandStandardInputAndOutput": false,
	"Cybersec":                     false,

	// Overly prescriptive style
	"Dashes":         false,
	"EllipsisLength": false,
	"SendAnEmailTo":  false,
	"Touristic":      false,
	"BoringWords":    false,
	"Freezing":       false,
	"Starving":       false,
	"Excellent":      false,
	"FatalOutcome":   false,
	"AvoidAndAlso":   false,
	"CondenseAllThe": false,
	"DotInitialisms": false,

	// Too aggressive for technical/casual chat
	"SplitWords":                false,
	"OrthographicConsistency":   false,
	"ToDoHyphen":                false,
	"PhrasalVerbAsCompoundNoun": false,
	"DisjointPrefixes":          false,
	"NeedToNoun":                false,
	"MissingTo":                 false,
	"MoreAdjective":             false,
	"RightClick":                false,
	"MassNouns":                 false,
	"HaveTakeALook":             false,
}

type harperConfig struct {
	HarperLS harperLSConfig `json:"harper-ls"`
}

type harperLSConfig struct {
	Linters      map[string]bool `json:"linters"`
	UserDictPath string          `json:"userDictPath,omitempty"`
}

type lspPosition struct {
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspDiagnostic struct {
	Range lspRange `json:"range"`
}

type lspMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type lspDiagnosticsParams struct {
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type harperState struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	writes chan []byte
	textCh chan string

	reqID    int
	ver      int
	dictPath string
	dictPrev string
}

func (app *App) harperInit() {
	if !app.cfg.SpellCheck || app.cfg.Transient {
		return
	}
	path, err := exec.LookPath("harper-ls")
	if err != nil {
		return
	}
	cmd := exec.Command(path, "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return
	}

	dictFile, err := os.CreateTemp("", "senpai-harper-dict-*.txt")
	if err != nil {
		stdin.Close()
		cmd.Process.Kill()
		return
	}
	dictFile.Close()

	h := &harperState{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdout),
		writes:   make(chan []byte, 64),
		textCh:   make(chan string, 1),
		dictPath: dictFile.Name(),
	}
	go h.writeLoop()

	if err := h.sendRequest("initialize", json.RawMessage(`{
		"capabilities": {
			"textDocument": {
				"publishDiagnostics": {}
			}
		},
		"rootUri": null,
		"processId": null
	}`)); err != nil {
		cmd.Process.Kill()
		return
	}

	if err := h.sendNotification("initialized", json.RawMessage(`{}`)); err != nil {
		cmd.Process.Kill()
		return
	}

	app.harper = h
	go app.harperLoop()
}

func (app *App) harperLoop() {
	h := app.harper
	opened := false
	for {
		msg, err := h.readMessage()
		if err != nil {
			app.queueStatusLine("", ui.Line{
				Head: ui.PlainString("--"),
				Body: ui.PlainString("spell checker error: " + err.Error()),
			})
			return
		}
		var envelope lspMessage
		if err := json.Unmarshal(msg, &envelope); err != nil {
			continue
		}
		if envelope.ID != nil && envelope.Method != "" {
			switch envelope.Method {
			case "workspace/configuration":
				cfg, _ := json.Marshal([]harperConfig{{HarperLS: harperLSConfig{Linters: harperDisabledLinters, UserDictPath: h.dictPath}}})
				h.sendResponse(envelope.ID, cfg)
				if !opened {
					opened = true
					h.sendNotification("textDocument/didOpen", json.RawMessage(`{
						"textDocument": {
							"uri": "file:///senpai-input.txt",
							"languageId": "plaintext",
							"version": 0,
							"text": ""
						}
					}`))
				}
			default:
				h.sendResponse(envelope.ID, json.RawMessage(`null`))
			}
			continue
		}
		if envelope.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var params lspDiagnosticsParams
		if err := json.Unmarshal(envelope.Params, &params); err != nil {
			continue
		}
		var typos []events.TypoRange
		for _, d := range params.Diagnostics {
			typos = append(typos, events.TypoRange{
				Start: d.Range.Start.Character,
				End:   d.Range.End.Character,
			})
		}
		app.postEvent(event{
			src:     "*",
			content: &events.EventSpellCheck{Typos: typos},
		})
	}
}

func (app *App) spellCheck() {
	if app.harper == nil {
		return
	}
	input := app.win.InputContent()
	if len(input) == 0 {
		app.win.SetTypos(nil)
		return
	}
	if isCommand(input) {
		app.win.SetTypos(nil)
		return
	}
	app.updateHarperDict()
	app.harper.sendChange(string(input))
}

func (app *App) updateHarperDict() {
	netID, _ := app.win.CurrentBuffer()
	s := app.sessions[netID]
	if s == nil {
		return
	}
	words := s.Users()
	slices.Sort(words)
	content := strings.Join(words, "\n")
	if content == app.harper.dictPrev {
		return
	}
	app.harper.dictPrev = content
	os.WriteFile(app.harper.dictPath, []byte(content), 0600)
}

func (app *App) harperClose() {
	if app.harper == nil {
		return
	}
	os.Remove(app.harper.dictPath)
	app.harper.sendNotification("shutdown", json.RawMessage(`null`))
	close(app.harper.writes)
	app.harper.cmd.Process.Kill()
}

func (h *harperState) sendChange(text string) {
	select {
	case <-h.textCh:
	default:
	}
	h.textCh <- text
}

func (h *harperState) sendRequest(method string, params json.RawMessage) error {
	h.reqID++
	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      h.reqID,
		"method":  method,
		"params":  params,
	})
	return h.writeMessage(msg)
}

func (h *harperState) sendResponse(id json.RawMessage, result json.RawMessage) error {
	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return h.writeMessage(msg)
}

func (h *harperState) sendNotification(method string, params json.RawMessage) error {
	msg, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	return h.writeMessage(msg)
}

func (h *harperState) writeMessage(msg []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg))
	h.writes <- append([]byte(header), msg...)
	return nil
}

func (h *harperState) writeLoop() {
	for {
		select {
		case msg, ok := <-h.writes:
			if !ok {
				return
			}

			if _, err := h.stdin.Write(msg); err != nil {
				return
			}
		case text := <-h.textCh:
			h.ver++
			params, _ := json.Marshal(map[string]interface{}{
				"textDocument": map[string]interface{}{
					"uri":     "file:///senpai-input.txt",
					"version": h.ver,
				},
				"contentChanges": []map[string]interface{}{
					{"text": text},
				},
			})
			msg, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "textDocument/didChange",
				"params":  json.RawMessage(params),
			})

			header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg))
			if _, err := h.stdin.Write(append([]byte(header), msg...)); err != nil {
				return
			}
			// Wait for the config response before accepting more text,
			// so we throttle to harper's processing speed.
			resp, ok := <-h.writes
			if !ok {
				return
			}

			if _, err := h.stdin.Write(resp); err != nil {
				return
			}
		}
	}
}

func (h *harperState) readMessage() ([]byte, error) {
	var contentLength int
	for {
		line, err := h.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = line[:len(line)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if line == "" {
			break
		}
		if len(line) > 16 && line[:16] == "Content-Length: " {
			contentLength, _ = strconv.Atoi(line[16:])
		}
	}
	if contentLength == 0 {
		return nil, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(h.stdout, body); err != nil {
		return nil, err
	}
	return body, nil
}

func utf16OffsetToRuneIndex(s string, utf16Offset int) int {
	runeIdx := 0
	utf16Pos := 0
	for _, r := range s {
		if utf16Pos >= utf16Offset {
			return runeIdx
		}
		utf16Pos += len(utf16.Encode([]rune{r}))
		runeIdx++
	}
	return runeIdx
}
