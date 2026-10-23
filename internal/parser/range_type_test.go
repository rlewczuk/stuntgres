package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestRangeTypeParser(t *testing.T) {
	const q = `CREATE TYPE public.custom_span AS RANGE(SUBTYPE=integer,SUBTYPE_OPCLASS=pg_catalog.int4_ops,COLLATION=pg_catalog."C",SUBTYPE_DIFF=pg_catalog.int4eq,MULTIRANGE_TYPE_NAME=public.custom_spans,SUBTYPE=bigint)`
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	r, ok := statements[0].(*CreateRange)
	if !ok || len(r.Options) != 6 || len(r.Name.Parts) != 2 || r.Name.Parts[1].Text != "custom_span" {
		t.Fatalf("range AST %#v", statements[0])
	}
	want := []string{"subtype", "subtype_opclass", "collation", "subtype_diff", "multirange_type_name", "subtype"}
	for i, option := range r.Options {
		if option.Name.Text != want[i] || option.Name.Pos.Character == 0 || option.Value.Name == "" {
			t.Fatal(i, option)
		}
		if i > 0 && option.Name.Pos.Character <= r.Options[i-1].Name.Pos.Character {
			t.Fatal("option order lost")
		}
	}
	if r.Options[2].Value.Name != "C" || !r.Options[2].Value.Quoted {
		t.Fatal("quoted collation lost", r.Options[2])
	}
	for _, suffix := range []string{" trailing", ",", "; CREATE TYPE broken AS RANGE(SUBTYPE=)"} {
		if _, err := Parse(q + suffix); err == nil {
			t.Fatal("valid prefix accepted", suffix)
		}
	}
	for _, bad := range []string{`CREATE TYPE broken AS RANGE(SUBTYPE=)`, `CREATE TYPE broken AS RANGE(SUBTYPE=int4,)`, `CREATE TYPE broken AS RANGE(SUBTYPE int4)`} {
		if _, err := Parse(bad); err == nil {
			t.Fatal("malformed range accepted", bad)
		}
	}
	for _, tc := range []struct{ query, token string }{
		{`/*é*/ CREATE TYPE broken AS RANGE()`, `)`},
		{`CREATE TYPE broken AS RANGE('x')`, `'x'`},
		{`CREATE TYPE broken AS ENUM(SUBTYPE=int4)`, `SUBTYPE`},
	} {
		_, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		position := strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, tc.token)]) + 1)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "`+tc.token+`"` || diagnostic.Position != position {
			t.Fatal("declaration body frontier", tc.query, err)
		}
	}
}
