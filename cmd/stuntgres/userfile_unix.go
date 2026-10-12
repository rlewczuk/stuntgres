//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

func validateUserFilePermissions(_ *os.File, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("userfile must not be accessible by group or others")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("userfile must be owned by the serving user")
	}
	return nil
}
