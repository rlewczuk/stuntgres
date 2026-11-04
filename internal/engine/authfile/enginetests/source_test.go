package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authfile"
	"stuntgres/internal/sqlstate"
	"syscall"
	"testing"
)

func put(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
func TestAuthFileTokenizer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth")
	for _, tc := range []struct {
		input  string
		tokens []authfile.Token
	}{
		{`"all",ab"cd", "a""b"`, []authfile.Token{{Text: "all", InitiallyQuoted: true}, {Text: "abcd"}, {Text: `a"b`, InitiallyQuoted: true}}},
		{`"" x`, []authfile.Token{{Text: "", InitiallyQuoted: true}}},
		{"a\v\f\u00a0 b", []authfile.Token{{Text: "a\v\f\u00a0"}}},
	} {
		put(t, path, tc.input)
		records, err := authfile.Read(t.Context(), path)
		if err != nil || len(records) != 1 || !reflect.DeepEqual(records[0].Fields[0], tc.tokens) {
			t.Fatal(tc, records, err)
		}
	}
	large := strings.Repeat("x", 128*1024)
	put(t, path, "one\r\r\nsecond\r\\\n\nthird a\\\nb\nfourth "+large+"\n")
	records, err := authfile.Read(t.Context(), path)
	if err != nil || len(records) != 4 || records[1].Line != 2 || records[1].Fields[0][0].Text != "second" || records[2].Line != 4 || records[2].Fields[1][0].Text != "ab" || len(records[3].Fields[1][0].Text) != len(large) {
		t.Fatal(records, err)
	}
	// A directory's stream read failure is fatal, never an invented line.
	directory := t.TempDir()
	if records, err := authfile.Read(t.Context(), directory); records != nil || err == nil {
		t.Fatal(records, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if records, err := authfile.Read(ctx, path); records != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(records, err)
	}
}
func TestAuthFileIncludes(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	put(t, filepath.Join(root, "names with spaces"), `"all" plain`)
	put(t, filepath.Join(root, "child"), "child one\n")
	put(t, filepath.Join(root, "dir", "A.conf"), "upper\n")
	put(t, filepath.Join(root, "dir", "a.conf"), "lower\n")
	put(t, filepath.Join(root, "dir", ".hidden.conf"), "wrong\n")
	put(t, main, "include child\ninclude_if_exists absent\ninclude_dir dir\n@\"names with spaces\" x\n\"@literal\" y\n")
	records, err := authfile.Read(t.Context(), main)
	if err != nil || len(records) != 5 {
		t.Fatal(records, err)
	}
	if records[0].File != filepath.Join(root, "child") || records[1].Fields[0][0].Text != "upper" || records[2].Fields[0][0].Text != "lower" || !records[3].Fields[0][0].InitiallyQuoted || records[3].Fields[0][1].InitiallyQuoted || records[4].Fields[0][0].Text != "@literal" {
		t.Fatal(records)
	}
	put(t, filepath.Join(root, "other", "rule"), "selected\n")
	if err := os.MkdirAll(filepath.Join(root, "other", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other/sub", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	absolute := root + "/link/../rule"
	put(t, main, "include "+absolute+"\n")
	records, err = authfile.Read(t.Context(), main)
	if err != nil || len(records) != 1 || records[0].File != absolute || records[0].Fields[0][0].Text != "selected" {
		t.Fatal(records, err)
	}
	for _, name := range []string{"a.conf", "b.conf", "c.conf"} {
		put(t, filepath.Join(root, "failures", name), "selected\n")
	}
	for _, name := range []string{"a.conf", "c.conf"} {
		if err := os.Chmod(filepath.Join(root, "failures", name), 0); err != nil {
			t.Fatal(err)
		}
	}
	put(t, main, "include_dir failures\n")
	records, err = authfile.Read(t.Context(), main)
	first := fmt.Sprintf("could not open file \"%s\": Permission denied", filepath.Join(root, "failures", "a.conf"))
	last := fmt.Sprintf("could not open file \"%s\": Permission denied", filepath.Join(root, "failures", "c.conf"))
	if err != nil || len(records) != 2 || records[0].File != filepath.Join(root, "failures", "b.conf") || records[1].Diagnostic != first+"\n"+first+"\n"+last {
		t.Fatal(records, err)
	}
	for i := range 11 {
		data := "leaf\n"
		if i < 10 {
			data = fmt.Sprintf("include f%d\n", i+1)
		}
		put(t, filepath.Join(root, fmt.Sprintf("f%d", i)), data)
	}
	records, err = authfile.Read(t.Context(), filepath.Join(root, "f0"))
	if err != nil || len(records) != 1 || records[0].File != filepath.Join(root, "f10") {
		t.Fatal(records, err)
	}
	put(t, filepath.Join(root, "f10"), "include_if_exists f11\n")
	records, err = authfile.Read(t.Context(), filepath.Join(root, "f0"))
	if err != nil || len(records) != 1 || !strings.Contains(records[0].Diagnostic, "maximum nesting depth exceeded") {
		t.Fatal(records, err)
	}
}
func TestAuthFileErrorTranslation(t *testing.T) {
	for _, tc := range []struct {
		errno syscall.Errno
		code  string
	}{{syscall.ENOENT, "58P01"}, {syscall.EACCES, "42501"}, {syscall.EPERM, "42501"}, {syscall.EROFS, "42501"}, {syscall.EISDIR, "42809"}, {syscall.ENOTDIR, "42809"}, {syscall.ENOTEMPTY, "42809"}, {syscall.EMFILE, "53000"}, {syscall.ENFILE, "53000"}, {syscall.EEXIST, "58P02"}, {syscall.ENOSPC, "53100"}, {syscall.ENOMEM, "53200"}, {syscall.EIO, "58030"}, {syscall.ENAMETOOLONG, "58P03"}, {syscall.EINVAL, "XX000"}} {
		err := authfile.FileError("open", "/actual", &os.PathError{Op: "open", Path: "/actual", Err: tc.errno})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || !strings.HasPrefix(diagnostic.Message, `could not open file "/actual": `) {
			t.Fatal(tc, err)
		}
	}
}
