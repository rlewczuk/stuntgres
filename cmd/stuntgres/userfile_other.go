//go:build !unix && !windows

package main

import (
	"errors"
	"os"
)

func validateUserFilePermissions(_ *os.File, info os.FileInfo) error {
	// These targets expose native file permissions but no POSIX owner identity.
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("userfile must not be accessible by group or others")
	}
	return nil
}
