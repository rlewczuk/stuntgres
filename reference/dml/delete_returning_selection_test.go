package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningSelection(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					a, _ := fixture.LockConnections(t, p.Ctx, db)
					r := backend.Recorder
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT NOT NULL,v TEXT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,'a'),(2,NULL),(2,NULL),(3,'c')", 4)
					query := "DELETE FROM t WHERE id=$1 RETURNING *"
					// Hold the same prepared statement through both executions.
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = a.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					for _, want := range [][][]any{{{int64(2), nil}, {int64(2), nil}}, {}} {
						s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, want)
						s.Args, s.Bag = []any{2}, true
						if stmt == nil {
							fixture.RunStep(t, p.Ctx, a, r, s)
						} else {
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: query}, r, s)
						}
					}
					fixture.RunStep(t, p.Ctx, a, r, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}))
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2,NULL),(2,NULL)", 2)
					for _, count := range []int64{2, 0} {
						var result sql.Result
						var err error
						if stmt == nil {
							result, err = a.ExecContext(p.Ctx, query, 2)
						} else {
							result, err = stmt.ExecContext(p.Ctx, 2)
						}
						if err != nil {
							t.Fatal(err)
						}
						fixture.LockAffected(t, result, count)
						if _, err := result.LastInsertId(); err == nil {
							t.Fatal("DELETE invented LastInsertId")
						}
					}
					fixture.RunStep(t, p.Ctx, a, r, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "c"}}))
					fixture.RunStep(t, p.Ctx, a, r, fixture.Select("DELETE FROM t WHERE FALSE", []string{}, []string{}, [][]any{}))
					fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
				})
			}
		})
	}
}
