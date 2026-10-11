package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range fixture.RowClauses {
			for _, aggregate := range []bool{false, true} {
				for _, deleted := range []bool{false, true} {
					t.Run(clause+map[bool]string{false: "/scalar", true: "/aggregate"}[aggregate]+map[bool]string{false: "/update", true: "/delete"}[deleted]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := referenceViewFixture(t)
						if aggregate {
							p.Step(t, fixture.Exec("CREATE VIEW stored AS SELECT id,v FROM t WHERE id=1 "+clause, 0))
							p.Step(t, fixture.Exec("CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v", 0))
						}
						for _, backend := range p.Backends() {
							t.Run(backend.Name, func(t *testing.T) {
								a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
								reader, err := b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
								if err != nil {
									t.Fatal(err)
								}
								defer reader.Rollback()
								fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Select("SELECT val FROM w WHERE id=1", []string{"val"}, []string{"INT4"}, [][]any{{int64(11)}}))
								if aggregate {
									fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
								}
								change := "UPDATE t SET v=20 WHERE id=1"
								if deleted {
									change = "DELETE FROM t WHERE id=1"
								}
								fixture.RowAffected(t, p.Ctx, a, change, false, 1)
								query, column, value := "SELECT val FROM w WHERE val=11 "+clause, "val", int64(11)
								if aggregate {
									query, column, value = "SELECT v FROM grouped", "v", 10
								}
								if clause == "FOR KEY SHARE" && !deleted {
									step := fixture.Select(query, []string{column}, []string{"INT4"}, [][]any{{value}})
									step.Prepared = prepared
									fixture.RunStep(t, p.Ctx, reader, backend.Recorder, step)
								} else {
									step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
									step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
									fixture.RunStep(t, p.Ctx, reader, backend.Recorder, step)
									step = fixture.ImplicitError("SELECT val FROM w", fixture.TransactionAborted())
									step.SelectRows = true
									fixture.RunStep(t, p.Ctx, reader, backend.Recorder, step)
								}
							})
						}
					})
				}
			}
		}
	}
}
