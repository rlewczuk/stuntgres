package main

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestServerUsers(t *testing.T) {
	t.Run("Names", func(t *testing.T) {
		for _, name := range []string{"User", " spaced ", "a\"b", "数据库", strings.Repeat("a", 63)} {
			if err := validName(name, false); err != nil {
				t.Errorf("valid username %q: %v", name, err)
			}
		}
		for _, name := range []string{"", strings.Repeat("a", 64), strings.Repeat("é", 32), "\xff", "a\x00b", "a\nb", "a\rb", "a:b", "a,b"} {
			if validName(name, false) == nil || validName(name, true) == nil {
				t.Errorf("accepted invalid name %q", name)
			}
		}
		for _, name := range []string{".", "..", "a/b", "a\\b"} {
			if validName(name, true) == nil || validName(name, false) != nil {
				t.Errorf("incorrect database-only boundary %q", name)
			}
		}
		if err := validName("db--name", true); err != nil {
			t.Fatal(err)
		}
	})

	salt := bytes.Repeat([]byte{0x31}, 16)
	key, err := pbkdf2.Key(sha256.New, "test verifier secret", salt, 600000, 32)
	if err != nil {
		t.Fatal(err)
	}
	verifier := "$pbkdf2-sha256$i=600000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
	record := "Alice:" + verifier + ":db--name,数据库\n"
	t.Run("VerifierAndStrictRecords", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "users")
		put := func(text string) {
			t.Helper()
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		put(record)
		all, err := loadAccounts(path, false)
		if err != nil {
			t.Fatal(err)
		}
		a := all["Alice"]
		if len(all) != 1 || a.username != "Alice" || !bytes.Equal(a.salt, salt) || !bytes.Equal(a.key, key) || !reflect.DeepEqual(a.databases, []string{"db--name", "数据库"}) {
			t.Fatalf("parsed record does not preserve account metadata")
		}
		if !verifyPassword(a, "test verifier secret") || verifyPassword(a, "incorrect") || verifyPassword(a, "") || verifyPassword(a, "a\x00b") {
			t.Fatal("incorrect password verification")
		}
		if verifyPassword(account{salt: salt, key: key[:31]}, "test verifier secret") {
			t.Fatal("accepted short verifier")
		}
		malformed := []string{
			record + record, "\n", record + "\n", "Alice:" + verifier + ":\n",
			strings.Replace(record, "600000", "600001", 1),
			strings.Replace(record, "pbkdf2-sha256", "md5", 1),
			strings.Replace(record, "$i=", "$i=0", 1),
			strings.Replace(record, base64.RawStdEncoding.EncodeToString(salt), "!!!!", 1),
			strings.Replace(record, base64.RawStdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(salt), 1),
			strings.Replace(record, base64.RawStdEncoding.EncodeToString(key), base64.RawStdEncoding.EncodeToString(key[:31]), 1),
			"Alice:" + verifier + ":db,db\n", "Alice:" + verifier + ":db,\n",
			"Alice:" + verifier + ":../db\n", "Alice:" + verifier + ":db:extra\n",
			strings.Replace(record, "\n", "\r\n", 1), strings.Replace(record, "Alice", "Al\x00ice", 1),
		}
		for i, text := range malformed {
			put(text)
			if _, err := loadAccounts(path, false); err == nil {
				t.Errorf("malformed record case %d accepted", i)
			}
		}
		put(strings.TrimSuffix(record, "\n"))
		if _, err := loadAccounts(path, false); err != nil {
			t.Fatalf("final record without LF: %v", err)
		}
		if _, err := loadAccounts(t.TempDir(), false); err == nil {
			t.Fatal("accepted directory as userfile")
		}
	})

	t.Run("SecurePermissions", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "users")
		if err := os.WriteFile(path, []byte(record), 0600); err != nil {
			t.Fatal(err)
		}
		makePrivateUserFile(t, path)
		if _, err := loadAccounts(path, true); err != nil {
			t.Fatalf("private userfile admission: %v", err)
		}
		checkInsecureUserFile(t, path)
	})

	t.Run("AtomicUpdateAndPreservation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "users")
		if runtime.GOOS != "linux" {
			if err := updateAccount(path, "Alice", "first", []string{"one"}, true); err == nil {
				t.Fatal("unsupported lock returned success")
			}
			return
		}
		if err := updateAccount(path, "Alice", "first", nil, false); err == nil {
			t.Fatal("new user without grants accepted")
		}
		if err := updateAccount(path, "Alice", "first", []string{"one", "Two"}, true); err != nil {
			t.Fatal(err)
		}
		old, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		oldInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		lockInfo, err := os.Stat(path + ".lock")
		if err != nil {
			t.Fatal(err)
		}
		if err := updateAccount(path, "Alice", "second", []string{"ignored"}, false); err != nil {
			t.Fatal(err)
		}
		all, err := loadAccounts(path, true)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(all["Alice"].databases, []string{"one", "Two"}) || !verifyPassword(all["Alice"], "second") || verifyPassword(all["Alice"], "first") {
			t.Fatal("password change failed to preserve grants or replace verifier")
		}
		newInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		newLockInfo, err := os.Stat(path + ".lock")
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(oldInfo, newInfo) || !os.SameFile(lockInfo, newLockInfo) || newInfo.Mode().Perm() != 0600 {
			t.Fatal("publication must replace userfile inode, preserve lock inode, and use mode 0600")
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for _, name := range []string{"Bob", "Carol"} {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				errors <- updateAccount(path, name, "concurrent secret", []string{"one"}, true)
			}(name)
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := updateAccount(path, "Alice", "third", []string{"replacement"}, true); err != nil {
			t.Fatal(err)
		}
		all, err = loadAccounts(path, true)
		if err != nil || len(all) != 3 || !reflect.DeepEqual(all["Alice"].databases, []string{"replacement"}) {
			t.Fatalf("explicit replacement or concurrent account preservation failed: %v", err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, grants := range [][]string{nil, {""}, {"one", "one"}, {"../escape"}} {
			if err := updateAccount(path, "Alice", "fourth", grants, true); err == nil {
				t.Fatal("accepted invalid explicit grants")
			}
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("failed update changed published accounts")
		}
		if bytes.Equal(old, after) {
			t.Fatal("updates did not change account file")
		}
		malformed := []byte("malformed\n")
		if err := os.WriteFile(path, malformed, 0600); err != nil {
			t.Fatal(err)
		}
		if err := updateAccount(path, "Alice", "fifth", []string{"one"}, true); err == nil {
			t.Fatal("overwrote malformed existing file")
		}
		after, err = os.ReadFile(path)
		if err != nil || !bytes.Equal(after, malformed) {
			t.Fatal("malformed file was discarded")
		}
		files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".stuntgres-users-*"))
		if err != nil || len(files) != 0 {
			t.Fatal("temporary account files leaked")
		}
	})

	t.Run("PasswordPrompts", func(t *testing.T) {
		for i, test := range []struct {
			input string
			ok    bool
		}{
			{"matching\nmatching\n", true}, {"Unicode 密码\nUnicode 密码", true},
			{"first\nsecond\n", false}, {"\n\n", false}, {"one\n", false},
			{"a\x00b\na\x00b\n", false}, {"a\rb\na\rb\n", false}, {"one\r\none\r\n", false},
		} {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "stdin")
				if err := os.WriteFile(path, []byte(test.input), 0600); err != nil {
					t.Fatal(err)
				}
				in, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer in.Close()
				var output bytes.Buffer
				password, err := readPasswordPair(in, &output)
				if (err == nil) != test.ok || (!test.ok && password != "") {
					t.Fatalf("prompt result mismatch: %v", err)
				}
				if test.ok && (password != strings.Split(test.input, "\n")[0] || output.String() != "Password: \nRepeat password: \n") {
					t.Fatal("prompt returned altered password or emitted secret")
				}
			})
		}
		path := filepath.Join(t.TempDir(), "stdin")
		if err := os.WriteFile(path, []byte("one\none\n"), 0600); err != nil {
			t.Fatal(err)
		}
		in, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		if password, err := readPasswordPair(in, serverUsersFailWriter{}); err == nil || password != "" {
			t.Fatal("ignored prompt I/O error")
		}
	})
}

type serverUsersFailWriter struct{}

func (serverUsersFailWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
