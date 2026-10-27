package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func typeDescribeMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(names) {
		t.Fatalf("columns=%d want %d", len(columns), len(names))
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		if pg && types[i] != "TEXT" {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
			t.Fatalf("column %d name=%s type=%s scan=%v want %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], types[i], scan)
		}
		if n, ok := c.Nullable(); n || ok {
			t.Fatalf("computed nullable=%t,%t", n, ok)
		}
		if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatalf("decimal=%d,%d,%t", p, s, ok)
		}
		length, known := c.Length()
		if types[i] == "TEXT" {
			want := int64(9223372036854775807)
			if !known || length != want {
				t.Fatalf("TEXT length=%d,%t want %d,true", length, known, want)
			}
		} else if length != 0 || known {
			t.Fatalf("REGTYPE length=%d,%t", length, known)
		}
	}
}

func TestReferenceTypeDescribeMetadata(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE public.td_metadata(id integer,v integer)", 0))
		run(fixture.Exec("INSERT INTO public.td_metadata VALUES(1,7),(2,NULL),(3,7)", 3))
		for _, tc := range []struct {
			suffix string
			rows   [][]any
			bag    bool
		}{
			{" ORDER BY id", [][]any{{"integer", "integer", "integer"}, {"integer", "integer", "integer"}, {"integer", "integer", "integer"}}, false},
			{" WHERE id=2", [][]any{{"integer", "integer", "integer"}}, false},
			{" WHERE id=99", nil, false},
			{"", [][]any{{"integer", "integer", "integer"}, {"integer", "integer", "integer"}, {"integer", "integer", "integer"}}, true},
		} {
			s := fixture.Select("SELECT pg_typeof(v),format_type(pg_typeof(v),NULL),pg_basetype(pg_typeof(v)) AS base FROM public.td_metadata"+tc.suffix, []string{"pg_typeof", "format_type", "base"}, []string{"REGTYPE", "TEXT", "REGTYPE"}, tc.rows)
			s.Bag = tc.bag
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { typeDescribeMetadata(t, rows, b.PG, s.Columns, s.Types) }
			s.CheckRaw = func(t *testing.T, column, index int, v any) bool {
				if column != 1 {
					fixture.RegtypeRaw(t, b.PG, v)
					return true
				}
				return false
			}
			typeDescribeBoth(run, s)
		}
		nulls := fixture.Select("SELECT pg_basetype(NULL::regtype) AS identity,format_type(NULL::oid,NULL) AS label", []string{"identity", "label"}, []string{"REGTYPE", "TEXT"}, [][]any{{nil, nil}})
		nulls.CheckMetadata = func(t *testing.T, rows *sql.Rows) { typeDescribeMetadata(t, rows, b.PG, nulls.Columns, nulls.Types) }
		typeDescribeBoth(run, nulls)
		typeDescribeValue(run, "pg_typeof(format_type(23,NULL))::oid::bigint", "INT8", int64(25))
		typeDescribeValue(run, "pg_typeof(pg_basetype(23::oid::regtype))::oid::bigint", "INT8", int64(2206))
	})
}
