//go:build !linux

package serverauth

import "errors"

func userLock(path string) (func() error, error) {
	return nil, errors.New("password file locking is unsupported on this platform")
}
