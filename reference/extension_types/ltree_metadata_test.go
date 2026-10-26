package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeMetadata(t *testing.T) {
	ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lt_metadata(id integer NOT NULL,v ltree,n ltree NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE lt_vacant(v ltree NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE lt_nullonly(v ltree)", 0))
		run(fixture.Exec("INSERT INTO lt_nullonly VALUES (NULL)", 1))
		run(fixture.Exec("INSERT INTO lt_metadata VALUES (1,'a.b',''),(2,NULL,'AbC')", 2))
		for _, tc := range []struct{ q string; names []string; nullable []bool; values [][]any }{
			{"SELECT v,n FROM lt_metadata ORDER BY id", []string{"v", "n"}, []bool{true, false}, [][]any{{"a.b", ""}, {nil, "AbC"}}},
			{"SELECT v,n FROM lt_metadata WHERE id=99", []string{"v", "n"}, []bool{true, false}, [][]any{}},
			{"SELECT v FROM lt_metadata WHERE id=2", []string{"v"}, []bool{true}, [][]any{{nil}}},
			{"SELECT v FROM lt_nullonly", []string{"v"}, []bool{true}, [][]any{{nil}}},
			{"SELECT v FROM lt_vacant", []string{"v"}, []bool{false}, [][]any{}},
			{"SELECT v AS alias FROM lt_metadata WHERE id=2", []string{"alias"}, []bool{true}, [][]any{{nil}}},
			{"SELECT NULL::ltree AS v", []string{"v"}, nil, [][]any{{nil}}},
			{"SELECT 'a'::ltree AS v", []string{"v"}, nil, [][]any{{"a"}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := ltreeSelect(b, tc.q, tc.names, tc.values); s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ltreeMetadata(t, b.PG, rows, tc.names, tc.nullable) }
				run(s)
			}
		}
		q := "SELECT v,n FROM lt_metadata WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ id int; values [][]any }{{1, [][]any{{"a.b", ""}}}, {2, [][]any{{nil, "AbC"}}}, {99, [][]any{}}} {
			s := ltreeSelect(b, q, []string{"v", "n"}, tc.values, tc.id)
			var retained []*sql.ColumnType
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { retained = ltreeMetadata(t, b.PG, rows, s.Columns, []bool{true, false}) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			ltreeColumnMetadata(t, b.PG, retained, s.Columns, []bool{true, false})
		}
		run(fixture.Select("SELECT a.attname,a.atttypid=('ltree'::regtype)::oid AS same_type,a.attnotnull,a.atttypmod,a.attcollation::text AS attcollation FROM pg_attribute a WHERE a.attrelid='lt_metadata'::regclass::oid AND a.attnum>1 AND NOT a.attisdropped ORDER BY a.attnum", []string{"attname", "same_type", "attnotnull", "atttypmod", "attcollation"}, []string{"NAME", "BOOL", "BOOL", "INT4", "TEXT"}, [][]any{{"v", true, false, int64(-1), "0"}, {"n", true, true, int64(-1), "0"}}))
	})
}