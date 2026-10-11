package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityFreshValidationAfterDelete(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			run(fixture.Exec("CREATE TABLE u(a INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL),(7)", 2))
			run(fixture.Exec("INSERT INTO u VALUES(1)", 1))
			tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Select("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("DELETE FROM t WHERE a IS NULL", 1))
			run(fixture.Exec("INSERT INTO u VALUES(2)", 1))
			tr(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			tr(fixture.Select("SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
