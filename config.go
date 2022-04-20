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

type Color tcell.Color

func parseColor(s string, c *Color) error {
	if strings.HasPrefix(s, "#") {
		hex, err := strconv.ParseInt(s[1:], 16, 32)
		if err != nil {
			return err
		}

		*c = Color(tcell.NewHexColor(int32(hex)))
		return nil
	}

	code, err := strconv.Atoi(s)
	if err != nil {
		return err
	}

	if code == -1 {
		*c = Color(tcell.ColorDefault)
		return nil
	}

	if code < 0 || code > 255 {
		return fmt.Errorf("color code must be between 0-255. If you meant to use true colors, use #aabbcc notation")
	}

	*c = Color(tcell.PaletteColor(code))

	return nil
}

type ConfigColors struct {
	Prompt Color
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
	NickColWidth     int
	ChanColWidth     int
	ChanColEnabled   bool
	MemberColWidth   int
	MemberColEnabled bool

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
		NickColWidth:     14,
		ChanColWidth:     16,
		ChanColEnabled:   true,
		MemberColWidth:   16,
		MemberColEnabled: true,
		Colors: ConfigColors{
			Prompt: Color(tcell.ColorDefault),
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
					if channels == 0 {
						cfg.ChanColEnabled = false
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
					if members == 0 {
						cfg.MemberColEnabled = false
					} else {
						cfg.MemberColWidth = members
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
				switch child.Name {
				case "prompt":
					var prompt string
					if err := child.ParseParams(&prompt); err != nil {
						return err
					}

					if err = parseColor(prompt, &cfg.Colors.Prompt); err != nil {
						return err
					}
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
