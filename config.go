package senpai

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"git.sr.ht/~delthas/senpai/ui"

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
	ok := true
	switch s {
	case "black":
		*c = tcell.ColorBlack
	case "maroon":
		*c = tcell.ColorBrown
	case "green":
		*c = tcell.ColorGreen
	case "olive":
		*c = tcell.ColorOlive
	case "navy":
		*c = tcell.ColorNavy
	case "purple":
		*c = tcell.ColorPurple
	case "teal":
		*c = tcell.ColorTeal
	case "silver":
		*c = tcell.ColorSilver
	case "gray", "grey":
		*c = tcell.ColorGray
	case "red":
		*c = tcell.ColorRed
	case "lime":
		*c = tcell.ColorLime
	case "yellow":
		*c = tcell.ColorYellow
	case "blue":
		*c = tcell.ColorBlue
	case "fuschia":
		*c = tcell.ColorFuchsia
	case "aqua":
		*c = tcell.ColorAqua
	case "white":
		*c = tcell.ColorWhite
	default:
		ok = false
	}
	if ok {
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

type Config struct {
	Addr          string
	Nick          string
	Real          string
	User          string
	Password      *string
	TLS           bool
	TLSSkipVerify bool

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
	StatusEnabled    bool

	Colors ui.ConfigColors

	Debug     bool
	Transient bool
}

func DefaultHighlightPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return path.Join(configDir, "senpai", "highlight"), nil
}

func Defaults() Config {
	return Config{
		Addr:             "",
		Nick:             "",
		Real:             "",
		User:             "",
		Password:         nil,
		TLS:              true,
		TLSSkipVerify:    false,
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
		StatusEnabled:    true,
		Colors: ui.ConfigColors{
			Status: tcell.ColorGray,
			Prompt: tcell.ColorDefault,
			Unread: tcell.ColorDefault,
			Nicks: ui.ColorScheme{
				Type:   ui.ColorSchemeBase,
				Others: tcell.ColorDefault,
				Self:   tcell.ColorRed,
			},
		},
		Debug: false,
	}
}

func ParseAddr(addr string, cfg *Config) error {
	if addr == "" {
		return nil
	}
	if !strings.Contains(addr, "://") {
		addr = "irc://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "ircs+insecure":
		cfg.TLS = true
		cfg.TLSSkipVerify = true
	case "ircs":
		cfg.TLS = true
	case "irc+insecure":
		cfg.TLS = false
	case "irc":
		// Could be TLS or plaintext, keep TLS as is.
	default:
		return fmt.Errorf("invalid IRC addr scheme: %v", addr)
	}
	if u := u.User; u != nil {
		cfg.User = u.Username()
		if p, ok := u.Password(); ok {
			cfg.Password = &p
		} else {
			cfg.Password = nil
		}
	}
	cfg.Addr = u.Host
	target, _, _ := strings.Cut(strings.TrimLeft(u.Path, "/"), "/")
	if target != "" {
		cfg.Channels = []string{target}
	}
	return nil
}

func LoadConfigFile(filename string) (Config, error) {
	cfg := Defaults()

	err := unmarshal(filename, &cfg)
	if err != nil {
		return Config{}, err
	}
	if err := ParseAddr(cfg.Addr, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func unmarshal(filename string, cfg *Config) (err error) {
	directives, err := scfg.Load(filename)
	if err != nil {
		return fmt.Errorf("error parsing scfg: %w", err)
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
				return fmt.Errorf("error running password command: %v", err)
			}

			passCmdOut := strings.Split(string(stdout), "\n")
			if len(passCmdOut) < 1 || strings.TrimSpace(passCmdOut[0]) == "" {
				return fmt.Errorf("password command returned no data")
			}
			cfg.Password = &passCmdOut[0]
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

				switch child.Name {
				case "nicks":
					switch colorStr {
					case "base":
						cfg.Colors.Nicks.Type = ui.ColorSchemeBase
					case "extended":
						cfg.Colors.Nicks.Type = ui.ColorSchemeExtended
					case "fixed":
						cfg.Colors.Nicks.Type = ui.ColorSchemeFixed
						if len(child.Params) >= 2 {
							if err = parseColor(child.Params[1], &cfg.Colors.Nicks.Others); err != nil {
								return err
							}
						}
						if len(child.Params) >= 3 {
							if err = parseColor(child.Params[2], &cfg.Colors.Nicks.Self); err != nil {
								return err
							}
						}
					default:
						return fmt.Errorf("unknown nick color scheme %q", colorStr)
					}
					continue
				case "status":
					if colorStr == "disabled" {
						cfg.StatusEnabled = false
						continue
					}
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
				case "status":
					cfg.Colors.Status = color
				default:
					return fmt.Errorf("unknown colors directive %q", child.Name)
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
		case "transient":
			var transient string
			if err := d.ParseParams(&transient); err != nil {
				return err
			}

			if cfg.Transient, err = strconv.ParseBool(transient); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown directive %q", d.Name)
		}
	}

	return
}
