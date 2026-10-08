package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteReturningSelection(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					a, _ := referenceLockConnections(t, p.ctx, db)
					r := backend.recorder
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT NOT NULL,v TEXT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", 4)
					query := "DELETE FROM t WHERE id=$1 RETURNING *"
					// Hold the same prepared statement through both executions.
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = a.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					for _, want := range [][][]any{{{int64(2), nil}, {int64(2), nil}}, {}} {
						s := referenceSelect(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, want)
						s.args, s.bag = []any{2}, true
						if stmt == nil {
							referenceRunStep(t, p.ctx, a, r, s)
						} else {
							referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: stmt, query: query}, r, s)
						}
					}
					referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}))
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2,NULL),(2,NULL)", 2)
					for _, count := range []int64{2, 0} {
						var result sql.Result
						var err error
						if stmt == nil {
							result, err = a.ExecContext(p.ctx, query, 2)
						} else {
							result, err = stmt.ExecContext(p.ctx, 2)
						}
						if err != nil {
							t.Fatal(err)
						}
						referenceLockAffected(t, result, count)
						if _, err := result.LastInsertId(); err == nil {
							t.Fatal("DELETE invented LastInsertId")
						}
					}
					referenceRunStep(t, p.ctx, a, r, referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}))
					referenceRunStep(t, p.ctx, a, r, referenceSelect("DELETE FROM t WHERE FALSE", []string{}, []string{}, [][]any{}))
					referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
				})
			}
		})
	}
}
