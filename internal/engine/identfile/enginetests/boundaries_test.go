package enginetests_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"stuntgres/internal/engine/identfile"
)

func TestIdentFileParserAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "ident")
	write(t, filepath.Join(root, "child.conf"), "child s p\n")
	write(t, filepath.Join(root, "rule.conf"), "wrong s p\n")
	write(t, filepath.Join(root, "other", "rule.conf"), "selected s p\n")
	write(t, filepath.Join(root, "token"), "wrong_token\n")
	write(t, filepath.Join(root, "other", "token"), "selected_token\n")
	if err := os.Mkdir(filepath.Join(root, "other", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other/sub", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	child := root + "/./child.conf"
	selected := root + "/link/../rule.conf"
	missing := root + "/link/../missing"
	write(t, main, "include "+child+"\ninclude "+selected+"\nexpanded @"+root+"/link/../token p\ninclude "+missing+"\n")
	rows, err := identfile.New(main).Read(t.Context())
	want := []identfile.Row{
		{Number: 1, File: child, Line: 1, Map: "child", System: "s", Username: "p"},
		{Number: 2, File: selected, Line: 1, Map: "selected", System: "s", Username: "p"},
		{Number: 3, File: main, Line: 3, Map: "expanded", System: "selected_token", Username: "p"},
		{File: main, Line: 4, Error: "could not open file \"" + missing + "\": No such file or directory"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatal(rows, want, err)
	}
}

func TestIdentFileParserIncludeFailures(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "ident")
	a := filepath.Join(root, "failures", "A_bad.conf")
	b := filepath.Join(root, "failures", "B_good.conf")
	c := filepath.Join(root, "failures", "C_bad.conf")
	for _, path := range []string{a, b, c} {
		write(t, path, "included s p\n")
	}
	for _, path := range []string{a, c} {
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o600); err != nil {
				t.Error(err)
			}
		})
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
	}
	deniedA := "could not open file \"" + a + "\": Permission denied"
	deniedC := "could not open file \"" + c + "\": Permission denied"
	write(t, main, "before s p\ninclude_dir failures\nafter s p\n")
	rows, err := identfile.New(main).Read(t.Context())
	want := []identfile.Row{
		{Number: 1, File: main, Line: 1, Map: "before", System: "s", Username: "p"},
		{Number: 2, File: b, Line: 1, Map: "included", System: "s", Username: "p"},
		{File: main, Line: 2, Error: deniedA + "\n" + deniedA + "\n" + deniedC},
		{Number: 3, File: main, Line: 3, Map: "after", System: "s", Username: "p"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatal(rows, want, err)
	}
	write(t, main, "include_if_exists failures/A_bad.conf\nafter s p\n")
	rows, err = identfile.New(main).Read(t.Context())
	want = []identfile.Row{
		{File: main, Line: 1, Error: deniedA},
		{Number: 1, File: main, Line: 2, Map: "after", System: "s", Username: "p"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatal(rows, want, err)
	}
}

func TestIdentFileParserOptionalDepth(t *testing.T) {
	root := t.TempDir()
	for i := range 11 {
		data := "deep s p\n"
		if i < 10 {
			data = fmt.Sprintf("include_if_exists f%d\n", i+1)
		}
		write(t, filepath.Join(root, fmt.Sprintf("f%d", i)), data)
	}
	source := identfile.New(filepath.Join(root, "f0"))
	rows, err := source.Read(t.Context())
	want := []identfile.Row{{Number: 1, File: filepath.Join(root, "f10"), Line: 1, Map: "deep", System: "s", Username: "p"}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatal(rows, want, err)
	}
	write(t, filepath.Join(root, "f10"), "include_if_exists absent11\n")
	rows, err = source.Read(t.Context())
	want = []identfile.Row{{File: filepath.Join(root, "f10"), Line: 1, Error: "could not open file \"" + filepath.Join(root, "absent11") + "\": maximum nesting depth exceeded"}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatal(rows, want, err)
	}
	write(t, filepath.Join(root, "f9"), "include_if_exists absent10\n")
	rows, err = source.Read(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}
