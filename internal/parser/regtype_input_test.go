package parser

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRegtypeStandaloneTypeEntry(t *testing.T) {
	for _, tc := range []struct {
		input, name   string
		system        bool
		parts, arrays int
	}{
		{"INTEGER", "int4", true, 0, 0},
		{"int4", "int4", false, 0, 0},
		{"double precision", "float8", true, 0, 0},
		{"float(25)", "float8", true, 0, 0},
		{"timestamp with time zone[]", "timestamptz", true, 0, 1},
		{`"Odd.Schema"."a.b"[]`, "a.b", false, 2, 1},
		{`stuntgres.pg_catalog.numeric(4,2)[8]`, "numeric", false, 3, 1},
	} {
		raw, err := ParseTypeName(context.Background(), tc.input, nil)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		if raw.Name != tc.name || raw.System != tc.system || len(raw.Parts) != tc.parts || len(raw.ArrayBounds) != tc.arrays {
			t.Fatalf("%q: %#v", tc.input, raw)
		}
	}
	// The private entry is not a SELECT wrapper: it must consume its entire input.
	for _, text := range []string{"integer; SELECT 1", "integer trailing", "integer(4)", "integer..x", "SETOF integer", "integer%TYPE"} {
		_, err := ParseTypeName(context.Background(), text, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatalf("%q: %v", text, err)
		}
	}
	statements, err := Parse("CREATE TABLE t(a INT4,b BOOL)")
	if err != nil {
		t.Fatal(err)
	}
	create := statements[0].(*CreateTable)
	if create.Elements[0].Column.TypeName.Name != "integer" || create.Elements[1].Column.TypeName.Name != "boolean" {
		t.Fatal("private entry changed SQL annotation normalization", create)
	}
}
