package main

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"os/user"
	"path"
	"strings"
	"syscall"
	"time"

	"git.sr.ht/~delthas/senpai"
)

func main() {
	var configPath string
	var nickname string
	var debug bool
	flag.StringVar(&configPath, "config", "", "path to the configuration file")
	flag.StringVar(&nickname, "nickname", "", "nick name/display name to use")
	flag.BoolVar(&debug, "debug", false, "show raw protocol data in the home buffer")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())

	if configPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		configPath = path.Join(configDir, "senpai", "senpai.scfg")
	}

	cfg, err := senpai.LoadConfigFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "failed to load the required configuration file at %q: %s\n", configPath, err)
			os.Exit(1)
			return
		}
		var host, port string
		tls := true
		var nick, password string
		fmt.Fprintf(os.Stderr, "The configuration file at %q was not found.\n", configPath)
		fmt.Fprintf(os.Stderr, "Configuration assistant: senpai will create a configuration file for you.\n\n")
		fmt.Fprintf(os.Stderr, "Important senpai information:\n")
		fmt.Fprintf(os.Stderr, "* senpai is able to connect to at most 1 server at a time.\n")
		fmt.Fprintf(os.Stderr, "* In order to connect to multiple networks, keep message history, search through your messages, and upload files, use an \x1B[1mIRC bouncer\x1B[0m and point senpai to the bouncer.\n")
		fmt.Fprintf(os.Stderr, "* Most senpai users use senpai with the IRC bouncer software \x1B[1msoju\x1B[0m.\n")
		fmt.Fprintf(os.Stderr, "** You can self-host \x1B[1msoju\x1B[0m yourself (it is free and open-source): https://soju.im/\n")
		fmt.Fprintf(os.Stderr, "** You can also use a commercial hosted bouncer (uses \x1B[1msoju\x1B[0m underneath), endorsed by senpai: \x1B[1;4mhttps://irctoday.com/\x1B[0m\n\n")
		fmt.Fprintf(os.Stderr, "Feel free to connect to your server now and configure a bouncer later to enable additional features.\n\n")
		fmt.Fprintf(os.Stderr, "Configuration assistant: Enter your server host (examples: irc.libera.chat, irctoday.com): ")
		for host == "" {
			fmt.Scanln(&host)
		}
		fmt.Fprintf(os.Stderr, "Configuration assistant: Enter your server port (examples: 6667, 6697) [optional]: ")
		fmt.Scanln(&port)
		fmt.Fprintf(os.Stderr, "Configuration assistant: Enter whether your server uses TLS (examples: yes, no) [optional, default: yes]: ")
		for {
			var tlsStr string
			fmt.Scanln(&tlsStr)
			if tlsStr == "" {
				break
			}
			switch strings.ToLower(tlsStr) {
			case "y", "yes":
				tls = true
			case "n", "no":
				tls = false
			default:
				continue
			}
			break
		}
		var defaultNick string
		if u, err := user.Current(); err == nil {
			defaultNick = u.Username
			if _, name, ok := strings.Cut(defaultNick, "\\"); ok {
				defaultNick = name
			}
			fmt.Fprintf(os.Stderr, "Configuration assistant: Enter your nickname [optional, default: %v]: ", defaultNick)
		} else {
			fmt.Fprintf(os.Stderr, "Configuration assistant: Enter your nickname: ")
		}
		fmt.Scanln(&nick)
		for defaultNick == "" && nick == "" {
			fmt.Scanln(&nick)
		}
		if nick == "" {
			nick = defaultNick
		}
		fmt.Fprintf(os.Stderr, "Configuration assistant: Enter your password (only enter if you already have an account) [optional]: ")
		fmt.Scanln(&password)

		folderPath := path.Dir(configPath)
		if err := os.MkdirAll(folderPath, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create the configuration file folder at %q: %s\n", folderPath, err)
			os.Exit(1)
			return
		}
		f, err := os.OpenFile(configPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create the configuration file at %q: %s\n", configPath, err)
			os.Exit(1)
			return
		}
		var addr string
		if !tls {
			addr += "irc+insecure://"
		}
		addr += host
		if port != "" {
			addr += ":" + port
		}
		fmt.Fprintf(f, "address %q\n", addr)
		fmt.Fprintf(f, "nickname %q\n", nick)
		if password != "" {
			fmt.Fprintf(f, "password %q\n", password)
		}
		f.Close()

		cfg, err = senpai.LoadConfigFile(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load the configuration file at %q: %s\n", configPath, err)
			os.Exit(1)
			return
		}

		fmt.Fprintf(os.Stderr, "Configuration assistant: Configuration saved to %q. Now starting.", configPath)
		for i := 0; i < 6; i++ {
			time.Sleep(500 * time.Millisecond)
			fmt.Fprintf(os.Stderr, ".")
		}
	}

	cfg.Debug = cfg.Debug || debug
	if nickname != "" {
		cfg.Nick = nickname
	}

	app, err := senpai.NewApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to run: %s\n", err)
		os.Exit(1)
		return
	}

	if !cfg.Transient {
		lastNetID, lastBuffer := getLastBuffer()
		app.SwitchToBuffer(lastNetID, lastBuffer)
		app.SetLastClose(getLastStamp())
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		<-sigCh
		app.Close()
	}()

	app.Run()
	app.Close()
	if !cfg.Transient {
		writeLastBuffer(app)
		writeLastStamp(app)
	}
}

func cachePath() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		panic(err)
	}
	cache := path.Join(cacheDir, "senpai")
	err = os.MkdirAll(cache, 0755)
	if err != nil {
		panic(err)
	}
	return cache
}

func lastBufferPath() string {
	return path.Join(cachePath(), "lastbuffer.txt")
}

func getLastBuffer() (netID, buffer string) {
	buf, err := os.ReadFile(lastBufferPath())
	if err != nil {
		return "", ""
	}

	fields := strings.SplitN(strings.TrimSpace(string(buf)), " ", 2)
	if len(fields) < 2 {
		return "", ""
	}

	return fields[0], fields[1]
}

func writeLastBuffer(app *senpai.App) {
	lastBufferPath := lastBufferPath()
	lastNetID, lastBuffer := app.CurrentBuffer()
	err := os.WriteFile(lastBufferPath, []byte(fmt.Sprintf("%s %s", lastNetID, lastBuffer)), 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to write last buffer at %q: %s\n", lastBufferPath, err)
	}
}

func lastStampPath() string {
	return path.Join(cachePath(), "laststamp.txt")
}

func getLastStamp() time.Time {
	buf, err := os.ReadFile(lastStampPath())
	if err != nil {
		return time.Time{}
	}

	stamp := strings.TrimSpace(string(buf))
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

func writeLastStamp(app *senpai.App) {
	lastStampPath := lastStampPath()
	last := app.LastMessageTime()
	if last.IsZero() {
		return
	}
	err := os.WriteFile(lastStampPath, []byte(last.UTC().Format(time.RFC3339Nano)), 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to write last stamp at %q: %s\n", lastStampPath, err)
	}
}
