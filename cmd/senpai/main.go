package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"git.sr.ht/~delthas/senpai"
	"git.sr.ht/~delthas/senpai/varlinkservice"
	"github.com/emersion/go-varlink"
)

func main() {
	var configPath string
	var nickname string
	var debug bool
	var version bool
	flag.StringVar(&configPath, "config", "", "path to the configuration file")
	flag.StringVar(&nickname, "nickname", "", "nick name/display name to use")
	flag.BoolVar(&debug, "debug", false, "show raw protocol data in the home buffer")
	flag.BoolVar(&version, "version", false, "show version info")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())

	if version {
		if v, ok := senpai.BuildVersion(); ok {
			fmt.Printf("senpai version %v\n", v)
		} else {
			fmt.Printf("senpai (unknown version)\n")
		}
		return
	}

	socketDir := os.Getenv("XDG_RUNTIME_DIR")
	if socketDir == "" {
		socketDir = os.TempDir()
	}
	socketDir = filepath.Join(socketDir, "senpai")

	var link string
	if _, err := url.Parse(flag.Arg(0)); err == nil {
		link = flag.Arg(0)
	}
	if link != "" {
		ok, err := sendOpenLink(socketDir, link)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to send open link to another instance: %v\n", err)
		} else if ok {
			// Sent link open command to another instance, exit.
			return
		}
	}

	var previousConfigPath string
	if configPath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		if runtime.GOOS == "darwin" {
			if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && dir != configDir {
				previousConfigPath = configDir
				configDir = dir
			}
		}
		configPath = path.Join(configDir, "senpai", "senpai.scfg")
		if previousConfigPath != "" {
			previousConfigPath = path.Join(previousConfigPath, "senpai", "senpai.scfg")
		}
	}

	cfg, err := senpai.LoadConfigFile(configPath)
	if err != nil && errors.Is(err, os.ErrNotExist) && previousConfigPath != "" {
		var ee error
		cfg, ee = senpai.LoadConfigFile(previousConfigPath)
		if ee == nil {
			err = nil
			fmt.Fprintf(os.Stderr, "Configuration assistant: Previous configuration file found at %q; the new default configuration location is now %q\n", previousConfigPath, configPath)
			fmt.Fprintf(os.Stderr, "Configuration assistant: You can run senpai with an explicit -config path argument, or move the file to its new location.\n")
			fmt.Fprintf(os.Stderr, "Configuration assistant: Would you like senpai to move the configuration file to the new location? (examples: yes, no) [optional, default: yes]: ")
			move := true
			scanBool(&move)
			if move {
				if err := copyContents(configPath, previousConfigPath); err != nil {
					fmt.Fprintf(os.Stderr, "failed to move the configuration file: %v\n", err)
					os.Exit(1)
					return
				}
				if err := os.Remove(previousConfigPath); err != nil {
					fmt.Fprintf(os.Stderr, "failed to delete the previous configuration file at %q: %v\n", previousConfigPath, err)
					os.Exit(1)
					return
				}
			}
		}
	}
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
		scanBool(&tls)
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
		fmt.Fprintf(os.Stderr, "\n")
	}

	cfg.OpenLink = link
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

	if cfg.LocalIntegrations {
		socketPath := filepath.Join(socketDir, fmt.Sprintf("%v.sock", os.Getpid()))
		if err := listenVarlink(socketPath, app); err != nil {
			fmt.Fprintf(os.Stderr, "failed to send open link to another instance: %v\n", err)
		}
		defer os.Remove(socketPath)
	}

	app.Run()
	app.Close()
	if !cfg.Transient {
		writeLastBuffer(app)
		writeLastStamp(app)
	}
}

func scanBool(v *bool) {
	for {
		var s string
		fmt.Scanln(&s)
		if s == "" {
			return
		}
		switch strings.ToLower(s) {
		case "y", "yes":
			*v = true
			return
		case "n", "no":
			*v = false
			return
		}
	}
}

func copyContents(dstPath string, srcPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %q: %v", srcPath, err)
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create %q: %v", dstPath, err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy %q to %q: %v", srcPath, dstPath, err)
	}
	return nil
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

func sendOpenLink(socketDir string, link string) (ok bool, err error) {
	es, err := os.ReadDir(socketDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range es {
		if filepath.Ext(e.Name()) != ".sock" {
			continue
		}
		conn, err := net.Dial("unix", filepath.Join(socketDir, e.Name()))
		if err != nil {
			return false, err
		}
		c := varlinkservice.Client{Client: varlink.NewClient(conn)}
		defer c.Close()
		if _, err := c.OpenLink(&varlinkservice.OpenLinkIn{Link: link}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func listenVarlink(socketPath string, backend varlinkservice.Backend) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return err
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	server := varlink.NewServer()
	server.Handler = varlinkservice.Handler{
		Backend: backend,
	}
	go func() {
		if err := server.Serve(l); err != nil {
			fmt.Fprintf(os.Stderr, "failed to serve varlink server: %v\n", err)
		}
	}()
	return nil
}
