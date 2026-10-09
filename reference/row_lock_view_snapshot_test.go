package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRowLockViewSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range referenceRowClauses {
			for _, aggregate := range []bool{false, true} {
				for _, deleted := range []bool{false, true} {
					t.Run(clause+map[bool]string{false: "/scalar", true: "/aggregate"}[aggregate]+map[bool]string{false: "/update", true: "/delete"}[deleted]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := referenceViewFixture(t)
						if aggregate {
							p.step(t, referenceExec("CREATE VIEW stored AS SELECT id,v FROM t WHERE id=1 "+clause, 0))
							p.step(t, referenceExec("CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v", 0))
						}
						for _, backend := range p.backends() {
							t.Run(backend.name, func(t *testing.T) {
								a, b := referenceLockConnections(t, p.ctx, backend.db)
								reader, err := b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
								if err != nil {
									t.Fatal(err)
								}
								defer reader.Rollback()
								referenceRunStep(t, p.ctx, reader, backend.recorder, referenceSelect("SELECT val FROM w WHERE id=1", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}}))
								if aggregate {
									referenceRunStep(t, p.ctx, reader, backend.recorder, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
								}
								change := "UPDATE t SET v=20 WHERE id=1"
								if deleted {
									change = "DELETE FROM t WHERE id=1"
								}
								referenceRowAffected(t, p.ctx, a, change, false, 1)
								query, column, value := "SELECT val FROM w WHERE val=11 "+clause, "val", int64(11)
								if aggregate {
									query, column, value = "SELECT v FROM grouped", "v", 10
								}
								if clause == "FOR KEY SHARE" && !deleted {
									step := referenceSelect(query, []string{column}, []string{"INT4"}, [][]any{{value}})
									step.prepared = prepared
									referenceRunStep(t, p.ctx, reader, backend.recorder, step)
								} else {
									step := referenceImplicitError(query, referenceDiagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
									step.selectRows, step.prepared, step.errorStage = true, prepared, "execute"
									referenceRunStep(t, p.ctx, reader, backend.recorder, step)
									step = referenceImplicitError("SELECT val FROM w", referenceTransactionAborted())
									step.selectRows = true
									referenceRunStep(t, p.ctx, reader, backend.recorder, step)
								}
							})
						}
					})
				}
			}
		}
	}
}
