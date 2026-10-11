package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCopyParserCSV(t *testing.T) {
	for _, q := range []string{
		"COPY t FROM STDIN WITH(FORMAT csv,HEADER MATCH,QUOTE '''',ESCAPE '!',FORCE_NULL(a),FORCE_NOT_NULL *)",
		"COPY t TO STDOUT WITH(FORMAT csv,FORCE_QUOTE *)",
		"COPY t TO STDOUT WITH(FORMAT csv,FORCE_QUOTE '*')",
		"COPY t TO STDOUT WITH(FORMAT csv,FORCE_QUOTE(\"*\"))",
		"COPY t FROM STDIN HEADER",
		"COPY t FROM STDIN WITH(FORMAT csv,FORCE_NULL)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		c, ok := statements[0].(*Copy)
		if !ok {
			t.Fatal(statements)
		}
		for _, opt := range c.Options {
			index := strings.Index(strings.ToLower(q), opt.Name)
			want := utf8.RuneCountInString(q[:index]) + 1
			if opt.Pos.Character != want {
				t.Fatal(q, opt, want)
			}
		}
	}
	for _, q := range []string{"COPY t FROM STDIN WITH(FORMAT csv) garbage", "COPY t FROM STDIN WITH(FORMAT csv); garbage", "COPY t FROM STDIN WITH(FORMAT csv,FORCE_NULL(a)) trailing"} {
		statements, err := Parse(q)
		if err == nil || statements != nil {
			t.Fatal("accepted invalid suffix", q, statements, err)
		}
	}
	q := "/* é */ COPY t FROM STDIN WITH(FORMAT csv,FORCE_NULL(a))"
	statements, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	c := statements[0].(*Copy)
	if c.Options[1].Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, "FORCE_NULL")])+1 {
		t.Fatal(c.Options)
	}
	for _, tc := range []struct {
		argument string
		shape    CopyArgument
	}{
		{"", CopyArgumentNone}, {" '*'", CopyArgumentScalar}, {" *", CopyArgumentStar}, {"(\"*\")", CopyArgumentColumns},
	} {
		statements, err := Parse("COPY t TO STDOUT WITH(FORMAT csv,FORCE_QUOTE" + tc.argument + ")")
		if err != nil {
			t.Fatal(err)
		}
		option := statements[0].(*Copy).Options[1]
		if option.Argument != tc.shape {
			t.Fatal(tc.argument, option)
		}
	}
}
