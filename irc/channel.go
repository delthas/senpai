package irc

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

// special internal Message commands to propagate labeled response status to the writing goroutine
var labelEnableCommand = ":enable_labeled_response"
var labelDisableCommand = ":disable_labeled_response"

const chanCapacity = 64

func ChanInOut(conn net.Conn) (in <-chan Message, out chan<- Message) {
	in_ := make(chan Message, chanCapacity)
	out_ := make(chan Message, chanCapacity)

	const keepAlive = 30 * time.Second
	const maxRTT = 10 * time.Second
	var last atomic.Value
	last.Store(time.Now())

	go func() {
		r := bufio.NewScanner(conn)
		for r.Scan() {
			line := r.Text()
			line = strings.ToValidUTF8(line, string([]rune{unicode.ReplacementChar}))
			msg, err := ParseMessage(line)
			if err != nil {
				continue
			}
			now := time.Now()
			last.Store(now)
			conn.SetReadDeadline(now.Add(keepAlive + maxRTT))
			in_ <- msg
		}
		close(in_)
	}()

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		labelOff := 1
		labeledResponse := false
	outer:
		for {
			select {
			case msg, ok := <-out_:
				if !ok {
					break outer
				}
				if msg.Command == labelEnableCommand {
					labeledResponse = true
					continue
				}
				if msg.Command == labelDisableCommand {
					labeledResponse = false
					continue
				}
				if labeledResponse {
					label := strconv.Itoa(labelOff)
					labelOff++
					if msg.Tags == nil {
						msg.Tags = map[string]string{"label": label}
					} else {
						msg.Tags["label"] = label
					}
				}

				last.Store(time.Now())
				// TODO send messages by batches
				_, err := fmt.Fprintf(conn, "%s\r\n", msg.String())
				if err != nil {
					break outer
				}
			case <-t.C:
				now := time.Now()
				if last.Load().(time.Time).Add(keepAlive).After(now) {
					continue
				}
				if last.Load().(time.Time).Add(keepAlive + maxRTT).Before(now) {
					// probably out of sleep, reset connection
					conn.Close()
					continue
				}
				last.Store(now)
				_, err := fmt.Fprint(conn, "PING _\r\n")
				if err != nil {
					break outer
				}
			}
		}
		_ = conn.Close()
	}()

	return in_, out_
}
