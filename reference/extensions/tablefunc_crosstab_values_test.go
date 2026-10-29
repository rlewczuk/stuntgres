package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabValues(t *testing.T) {
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		crosstabSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT * FROM crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
		h := fixture.CrosstabHeld(t, p, c, q)
		s := fixture.CrosstabWant(q, fixture.CrosstabSource)
		run(s)
		run(fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		run(fixture.CrosstabWant("SELECT * FROM crosstab(" + fixture.CrosstabQuote(fixture.CrosstabSource) + ") AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"))
		for _, tc := range []struct {
			name    string
			columns []string
			rows    [][]any
		}{
			{"crosstab2", []string{"row_name", "category_1", "category_2"}, [][]any{{"g1", "A", "C"}, {"g2", "B1", "B2"}, {"g3", nil, nil}}},
			{"crosstab3", []string{"row_name", "category_1", "category_2", "category_3"}, [][]any{{"g1", "A", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}}},
			{"crosstab4", []string{"row_name", "category_1", "category_2", "category_3", "category_4"}, [][]any{{"g1", "A", "C", nil, nil}, {"g2", "B1", "B2", "B3", "B4"}, {"g3", nil, nil, nil, nil}}},
		} {
			types := make([]string, len(tc.columns))
			for i := range types {
				types[i] = "TEXT"
			}
			s := fixture.Select("SELECT * FROM "+tc.name+"($1::text) ORDER BY row_name", tc.columns, types, tc.rows, fixture.CrosstabSource)
			run(s)
			run(fixture.Prepared(s))
		}
		// Category labels are neither keys nor sort instructions.
		run(fixture.Exec("UPDATE ct_source SET category='same'", 8))
		fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.CrosstabWant(q, fixture.CrosstabSource))
		run(fixture.Exec("UPDATE ct_source SET v='changed' WHERE seq=1", 1))
		s = fixture.CrosstabWant(q, fixture.CrosstabSource)
		s.Rows = [][]any{{"g1", "changed", "C", nil}, {"g2", "B1", "B2", "B3"}, {"g3", nil, nil, nil}}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		whole := fixture.Select("SELECT crosstab3($1::text)::text AS pivot", []string{"pivot"}, []string{"TEXT"}, [][]any{{"(g1,changed,C,)"}, {"(g2,B1,B2,B3)"}, {"(g3,,,)"}}, fixture.CrosstabSource)
		whole.Bag = true
		run(whole)
		run(fixture.Prepared(whole))
	})
}

func TestReferenceTablefuncCrosstabBoundaries(t *testing.T) {
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		crosstabSeed(t, p, b, c)
		run := func(s fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("boundary query %q args %#v prepared=%v", s.Query, s.Args, s.Prepared)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q := "SELECT * FROM crosstab($1::text,$2::integer) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
		h := fixture.CrosstabHeld(t, p, c, q)
		for _, n := range []int64{-1, 0, 1, 2147483647} {
			s := fixture.CrosstabWant(q, fixture.CrosstabSource, n)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		for _, args := range [][]any{{nil, int64(1)}, {"SELECT missing_ct", nil}, {nil, nil}} {
			s := fixture.CrosstabWant(q, args...)
			s.Rows = nil
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		for _, source := range []any{nil, "", "SELECT k,category,v FROM ct_source WHERE false", "SELECT k FROM ct_source WHERE false", "SELECT 1,2,3 WHERE false", "SELECT 1; SELECT k,category,v FROM ct_source WHERE false"} {
			s := fixture.CrosstabWant("SELECT * FROM crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text)", source)
			s.Rows = nil
			run(s)
			run(fixture.Prepared(s))
		}
		for _, source := range []string{"SHOW work_mem", fixture.CrosstabSource + "; SHOW work_mem"} {
			d := fixture.ControlsReferenceDiagnostic("0A000", "SHOW is not allowed in a non-volatile function")
			d.Context = "SQL statement \"" + source + "\""
			crosstabFailure(t, p, b, c, "SELECT * FROM crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text)", d, "execute", source)
		}
		run(fixture.CrosstabWant("SELECT * FROM crosstab($1::text) AS t(k text,c1 text,c2 text,c3 text) ORDER BY k", "SELECT 1; "+fixture.CrosstabSource))
		run(fixture.Exec("CREATE TABLE ct_groups(seq integer,k text,category integer,v text)", 0))
		run(fixture.Exec("INSERT INTO ct_groups VALUES (1,'a',1,'a1'),(2,'b',1,'b1'),(3,'a',1,'a2'),(4,NULL,NULL,'n1'),(5,NULL,1,NULL),(6,NULL,1,'n3'),(7,'z',0,NULL),(8,NULL,0,'n4')", 8))
		s := fixture.Select("SELECT * FROM crosstab($1::text) AS t(k text,a text,b text)", []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"a", "a1", nil}, {"b", "b1", nil}, {"a", "a2", nil}, {nil, "n1", nil}, {"z", nil, nil}, {nil, "n4", nil}}, "SELECT k,category,v FROM ct_groups ORDER BY seq")
		s.Bag = true
		run(s)
		run(fixture.Prepared(s))
		for _, n := range []string{"2", "3", "4"} {
			columns := []string{"row_name"}
			types := []string{"TEXT"}
			for i := 1; i <= int(n[0]-'0'); i++ {
				columns = append(columns, "category_"+string(rune('0'+i)))
				types = append(types, "TEXT")
			}
			s := fixture.Select("SELECT * FROM crosstab"+n+"(NULL::text)", columns, types, nil)
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
