//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type userfileTestInfo struct {
	os.FileInfo
	stat any
}

func (info userfileTestInfo) Sys() any { return info.stat }

func TestServerUsersOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateUserFilePermissions(f, info); err != nil {
		t.Fatalf("current user's private file: %v", err)
	}
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid = uint32(os.Geteuid()) + 1
	if err := validateUserFilePermissions(f, userfileTestInfo{info, &stat}); err == nil {
		t.Fatal("accepted a private file owned by another user")
	}
	if err := validateUserFilePermissions(f, userfileTestInfo{info, nil}); err == nil {
		t.Fatal("accepted unavailable Unix ownership metadata")
	}
}
