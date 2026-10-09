package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterNullabilityFreshValidationAfterDelete(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("CREATE TABLE u(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(NULL),(7)", 2))
			run(referenceExec("INSERT INTO u VALUES(1)", 1))
			tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
			tr(referenceSelect("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceExec("DELETE FROM t WHERE a IS NULL", 1))
			run(referenceExec("INSERT INTO u VALUES(2)", 1))
			tr(referenceExec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			tr(referenceSelect("SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			referenceTableDDLMetadata(t, p.ctx, b.db, b.pg, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 1)
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
