//go:build !linux

package sandbox

import (
	"context"
	"io"
)

type localTerminal struct{}

func localTerminalSize(io.Reader) (uint, uint, bool) { return 0, 0, false }

func openLocalTerminal(io.Reader, bool, func(uint, uint) error) (*localTerminal, error) {
	return nil, nil
}

func (*localTerminal) copyTo(context.Context, io.Writer) error { return nil }
func (*localTerminal) Close() error                            { return nil }
