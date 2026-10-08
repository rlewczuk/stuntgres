//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceAlterNullabilityReviewRetainedPositiveDML(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
		native bool
	}{{"postgres", p.postgres, p.postgresRecord, false}, {"native", p.memory, p.memoryRecord, true}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
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
				r, e := update.ExecContext(p.ctx, next, previous)
				if e != nil {
					t.Fatal(e)
				}
				count, e := r.RowsAffected()
				if e != nil || count != 1 {
					t.Fatal(count, e)
				}
				rows, e := selected.QueryContext(p.ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				names, e := rows.Columns()
				if e != nil || !reflect.DeepEqual(names, []string{"c"}) {
					t.Fatal(names, e)
				}
				metadata, e := rows.ColumnTypes()
				if e != nil || len(metadata) != 1 {
					t.Fatal(metadata, e)
				}
				scan := reflect.TypeFor[int32]()
				wantNullable := false
				if backend.native {
					scan = reflect.TypeFor[int64]()
					wantNullable = nullable
				}
				n, k := metadata[0].Nullable()
				if metadata[0].DatabaseTypeName() != "INT4" || metadata[0].ScanType() != scan || k != backend.native || n != wantNullable {
					t.Fatal(metadata[0], n, k)
				}
				var got int64
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				if e = rows.Scan(&got); e != nil || got != next {
					t.Fatal(got, e)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("unexpected retained result cardinality", rows.Err())
				}
			}
			tr := func(q string) { referenceRunStep(t, p.ctx, tx, backend.record, referenceExec(q, 0)) }
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
				p := newReferencePair(t)
				for _, backend := range []struct {
					name   string
					db     *sql.DB
					record *referenceRecorder
				}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
					t.Run(backend.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
						run(referenceExec("CREATE TABLE t(a INT,b TEXT,c INT,d TEXT)", 0))
						run(referenceExec("INSERT INTO t VALUES(7,'nine',11,'thirteen')", 1))
						tx, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						tr := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, tx, backend.record, s) }
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
