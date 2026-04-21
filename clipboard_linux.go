//go:build linux

package senpai

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type clipboardReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (r *clipboardReader) Close() error {
	err := r.ReadCloser.Close()
	if waitErr := r.cmd.Wait(); err == nil {
		err = waitErr
	}
	return err
}

func readClipboard() (io.ReadCloser, string, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return readClipboardWayland()
	}
	return readClipboardX11()
}

func startClipboardCmd(cmd *exec.Cmd) (io.ReadCloser, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &clipboardReader{ReadCloser: stdout, cmd: cmd}, nil
}

func readClipboardWayland() (io.ReadCloser, string, error) {
	out, err := exec.Command("wl-paste", "--list-types").Output()
	if err != nil {
		return nil, "", fmt.Errorf("listing clipboard types: %v", err)
	}
	var mimetype string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			mimetype = line
			break
		}
	}
	if mimetype == "" {
		return nil, "", fmt.Errorf("clipboard is empty")
	}
	rc, err := startClipboardCmd(exec.Command("wl-paste", "-t", mimetype))
	if err != nil {
		return nil, "", fmt.Errorf("reading clipboard: %v", err)
	}
	return rc, mimetype, nil
}

var x11SkipTargets = map[string]bool{
	"TARGETS":      true,
	"TIMESTAMP":    true,
	"MULTIPLE":     true,
	"SAVE_TARGETS": true,
}

func readClipboardX11() (io.ReadCloser, string, error) {
	out, err := exec.Command("xclip", "-selection", "clipboard", "-o", "-t", "TARGETS").Output()
	if err != nil {
		return nil, "", fmt.Errorf("listing clipboard types: %v", err)
	}
	var mimetype string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || x11SkipTargets[line] {
			continue
		}
		mimetype = line
		break
	}
	if mimetype == "" {
		return nil, "", fmt.Errorf("clipboard is empty")
	}
	rc, err := startClipboardCmd(exec.Command("xclip", "-selection", "clipboard", "-o", "-t", mimetype))
	if err != nil {
		return nil, "", fmt.Errorf("reading clipboard: %v", err)
	}
	return rc, mimetype, nil
}
