package senpai

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"git.sr.ht/~emersion/go-scfg"
)

func parseColor(s string, c *tcell.Color) error {
	if strings.HasPrefix(s, "#") {
		hex, err := strconv.ParseInt(s[1:], 16, 32)
		if err != nil {
			return err
		}

		*c = tcell.NewHexColor(int32(hex))
		return nil
	}

	code, err := strconv.Atoi(s)
	if err != nil {
		return err
	}

	if code == -1 {
		*c = tcell.ColorDefault
		return nil
	}

	if code < 0 || code > 255 {
		return fmt.Errorf("color code must be between 0-255. If you meant to use true colors, use #aabbcc notation")
	}

	*c = tcell.PaletteColor(code)

	return nil
}

type ConfigColors struct {
	Prompt tcell.Color
	Unread tcell.Color
}

type Config struct {
	Addr     string
	Nick     string
	Real     string
	User     string
	Password *string
	TLS      bool
	Channels []string

	Typings bool
	Mouse   bool

	Highlights       []string
	OnHighlightPath  string
	OnHighlightBeep  bool
	NickColWidth     int
	ChanColWidth     int
	ChanColEnabled   bool
	MemberColWidth   int
	MemberColEnabled bool
	TextMaxWidth     int

	Colors ConfigColors

	Debug bool
}

func DefaultHighlightPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return path.Join(configDir, "senpai", "highlight"), nil
}

func Defaults() (cfg Config, err error) {
	cfg = Config{
		Addr:             "",
		Nick:             "",
		Real:             "",
		User:             "",
		Password:         nil,
		TLS:              true,
		Channels:         nil,
		Typings:          true,
		Mouse:            true,
		Highlights:       nil,
		OnHighlightPath:  "",
		OnHighlightBeep:  false,
		NickColWidth:     14,
		ChanColWidth:     16,
		ChanColEnabled:   true,
		MemberColWidth:   16,
		MemberColEnabled: true,
		TextMaxWidth:     0,
		Colors: ConfigColors{
			Prompt: tcell.ColorDefault,
			Unread: tcell.ColorDefault,
		},
		Debug: false,
	}

	return
}

func LoadConfigFile(filename string) (cfg Config, err error) {
	cfg, err = Defaults()
	if err != nil {
		return
	}

	err = unmarshal(filename, &cfg)
	if err != nil {
		return cfg, err
	}
	if cfg.Addr == "" {
		return cfg, errors.New("addr is required")
	}
	if cfg.Nick == "" {
		return cfg, errors.New("nick is required")
	}
	if cfg.User == "" {
		cfg.User = cfg.Nick
	}
	if cfg.Real == "" {
		cfg.Real = cfg.Nick
	}
	return
}

func unmarshal(filename string, cfg *Config) (err error) {
	directives, err := scfg.Load(filename)
	if err != nil {
		return fmt.Errorf("error parsing scfg: %s", err)
	}

	for _, d := range directives {
		switch d.Name {
		case "address":
			if err := d.ParseParams(&cfg.Addr); err != nil {
				return err
			}
		case "nickname":
			if err := d.ParseParams(&cfg.Nick); err != nil {
				return err
			}
		case "username":
			if err := d.ParseParams(&cfg.User); err != nil {
				return err
			}
		case "realname":
			if err := d.ParseParams(&cfg.Real); err != nil {
				return err
			}
		case "password":
			// if a password-cmd is provided, don't use this value
			if directives.Get("password-cmd") != nil {
				continue
			}

			var password string
			if err := d.ParseParams(&password); err != nil {
				return err
			}
			cfg.Password = &password
		case "password-cmd":
			var cmdName string
			if err := d.ParseParams(&cmdName); err != nil {
				return err
			}

			cmd := exec.Command(cmdName, d.Params[1:]...)
			var stdout []byte
			if stdout, err = cmd.Output(); err != nil {
				return fmt.Errorf("error running password command: %s", err)
			}

			passCmdOut := strings.Split(string(stdout), "\n")
			if len(passCmdOut) >= 1 {
				cfg.Password = &passCmdOut[0]
			}
		case "channel":
			// TODO: does this work with soju.im/bouncer-networks extension?
			cfg.Channels = append(cfg.Channels, d.Params...)
		case "highlight":
			cfg.Highlights = append(cfg.Highlights, d.Params...)
		case "on-highlight-path":
			if err := d.ParseParams(&cfg.OnHighlightPath); err != nil {
				return err
			}
		case "on-highlight-beep":
			var onHighlightBeep string
			if err := d.ParseParams(&onHighlightBeep); err != nil {
				return err
			}

			if cfg.OnHighlightBeep, err = strconv.ParseBool(onHighlightBeep); err != nil {
				return err
			}
		case "pane-widths":
			for _, child := range d.Children {
				switch child.Name {
				case "nicknames":
					var nicknames string
					if err := child.ParseParams(&nicknames); err != nil {
						return err
					}

					if cfg.NickColWidth, err = strconv.Atoi(nicknames); err != nil {
						return err
					}
				case "channels":
					var channelsStr string
					if err := child.ParseParams(&channelsStr); err != nil {
						return err
					}
					channels, err := strconv.Atoi(channelsStr)
					if err != nil {
						return err
					}
					if channels <= 0 {
						cfg.ChanColEnabled = false
						if channels < 0 {
							cfg.ChanColWidth = -channels
						}
					} else {
						cfg.ChanColWidth = channels
					}
				case "members":
					var membersStr string
					if err := child.ParseParams(&membersStr); err != nil {
						return err
					}
					members, err := strconv.Atoi(membersStr)
					if err != nil {
						return err
					}
					if members <= 0 {
						cfg.MemberColEnabled = false
						if members < 0 {
							cfg.MemberColWidth = -members
						}
					} else {
						cfg.MemberColWidth = members
					}
				case "text":
					var text string
					if err := child.ParseParams(&text); err != nil {
						return err
					}

					if cfg.TextMaxWidth, err = strconv.Atoi(text); err != nil {
						return err
					}
				default:
					return fmt.Errorf("unknown directive %q", child.Name)
				}
			}
		case "tls":
			var tls string
			if err := d.ParseParams(&tls); err != nil {
				return err
			}

			if cfg.TLS, err = strconv.ParseBool(tls); err != nil {
				return err
			}
		case "typings":
			var typings string
			if err := d.ParseParams(&typings); err != nil {
				return err
			}

			if cfg.Typings, err = strconv.ParseBool(typings); err != nil {
				return err
			}
		case "mouse":
			var mouse string
			if err := d.ParseParams(&mouse); err != nil {
				return err
			}

			if cfg.Mouse, err = strconv.ParseBool(mouse); err != nil {
				return err
			}
		case "colors":
			for _, child := range d.Children {
				var colorStr string
				if err := child.ParseParams(&colorStr); err != nil {
					return err
				}

				var color tcell.Color
				if err = parseColor(colorStr, &color); err != nil {
					return err
				}
				switch child.Name {
				case "prompt":
					cfg.Colors.Prompt = color
				case "unread":
					cfg.Colors.Unread = color
				default:
					return fmt.Errorf("unknown directive %q", child.Name)
				}
			}
		case "debug":
			var debug string
			if err := d.ParseParams(&debug); err != nil {
				return err
			}

			if cfg.Debug, err = strconv.ParseBool(debug); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown directive %q", d.Name)
		}
	}

	return
}
