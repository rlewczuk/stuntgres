package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferencePrimaryKeyCatalogMetadata(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
		ids := primaryKeyReferenceIDs(t, p, b, "t_pkey")
		run(primaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_b_not_null", "n", "NOT NULL b", nil}, {"t_pkey", "p", "PRIMARY KEY (a, b)", nil}}))
		run(referenceSelect("SELECT pg_get_constraintdef(oid),pg_get_constraintdef(oid,true),conrelid::int8,conbin FROM pg_constraint WHERE oid=$1", []string{"pg_get_constraintdef", "pg_get_constraintdef", "conrelid", "conbin"}, []string{"TEXT", "TEXT", "INT8", "PG_NODE_TREE"}, [][]any{{"PRIMARY KEY (a, b)", "PRIMARY KEY (a, b)", ids[1], nil}}, ids[0]))
		run(referenceSelect("SELECT relkind,relnatts FROM pg_class WHERE oid=$1", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"i", int64(2)}}, ids[2]))
		run(referenceSelect("SELECT indexname,indexdef FROM pg_indexes WHERE tablename='t'", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{{"t_pkey", "CREATE UNIQUE INDEX t_pkey ON public.t USING btree (a, b)"}}))
		// pg_index/conindid are external-only catalog columns, not native mocks.
		if b.pg {
			run(referenceSelect("SELECT indisprimary,indisunique,indimmediate,indisvalid FROM pg_index WHERE indexrelid=$1", []string{"indisprimary", "indisunique", "indimmediate", "indisvalid"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, ids[2]))
			run(referenceSelect("SELECT conindid::int8 FROM pg_constraint WHERE oid=$1", []string{"conindid"}, []string{"INT8"}, [][]any{{ids[2]}}, ids[0]))
		}
		query := "SELECT a,b,payload FROM t ORDER BY a,b"
		held, err := b.db.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ex := referenceHeldStatement{referenceExecutor: b.db, statement: held, query: query}
		want := referenceSelect(query, []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, nil)
		want.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			referenceTableDDLColumnTypes(t, b.pg, columns, want.columns, want.types, []bool{false, false, true})
		}
		for _, prepared := range []bool{false, true} {
			s := want
			s.prepared = prepared
			run(s)
		}
		referenceRunStep(t, p.ctx, ex, b.recorder, want)
		run(referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(1), "x", nil)))
		want.rows = [][]any{{int64(1), "x", nil}}
		run(want)
		referenceRunStep(t, p.ctx, ex, b.recorder, want)
	})
}
