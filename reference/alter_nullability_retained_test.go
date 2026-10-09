package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceAlterNullabilityReviewRetainedPositiveDML(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b INT,c INT,d INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7,9,11,13)", 1))
			originalUpdate, err := backend.db.PrepareContext(p.ctx, "UPDATE t SET c=$1 WHERE c=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer originalUpdate.Close()
			originalSelect, err := backend.db.PrepareContext(p.ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer originalSelect.Close()
			tx, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			update := tx.StmtContext(p.ctx, originalUpdate)
			defer update.Close()
			selected := tx.StmtContext(p.ctx, originalSelect)
			defer selected.Close()
			change := func(next, previous int64, nullable bool) {
				t.Helper()
				heldUpdate := referenceHeldStatement{referenceExecutor: tx, statement: update, query: "UPDATE t SET c=$1 WHERE c=$2"}
				heldSelect := referenceHeldStatement{referenceExecutor: tx, statement: selected, query: "SELECT c FROM t"}
				referenceRunStep(t, p.ctx, heldUpdate, backend.recorder, referenceExec(heldUpdate.query, 1, next, previous))
				referenceTableDDLMetadata(t, p.ctx, heldSelect, backend.pg, false, heldSelect.query, []string{"c"}, []string{"INT4"}, []bool{nullable}, 1)
				referenceRunStep(t, p.ctx, heldSelect, backend.recorder, referenceSelect(heldSelect.query, []string{"c"}, []string{"INT4"}, [][]any{{next}}))
			}
			tr := func(q string) { referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec(q, 0)) }
			tr("SAVEPOINT original_shape")
			tr("ALTER TABLE t DROP b, ALTER c SET NOT NULL")
			change(14, 11, false)
			tr("SAVEPOINT parent_shape")
			tr("ALTER TABLE t DROP a")
			change(15, 14, false)
			tr("ROLLBACK TO parent_shape")
			change(17, 14, false)
			tr("ROLLBACK TO original_shape")
			change(18, 11, true)
			tr("ALTER TABLE t DROP b, DROP a, ALTER c SET NOT NULL")
			change(19, 18, false)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT * FROM t", []string{"c", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(19), int64(13)}}))
		})
	}
}

func TestReferenceAlterNullabilityReviewSnapshotTimelines(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, timeline := range []string{"middle", "multiple-before", "own-before"} {
			t.Run(fmt.Sprint(prepared)+timeline, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
						run(referenceExec("CREATE TABLE t(a INT,b TEXT,c INT,d TEXT)", 0))
						run(referenceExec("INSERT INTO t VALUES(7,'nine',11,'thirteen')", 1))
						tx, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						tr := func(s referenceStep) {
							s.prepared = prepared
							if s.selectRows {
								nullable := make([]bool, len(s.columns))
								for i, name := range s.columns {
									nullable[i] = len(s.columns) == 4 || name != "d"
								}
								referenceTableDDLMetadata(t, p.ctx, tx, backend.pg, prepared, s.query, s.columns, s.types, nullable, len(s.rows))
							}
							referenceRunStep(t, p.ctx, tx, backend.recorder, s)
						}
						tr(referenceExec("SAVEPOINT original_shape", 0))
						if timeline == "own-before" {
							tr(referenceExec("INSERT INTO t VALUES(8,'own',12,'ownlast')", 1))
						}
						tr(referenceExec("ALTER TABLE t DROP b, ALTER d SET NOT NULL", 0))
						if timeline != "middle" {
							tr(referenceExec("SAVEPOINT parent_shape", 0))
							tr(referenceExec("ALTER TABLE t DROP a", 0))
						}
						if timeline == "middle" {
							tr(referenceSelect("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(11), "thirteen"}}))
						} else {
							want := [][]any{{int64(11), "thirteen"}}
							if timeline == "own-before" {
								want = append(want, []any{int64(12), "ownlast"})
							}
							tr(referenceSelect("SELECT * FROM t ORDER BY c", []string{"c", "d"}, []string{"INT4", "TEXT"}, want))
							tr(referenceExec("ROLLBACK TO parent_shape", 0))
							if timeline == "multiple-before" {
								tr(referenceExec("ROLLBACK TO parent_shape", 0))
								tr(referenceSelect("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(11), "thirteen"}}))
							}
						}
						tr(referenceExec("ROLLBACK TO original_shape", 0))
						tr(referenceSelect("SELECT * FROM t", []string{"a", "b", "c", "d"}, []string{"INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(7), "nine", int64(11), "thirteen"}}))
						tr(referenceExec("ALTER TABLE t DROP b, ALTER d SET NOT NULL", 0))
						tr(referenceExec("UPDATE t SET d='changed'", 1))
						if err = tx.Commit(); err != nil {
							t.Fatal(err)
						}
						run(referenceSelect("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(11), "changed"}}))
					})
				}
			})
		}
	}
}
