package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerUsersPasswordPrompts(t *testing.T) {
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
