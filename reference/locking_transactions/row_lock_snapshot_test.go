package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockKeyShareSnapshot(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			recorder := backend.Recorder
			fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
			fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			reader, err := b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			fixture.RunStep(t, p.Ctx, reader, recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("UPDATE t SET v=20 WHERE id=1", 1))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"SELECT v FROM t WHERE id=1 FOR KEY SHARE", "SELECT v FROM t WHERE v=10 FOR KEY SHARE", "SELECT a.v FROM t a JOIN t b USING(id) WHERE a.id=1 AND b.v=10 FOR KEY SHARE"} {
					step := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, reader, recorder, step)
				}
			}
			fixture.RunStep(t, p.Ctx, a, recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}}))
		})
	}
}

func TestReferenceRowLockKeyShareDeletedReplacement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					recorder := backend.Recorder
					fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					reader, err := b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					fixture.RunStep(t, p.Ctx, reader, recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("UPDATE t SET v=20 WHERE id=1", 1))
					fixture.RunStep(t, p.Ctx, a, recorder, fixture.Exec("DELETE FROM t WHERE id=1", 1))
					step := fixture.ImplicitError("SELECT v FROM t WHERE id=1 FOR KEY SHARE", fixture.Diagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
					step.SelectRows = true
					step.ErrorStage = "execute"
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, reader, recorder, step)
					fixture.RunStep(t, p.Ctx, reader, recorder, fixture.TransactionError(false, "SELECT v FROM t", fixture.TransactionAborted()))
				})
			}
		})
	}
}
