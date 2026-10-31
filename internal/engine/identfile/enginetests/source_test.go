package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/identfile"
	"stuntgres/internal/sqlstate"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
func TestIdentFileParserLexical(t *testing.T) {
	for _, tc := range []struct{ name, input, mapName, system, user, diagnostic string }{
		{"plain", "m s p", "m", "s", "p", ""},
		{"quotes", "\"map name\" \"a,b#c\" \"d\"\"e\"", "map name", "a,b#c", "d\"e", ""},
		{"fragments", "m ab\"cd ef\"gh p", "m", "abcd efgh", "p", ""},
		{"empty", "m s \"\"", "m", "s", "", ""},
		{"unterminated", "m s \"p q", "m", "s", "p q", ""},
		{"literal at", "m \"@literal\" p", "m", "@literal", "p", ""},
		{"backslash", "m s\\x p\\1", "m", "s\\x", "p\\1", ""},
		{"crlf", "m s p\r\n", "m", "s", "p", ""},
		{"repeated trailing CR", "m s p\r\r\n", "m", "s", "p", ""},
		{"CR exposed by continuation", "m s p\r\\\n\n", "m", "s", "p", ""},
		{"CR field separator", "m\rs\rp\rignored\n", "m", "s", "p", ""},
		{"CR exposed before token", "m s p\r\\\nq\n", "m", "s", "p", ""},
		{"quoted CR", "m s \"p\rq\"\n", "m", "s", "p\rq", ""},
		{"continuation", "m sy\\\nstem p\n", "m", "system", "p", ""},
		{"quoted continuation", "m \"sy\\\nstem\" p\n", "m", "system", "p", ""},
		// REL_18_6 hba.c: last_backslash_buflen compares accumulated lengths.
		// A second physical backslash advances that length and is removed too;
		// a blank line does not advance it and must preserve the earlier slash.
		{"successive continuations", "m s p\\\n\\\n", "m", "s", "p", ""},
		{"continuation guard", "m s p\\\\\n\n", "m", "s", "p\\", ""},
		{"eof continuation", "m s p\\", "m", "s", "p", ""},
		{"trailing", "m s p ignored,extra", "m", "s", "p", ""},
		{"leading commas", ",,m s p", "m", "s", "p", ""},
		{"trailing comma", "m s p,", "m", "s", "p", ""},
		{"repeated comma", "m s,,x p", "", "", "", "multiple values in ident field"},
		{"space after comma", "m s, x p", "", "", "", "multiple values in ident field"},
		{"missing", "m s", "", "", "", "missing entry at end of line"},
		{"cardinality before regex", "m,x /[ p", "", "", "", "multiple values in ident field"},
		{"third cardinality before regex", "m /[ p,q", "", "", "", "multiple values in ident field"},
		{"regex system", "m /[ p", "", "", "", "invalid regular expression \"[\": brackets [] not balanced"},
		{"regex postgres", "m s /[", "", "", "", "invalid regular expression \"[\": brackets [] not balanced"},
		{"literal slash map", "/[ s p", "/[", "s", "p", ""},
		{"are", "m /(?=ab)ab /(?i)abc", "m", "/(?=ab)ab", "/(?i)abc", ""},
		{"literal auth", "all unknown +role", "all", "unknown", "+role", ""},
		{"three field include", "include user role", "include", "user", "role", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ident.conf")
			write(t, path, tc.input)
			rows, err := identfile.New(path).Read(t.Context())
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			want := identfile.Row{File: path, Line: 1, Map: tc.mapName, System: tc.system, Username: tc.user, Error: tc.diagnostic}
			if tc.diagnostic == "" {
				want.Number = 1
			}
			if rows[0] != want {
				t.Fatalf("got %#v want %#v", rows[0], want)
			}
		})
	}
}

