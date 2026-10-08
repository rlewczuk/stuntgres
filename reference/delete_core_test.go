package reference_test

import (
	"testing"
)

func TestReferenceDeleteCoreSelection(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		prepared    bool
	}{
		{"direct", "DELETE FROM t WHERE v=10", false},
		{"as_alias", "DELETE FROM t AS d WHERE d.v=10", false},
		{"bare_alias", "DELETE FROM t d WHERE d.v=10", false},
		{"prepared", "DELETE FROM t AS d WHERE d.v=$1", true},
		{"qualified_quoted_alias", `/*é*/ DELETE FROM public.t AS "d" WHERE "d"."v"=10`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			// Each backend owns its entire scenario. A native Fatal must not
			// prevent the PostgreSQL scenario from executing independently.
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						t.Helper()
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					run(referenceExec("CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)", 5))
					if tc.prepared {
						// Retain one real statement across executions: parameters
						// and row selection must be fresh, not prepare-time state.
						stmt, err := backend.db.PrepareContext(p.ctx, tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for _, call := range []struct {
							arg  any
							want int64
						}{{10, 3}, {10, 0}, {nil, 0}} {
							result, err := stmt.ExecContext(p.ctx, call.arg)
							if err != nil {
								t.Fatal(err)
							}
							if _, err := result.LastInsertId(); err == nil {
								t.Fatal("DELETE invented LastInsertId")
							}
							if got, err := result.RowsAffected(); err != nil || got != call.want {
								t.Fatalf("RowsAffected got %d, %v; want %d", got, err, call.want)
							}
						}
					} else {
						for _, count := range []int64{3, 0} {
							result, err := backend.db.ExecContext(p.ctx, tc.query)
							if err != nil {
								t.Fatal(err)
							}
							referenceLockAffected(t, result, count)
							if _, err := result.LastInsertId(); err == nil {
								t.Fatal("DELETE invented LastInsertId")
							}
						}
					}
					run(referenceSelect("SELECT id,v,f FROM t ORDER BY id", []string{"id", "v", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(2), nil, nil}, {int64(3), int64(20), false}}))
					run(referenceExec("DELETE FROM t WHERE FALSE", 0))
					run(referenceExec("DELETE FROM t WHERE NULL", 0))
					run(referenceSelect("SELECT id,v,f FROM t ORDER BY id", []string{"id", "v", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(2), nil, nil}, {int64(3), int64(20), false}}))
					run(referenceExec("DELETE FROM t", 2))
					run(referenceSelect("SELECT id,v,f FROM t ORDER BY id", []string{"id", "v", "f"}, []string{"INT4", "INT4", "BOOL"}, nil))
					run(referenceExec("DELETE FROM t", 0))
				})
			}
		})
	}
}
