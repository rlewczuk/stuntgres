//go:build windows

package main

import (
	"syscall"
	"testing"
	"unsafe"
)

func setUserfileTestDACL(t *testing.T, path, rules string) {
	t.Helper()
	convert := userfileAdvapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	set := userfileAdvapi.NewProc("SetFileSecurityW")
	sddl, err := syscall.UTF16PtrFromString("D:P" + rules)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor uintptr
	ok, _, callErr := convert.Call(uintptr(unsafe.Pointer(sddl)), 1, uintptr(unsafe.Pointer(&descriptor)), 0)
	if ok == 0 {
		t.Fatalf("create test DACL: %v", callErr)
	}
	defer syscall.LocalFree(syscall.Handle(descriptor))
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Protect the DACL from inherited directory rules.
	ok, _, callErr = set.Call(uintptr(unsafe.Pointer(name)), 4|0x80000000, descriptor)
	if ok == 0 {
		t.Fatalf("install test DACL: %v", callErr)
	}
}

func privateUserfileTestRules(t *testing.T) string {
	t.Helper()
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		t.Fatal(err)
	}
	return "(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)"
}

func makePrivateUserFile(t *testing.T, path string) {
	t.Helper()
	setUserfileTestDACL(t, path, privateUserfileTestRules(t))
}

func checkInsecureUserFile(t *testing.T, path string) {
	t.Helper()
	private := privateUserfileTestRules(t)
	for _, permission := range []string{"FR", "FW", "FA"} {
		setUserfileTestDACL(t, path, private+"(A;;"+permission+";;;WD)")
		if _, err := loadAccounts(path, true); err == nil {
			t.Errorf("accepted Everyone access %s", permission)
		}
	}
	makePrivateUserFile(t, path)
	if _, err := loadAccounts(path, true); err != nil {
		t.Fatalf("private DACL restored: %v", err)
	}
}
