//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	userfileAdvapi          = syscall.NewLazyDLL("advapi32.dll")
	userfileGetSecurityInfo = userfileAdvapi.NewProc("GetSecurityInfo")
	userfileGetAce          = userfileAdvapi.NewProc("GetAce")
)

type userfileACL struct {
	revision byte
	reserved byte
	size     uint16
	aceCount uint16
	unused   uint16
}

type userfileACEHeader struct {
	kind  byte
	flags byte
	size  uint16
}

func validateUserFilePermissions(f *os.File, _ os.FileInfo) error {
	// Windows FileMode reports synthetic 0666/0444 bits, not access authority.
	// Inspect the opened file's DACL, not its path or those synthetic bits.
	var dacl *userfileACL
	var descriptor uintptr
	status, _, _ := userfileGetSecurityInfo.Call(f.Fd(), 1, 4, 0, 0,
		uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&descriptor)))
	if status != 0 {
		return fmt.Errorf("read userfile security descriptor: %w", syscall.Errno(status))
	}
	defer syscall.LocalFree(syscall.Handle(descriptor))
	if dacl == nil {
		return errors.New("userfile must have a restricted access control list")
	}
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("read serving user identity: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read serving user identity: %w", err)
	}
	userSID, err := user.User.Sid.String()
	if err != nil {
		return err
	}
	for i := uint16(0); i < dacl.aceCount; i++ {
		var ace unsafe.Pointer
		ok, _, callErr := userfileGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace)))
		if ok == 0 {
			return fmt.Errorf("read userfile access rule: %w", callErr)
		}
		header := (*userfileACEHeader)(ace)
		if header.flags&0x08 != 0 || header.kind == 1 {
			// INHERIT_ONLY rules do not apply to this file; deny rules grant no access.
			continue
		}
		if header.kind != 0 || header.size < 16 {
			return errors.New("userfile has an unsupported access control rule")
		}
		mask := *(*uint32)(unsafe.Add(ace, 4))
		if mask == 0 {
			continue
		}
		sid, err := (*syscall.SID)(unsafe.Add(ace, 8)).String()
		if err != nil {
			return fmt.Errorf("read userfile access identity: %w", err)
		}
		// SYSTEM and local Administrators have privileged filesystem authority,
		// analogous to root on Unix. No other principal may receive access.
		if sid != userSID && sid != "S-1-5-18" && sid != "S-1-5-32-544" {
			return errors.New("userfile must not be accessible by other users or groups")
		}
	}
	return nil
}
