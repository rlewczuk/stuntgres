package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestArrayTypeDeclarations(t *testing.T) {
	for _, tc := range []struct {
		declaration, name, schema string
		bounds                    []int32
	}{
		{"INTEGER[]", "integer", "", []int32{0}},
		{"INTEGER[1][2]", "integer", "", []int32{1, 2}},
		{"INTEGER ARRAY", "integer", "", []int32{0}},
		{"INTEGER ARRAY[4]", "integer", "", []int32{4}},
		{"pg_catalog.int4[][]", "int4", "pg_catalog", []int32{0, 0}},
		{"array_s.di[]", "di", "array_s", []int32{0}},
		{"VARCHAR(3)[]", "varchar", "", []int32{0}},
		{"NUMERIC(5,2)[][]", "numeric", "", []int32{0, 0}},
		{"TIMESTAMP(3) WITH TIME ZONE[]", "timestamptz", "", []int32{0}},
		{"INTERVAL DAY TO SECOND(3)[]", "interval", "", []int32{0}},
	} {
		statements, err := Parse("CREATE TABLE t(a " + tc.declaration + ")")
		if err != nil {
			t.Fatal(tc, err)
		}
		raw := statements[0].(*CreateTable).Elements[0].Column.TypeName
		schema := ""
		if raw.Schema != nil {
			schema = raw.Schema.Text
		}
		if raw.Name != tc.name || schema != tc.schema || !reflect.DeepEqual(raw.ArrayBounds, tc.bounds) {
			t.Fatal(tc, raw)
		}
		statements, err = Parse("SELECT CAST(a AS " + tc.declaration + ") FROM t")
		if err != nil {
			t.Fatal(tc, err)
		}
		cast := statements[0].(*Select).Targets[0].(CastExpr)
		if !reflect.DeepEqual(cast.TypeName.ArrayBounds, tc.bounds) {
			t.Fatal("cast dimensions", tc, cast)
		}
	}
	for _, query := range []string{"SELECT a[1] FROM t", "SELECT a[1:2] FROM t"} {
		_, err := Parse(query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("admitted excluded array expression", query, err)
		}
	}
}

func TestArrayKeywordCategories(t *testing.T) {
	// PostgreSQL REL_18_0 kwlist.h keeps ARRAY reserved and AS_LABEL.
	for _, query := range []string{
		"CREATE TABLE public.array (x INT)",
		"SELECT x AS array FROM t",
		`CREATE TABLE "array" (x INT)`,
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	keywordDiagnostic(t, "CREATE TABLE array (x INT)", "42601", `syntax error at or near "array"`, "14")
	keywordDiagnostic(t, "SELECT x array FROM t", "42601", `syntax error at or near "array"`, "10")
}
