//go:build !linux

package main

import (
	"errors"
	"io"
	"os"
)

func userLock(path string) (func() error, error) {
	return nil, errors.New("password file locking is unsupported on this platform")
}

func disablePasswordEcho(in *os.File) (io.Reader, func() error, error) {
	info, err := in.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil, nil, errors.New("TTY password prompting is unsupported on this platform")
	}
	return in, func() error { return nil }, nil
}
