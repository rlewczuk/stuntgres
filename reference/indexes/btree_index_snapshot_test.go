package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexSnapshotCurrency(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
		for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("CREATE INDEX idx ON t(n)", 0)} {
			fixture.RunStep(t, p.Ctx, db, recorder, step)
		}
		tx, err := db.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("INSERT INTO t VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Prepared(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})))
		fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Exec("INSERT INTO t VALUES(3)", 1))
		fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}