func TestIdentFileParserIncludes(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "ident")
	write(t, main, "first s p\ninclude child.conf\ninclude_if_exists absent\ninclude_dir conf.d\nlast s p\n")
	write(t, filepath.Join(root, "child.conf"), "# comment\nchild s p\ninclude leaf.conf\n")
	write(t, filepath.Join(root, "leaf.conf"), "leaf s p\n")
	for _, name := range []string{"a.conf", "A.conf", "b.conf", ".hidden.conf", "ignored.txt"} {
		write(t, filepath.Join(root, "conf.d", name), strings.TrimSuffix(name, ".conf")+" s p\n")
	}
	if err := os.Mkdir(filepath.Join(root, "conf.d", "z.conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	rows, err := identfile.New(main).Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"first", "child", "leaf", "A", "a", "b", "last"}
	if len(rows) != len(names) {
		t.Fatal(rows)
	}
	for i, row := range rows {
		if row.Number != int32(i+1) || row.Map != names[i] || row.Error != "" {
			t.Fatal(row)
		}
	}
	if rows[1].File != filepath.Join(root, "child.conf") || rows[1].Line != 2 || rows[2].File != filepath.Join(root, "leaf.conf") {
		t.Fatal(rows)
	}
	write(t, filepath.Join(root, "token with spaces"), "system\n")
	write(t, main, "m @\"token with spaces\" p\n")
	rows, err = identfile.New(main).Read(t.Context())
	if err != nil || len(rows) != 1 || rows[0].System != "system" {
		t.Fatal(rows, err)
	}
	write(t, filepath.Join(root, "tokens"), "one\ntwo\n")
	write(t, main, "m @tokens p\n")
	rows, err = identfile.New(main).Read(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Error != "multiple values in ident field" {
		t.Fatal(rows, err)
	}
	write(t, main, "m s p @missing\n")
	rows, err = identfile.New(main).Read(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Error != fmt.Sprintf("could not open file \"%s\": No such file or directory", filepath.Join(root, "missing")) {
		t.Fatal(rows, err)
	}
}

func TestIdentFileParserDepth(t *testing.T) {
	root := t.TempDir()
	for i := range 11 {
		data := "m s p\n"
		if i < 10 {
			data = fmt.Sprintf("include f%d\n", i+1)
		}
		write(t, filepath.Join(root, fmt.Sprintf("f%d", i)), data)
	}
	source := identfile.New(filepath.Join(root, "f0"))
	rows, err := source.Read(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Number != 1 || rows[0].File != filepath.Join(root, "f10") {
		t.Fatal(rows, err)
	}
	write(t, filepath.Join(root, "f10"), "include f11\n")
	rows, err = source.Read(t.Context())
	want := fmt.Sprintf("could not open file \"%s\": maximum nesting depth exceeded", filepath.Join(root, "f11"))
	if err != nil || len(rows) != 1 || rows[0].Error != want || rows[0].File != filepath.Join(root, "f10") {
		t.Fatal(rows, err)
	}
	write(t, filepath.Join(root, "f0"), "include f0\n")
	rows, err = source.Read(t.Context())
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Error, "maximum nesting depth exceeded") {
		t.Fatal(rows, err)
	}
}

func TestIdentFileParserNumberingAndLargeLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ident")
	write(t, path, "# comment\\\nignored s p\n\nfirst s p\nmissing\nm x,y p\nm /[ p\nlast "+strings.Repeat("x", 128*1024)+" p\n")
	rows, err := identfile.New(path).Read(t.Context())
	if err != nil || len(rows) != 5 {
		t.Fatal(len(rows), err)
	}
	if rows[0].Line != 4 || rows[0].Number != 1 || rows[4].Line != 8 || rows[4].Number != 2 || len(rows[4].System) != 128*1024 {
		t.Fatal(rows[0], rows[4].Line)
	}
	for _, row := range rows[1:4] {
		if row.Number != 0 || row.Map != "" || row.System != "" || row.Username != "" || row.Error == "" {
			t.Fatal(row)
		}
	}
}

type cancelContext struct {
	context.Context
	calls, after int
}

func (c *cancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}
func TestIdentFileParserCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ident")
	write(t, path, "m /(?=abc)abc p\nm2 s2 p2\n")
	// Poll 15 admits the first parsed record; poll 16 enters regex compilation.
	for _, after := range []int{1, 5, 15, 16} {
		ctx := &cancelContext{Context: context.Background(), after: after}
		rows, err := identfile.New(path).Read(ctx)
		if !errors.Is(err, context.Canceled) || rows != nil {
			t.Fatal(after, rows, err)
		}
	}
	var source *identfile.Source
	rows, err := source.Read(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	_, err = identfile.New(filepath.Join(t.TempDir(), "missing")).Read(t.Context())
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "58P01" {
		t.Fatal(err)
	}
	// The same immutable source rereads bytes; it retains neither row nor regex state.
	write(t, path, "new s p\n")
	first, err := identfile.New(path).Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "newer s p\n")
	second, err := identfile.New(path).Read(t.Context())
	if err != nil || reflect.DeepEqual(first, second) || first[0].Map != "new" {
		t.Fatal(first, second, err)
	}
}
