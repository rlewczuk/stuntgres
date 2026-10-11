package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func queryDistinctArrayWitness(t *testing.T, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE arrays(a INT[])", 0))
	run(fixture.Exec("INSERT INTO arrays VALUES('{1,2}'),('{1,2}'),('[0:1]={1,2}'),('{{1,2}}'),('{}'),('{NULL}'),(NULL),(NULL)", 8))
	s := fixture.Select("SELECT DISTINCT a FROM arrays", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2}"}, {"[0:1]={1,2}"}, {"{{1,2}}"}, {"{}"}, {"{NULL}"}, {nil}})
	s.Bag = true
	fixture.QueryDistinctBoth(run, s)
	s.Query += " WHERE FALSE"
	s.Rows = [][]any{}
	fixture.QueryDistinctBoth(run, s)
}

func TestReferenceQueryDistinctArrays(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "distinct_path")
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctArrayWitness(t, run)
		for i, tc := range []struct {
			typ, values string
			count       int64
		}{
			{"NUMERIC[]", "('{1.0,NULL}'),('{1.00,NULL}'),('{2,NULL}'),(NULL),(NULL)", 3},
			{"CHAR(3)[]", "('{a,NULL}'),('{\"a \",NULL}'),('{A,NULL}'),(NULL),(NULL)", 3},
			{"DOUBLE PRECISION[]", "('{0,NaN}'),('{-0,NaN}'),('{1,NaN}'),(NULL),(NULL)", 3},
		} {
			table := fmt.Sprintf("array_equal_%d", i)
			run(fixture.Exec("CREATE TABLE "+table+"(a "+tc.typ+")", 0))
			run(fixture.Exec("INSERT INTO "+table+" VALUES"+tc.values, 5))
			run(fixture.Exec("CREATE VIEW "+table+"_dv AS SELECT DISTINCT a FROM "+table, 0))
			fixture.QueryDistinctBoth(run, fixture.Select("SELECT count(*) AS n FROM "+table+"_dv", []string{"n"}, []string{"INT8"}, [][]any{{tc.count}}))
		}
		run(fixture.Exec("CREATE TABLE names(a NAME[])", 0))
		run(fixture.Exec("INSERT INTO names VALUES('{NULL}'),('{NULL}'),('{\"NULL\"}'),('{ alpha }'),('{alpha}'),('{}'),(NULL),(NULL)", 8))
		s := fixture.Select("SELECT DISTINCT a FROM names", []string{"a"}, []string{"_NAME"}, [][]any{{"{NULL}"}, {`{"NULL"}`}, {"{alpha}"}, {"{}"}, {nil}})
		s.Bag = true
		fixture.QueryDistinctBoth(run, s)
		run(fixture.Exec("CREATE TABLE clipped_names(a NAME[])", 0))
		run(fixture.Exec("INSERT INTO clipped_names VALUES($1),($2),($3)", 3, "{"+strings.Repeat("a", 64)+"}", "{"+strings.Repeat("a", 63)+"}", "{"+strings.Repeat("é", 32)+"}"))
		s = fixture.Select("SELECT DISTINCT a FROM clipped_names", []string{"a"}, []string{"_NAME"}, [][]any{{"{" + strings.Repeat("a", 63) + "}"}, {"{" + strings.Repeat("é", 31) + "}"}})
		s.Bag = true
		fixture.QueryDistinctBoth(run, s)
		run(fixture.Exec("CREATE DOMAIN distinct_int AS INTEGER", 0))
		run(fixture.Exec("CREATE DOMAIN distinct_array AS INTEGER[]", 0))
		run(fixture.Exec("CREATE DOMAIN distinct_nested AS distinct_array[]", 0))
		for _, tc := range []struct {
			table, typ, public, values string
			want                       [][]any
		}{
			{"whole_domain", "distinct_array", "_INT4", "('{1,NULL}'),('{1,NULL}'),('{}'),(NULL),(NULL)", [][]any{{"{1,NULL}"}, {"{}"}, {nil}}},
			{"element_domain", "distinct_int[]", "_DISTINCT_INT", "('{1,NULL}'),('{1,NULL}'),('{}'),(NULL),(NULL)", [][]any{{"{1,NULL}"}, {"{}"}, {nil}}},
			{"nested_domain", "distinct_nested", "_DISTINCT_ARRAY", `('{"{1,NULL}",NULL}'),('{"{1,NULL}",NULL}'),('{}'),(NULL),(NULL)`, [][]any{{`{"{1,NULL}",NULL}`}, {"{}"}, {nil}}},
		} {
			run(fixture.Exec("CREATE TABLE "+tc.table+"(a "+tc.typ+")", 0))
			run(fixture.Exec("INSERT INTO "+tc.table+" VALUES"+tc.values, 5))
			public := tc.public
			if b.PG && tc.table != "whole_domain" {
				public = ""
			}
			s := fixture.ArrayReferenceSelect("SELECT DISTINCT a FROM "+tc.table, []string{"a"}, []string{public}, []string{"_INT4"}, tc.want, b.PG)
			s.Bag = true
			fixture.QueryDistinctBoth(run, s)
			s.Query += " WHERE FALSE"
			s.Rows = [][]any{}
			fixture.QueryDistinctBoth(run, s)
			run(fixture.Exec("CREATE VIEW "+tc.table+"_dv AS SELECT DISTINCT a FROM "+tc.table, 0))
		}
		run(fixture.Exec("CREATE SCHEMA distinct_path", 0))
		run(fixture.Exec("CREATE TABLE flags(f BOOLEAN)", 0))
		run(fixture.Exec("INSERT INTO flags VALUES(FALSE),(FALSE),(TRUE),(TRUE),(NULL),(NULL)", 6))
		run(fixture.Exec("SET search_path TO public", 0))
		s = fixture.Select("SELECT DISTINCT current_schemas(f) AS a FROM flags", []string{"a"}, []string{"_NAME"}, [][]any{{"{public}"}, {"{pg_catalog,public}"}, {nil}})
		s.Bag = true
		fixture.QueryDistinctBoth(run, s)
		q := "SELECT DISTINCT current_schemas($1::boolean) AS a FROM flags"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, path := range []string{"public", "distinct_path,public"} {
			run(fixture.Exec("SET search_path TO "+path, 0))
			for _, arg := range []any{false, true, nil} {
				var value any
				if arg == false {
					value = "{" + path + "}"
				}
				if arg == true {
					value = "{pg_catalog," + path + "}"
				}
				s := fixture.Select(q, []string{"a"}, []string{"_NAME"}, [][]any{{value}}, arg)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
			}
		}
	})
}
