package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyCatalogMetadata(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
		ids := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_b_not_null", "n", "NOT NULL b", nil}, {"t_pkey", "p", "PRIMARY KEY (a, b)", nil}}))
		run(fixture.Select("SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true),conrelid::int8,conbin FROM pg_constraint WHERE oid=$1", []string{"pg_get_constraintdef", "pg_get_constraintdef", "conrelid", "conbin"}, []string{"TEXT", "TEXT", "INT8", "PG_NODE_TREE"}, [][]any{{"PRIMARY KEY (a, b)", "PRIMARY KEY (a, b)", ids[1], nil}}, ids[0]))
		run(fixture.Select("SELECT relkind,relnatts FROM pg_class WHERE oid=$1", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"i", int64(2)}}, ids[2]))
		run(fixture.Select("SELECT indexname,indexdef FROM pg_indexes WHERE tablename='t'", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{{"t_pkey", "CREATE UNIQUE INDEX t_pkey ON public.t USING btree (a, b)"}}))
		run(fixture.Select("SELECT conindid::int8 FROM pg_constraint WHERE oid=$1", []string{"conindid"}, []string{"INT8"}, [][]any{{ids[2]}}, ids[0]))
		if b.PG {
			run(fixture.Select("SELECT indisprimary,indisunique,indimmediate,indisvalid FROM pg_index WHERE indexrelid=$1", []string{"indisprimary", "indisunique", "indimmediate", "indisvalid"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, ids[2]))
		}
		query := "SELECT a,b,payload FROM t ORDER BY a,b"
		held, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ex := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: query}
		want := fixture.Select(query, []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, nil)
		want.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, b.PG, columns, want.Columns, want.Types, []bool{false, false, true})
		}
		for _, prepared := range []bool{false, true} {
			s := want
			s.Prepared = prepared
			run(s)
		}
		fixture.RunStep(t, p.Ctx, ex, b.Recorder, want)
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(1), "x", nil)))
		want.Rows = [][]any{{int64(1), "x", nil}}
		run(want)
		fixture.RunStep(t, p.Ctx, ex, b.Recorder, want)
	})
}
