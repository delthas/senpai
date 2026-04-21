//go:build !linux

package senpai

import (
	"fmt"
	"io"
)

func readClipboard() (io.ReadCloser, string, error) {
	return nil, "", fmt.Errorf("clipboard access is not supported on this platform")
}
