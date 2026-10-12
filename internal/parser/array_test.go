package parser

import (
	"reflect"
	"testing"
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
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		if _, ok := statements[0].(*Select).Targets[0].(SubscriptExpr); !ok {
			t.Fatal("missing subscript AST", query)
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

func TestArrayExpressionAST(t *testing.T) {
	statements, err := Parse("SELECT ARRAY[[1,2],[3,4]],a[1:2][3],(a[1:2])[1],a[:],a[NULL:2] FROM t")
	if err != nil {
		t.Fatal(err)
	}
	targets := statements[0].(*Select).Targets
	constructor := targets[0].(ArrayExpr)
	if len(constructor.Elements) != 2 || len(constructor.Elements[0].(ArrayExpr).Elements) != 2 {
		t.Fatal(constructor)
	}
	adjacent := targets[1].(SubscriptExpr)
	if len(adjacent.Subscripts) != 2 || !adjacent.Subscripts[0].Slice || adjacent.Subscripts[1].Slice {
		t.Fatal(adjacent)
	}
	grouped := targets[2].(SubscriptExpr)
	if _, ok := grouped.Base.(SubscriptExpr); !ok {
		t.Fatal("grouping merged dimensions", grouped)
	}
	omitted := targets[3].(SubscriptExpr).Subscripts[0]
	explicit := targets[4].(SubscriptExpr).Subscripts[0]
	if omitted.Lower != nil || omitted.Upper != nil || explicit.Lower == nil {
		t.Fatal("omission collapsed into NULL")
	}
	statements, err = Parse("INSERT INTO t(a[1],a[3:4]) VALUES(7,ARRAY[8,9]);UPDATE t SET a[$1]=$2,a[:2]=ARRAY[7,8]")
	if err != nil {
		t.Fatal(err)
	}
	insert := statements[0].(*Insert)
	update := statements[1].(*Update)
	if len(insert.Targets[0].Steps) != 1 || !insert.Targets[1].Steps[0].Subscript.Slice || len(update.Assignments[0].Steps) != 1 || update.Assignments[1].Steps[0].Subscript.Lower != nil {
		t.Fatal(insert, update)
	}
	for _, query := range []string{"SELECT ARRAY[1,2][1] FROM t", "SELECT a[1:] garbage garbage FROM t", "INSERT INTO t(a[1]) VALUES(7) junk"} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("accepted excluded suffix/base", query)
		}
	}
}
