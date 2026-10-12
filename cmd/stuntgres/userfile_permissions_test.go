//go:build !windows

package main

import (
	"os"
	"testing"
)

func makePrivateUserFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
}

func checkInsecureUserFile(t *testing.T, path string) {
	t.Helper()
	for _, mode := range []os.FileMode{0640, 0604, 0666} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := loadAccounts(path, true); err == nil {
			t.Errorf("accepted insecure mode %o", mode)
		}
	}
}
