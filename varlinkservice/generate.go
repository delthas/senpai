//go:build generate

package varlinkservice

import (
	_ "github.com/emersion/go-varlink/cmd/varlinkgen"
)

//go:generate go run github.com/emersion/go-varlink/cmd/varlinkgen -i fr.delthas.senpai.varlink
