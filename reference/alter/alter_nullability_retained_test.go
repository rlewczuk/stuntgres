package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityReviewRetainedPositiveDML(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT,d INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7,9,11,13)", 1))
			originalUpdate, err := backend.DB.PrepareContext(p.Ctx, "UPDATE t SET c=$1 WHERE c=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer originalUpdate.Close()
			originalSelect, err := backend.DB.PrepareContext(p.Ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer originalSelect.Close()
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			update := tx.StmtContext(p.Ctx, originalUpdate)
			defer update.Close()
			selected := tx.StmtContext(p.Ctx, originalSelect)
			defer selected.Close()
			change := func(next, previous int64, nullable bool) {
				t.Helper()
				heldUpdate := fixture.HeldStatement{Executor: tx, Statement: update, SQL: "UPDATE t SET c=$1 WHERE c=$2"}
				heldSelect := fixture.HeldStatement{Executor: tx, Statement: selected, SQL: "SELECT c FROM t"}
				fixture.RunStep(t, p.Ctx, heldUpdate, backend.Recorder, fixture.Exec(heldUpdate.SQL, 1, next, previous))
				fixture.TableDDLMetadata(t, p.Ctx, heldSelect, backend.PG, false, heldSelect.SQL, []string{"c"}, []string{"INT4"}, []bool{nullable}, 1)
				fixture.RunStep(t, p.Ctx, heldSelect, backend.Recorder, fixture.Select(heldSelect.SQL, []string{"c"}, []string{"INT4"}, [][]any{{next}}))
			}
			tr := func(q string) { fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec(q, 0)) }
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
			run(fixture.Select("SELECT * FROM t", []string{"c", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(19), int64(13)}}))
		})
	}
}

func TestReferenceAlterNullabilityReviewSnapshotTimelines(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, timeline := range []string{"middle", "multiple-before", "own-before"} {
			t.Run(fmt.Sprint(prepared)+timeline, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,c INT,d TEXT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(7,'nine',11,'thirteen')", 1))
						tx, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						tr := func(s fixture.Step) {
							s.Prepared = prepared
							if s.SelectRows {
								nullable := make([]bool, len(s.Columns))
								for i, name := range s.Columns {
									nullable[i] = len(s.Columns) == 4 || name != "d"
								}
								fixture.TableDDLMetadata(t, p.Ctx, tx, backend.PG, prepared, s.Query, s.Columns, s.Types, nullable, len(s.Rows))
							}
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
						}
						tr(fixture.Exec("SAVEPOINT original_shape", 0))
						if timeline == "own-before" {
							tr(fixture.Exec("INSERT INTO t VALUES(8,'own',12,'ownlast')", 1))
						}
						tr(fixture.Exec("ALTER TABLE t DROP b, ALTER d SET NOT NULL", 0))
						if timeline != "middle" {
							tr(fixture.Exec("SAVEPOINT parent_shape", 0))
							tr(fixture.Exec("ALTER TABLE t DROP a", 0))
						}
						if timeline == "middle" {
							tr(fixture.Select("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(11), "thirteen"}}))
						} else {
							want := [][]any{{int64(11), "thirteen"}}
							if timeline == "own-before" {
								want = append(want, []any{int64(12), "ownlast"})
							}
							tr(fixture.Select("SELECT * FROM t ORDER BY c", []string{"c", "d"}, []string{"INT4", "TEXT"}, want))
							tr(fixture.Exec("ROLLBACK TO parent_shape", 0))
							if timeline == "multiple-before" {
								tr(fixture.Exec("ROLLBACK TO parent_shape", 0))
								tr(fixture.Select("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(11), "thirteen"}}))
							}
						}
						tr(fixture.Exec("ROLLBACK TO original_shape", 0))
						tr(fixture.Select("SELECT * FROM t", []string{"a", "b", "c", "d"}, []string{"INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(7), "nine", int64(11), "thirteen"}}))
						tr(fixture.Exec("ALTER TABLE t DROP b, ALTER d SET NOT NULL", 0))
						tr(fixture.Exec("UPDATE t SET d='changed'", 1))
						if err = tx.Commit(); err != nil {
							t.Fatal(err)
						}
						run(fixture.Select("SELECT * FROM t", []string{"a", "c", "d"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(7), int64(11), "changed"}}))
					})
				}
			})
		}
	}
}
