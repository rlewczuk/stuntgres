//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRowLockViewRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range referenceRowClauses {
			for _, tc := range []struct {
				name, body, query, change string
				commit                    bool
				want                      []int64
			}{
				{"computed", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", true, []int64{21}},
				{"definition_reject", "SELECT id,v+1 AS val FROM t WHERE v=10", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", true, nil},
				{"outer_reject", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE val=11", "UPDATE t SET v=20 WHERE id=1", true, nil},
				{"on_reject", "SELECT a.id,a.v+1 AS val FROM t a JOIN t b ON a.v=b.v WHERE b.id=2", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", true, nil},
				{"delete", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1", "DELETE FROM t WHERE id=1", true, nil},
				{"rollback", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w WHERE id=1", "UPDATE t SET v=20 WHERE id=1", false, []int64{11}},
				{"frozen_sort", "SELECT id,v+1 AS val FROM t", "SELECT val FROM w ORDER BY val", "UPDATE t SET v=30 WHERE id=1", true, []int64{31, 21}},
			} {
				t.Run(tc.name+"/"+clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := newReferencePair(t)
					a, b := referenceLockConnections(t, p.ctx, p.postgres)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
					if tc.name == "on_reject" {
						referenceLockExec(t, p.ctx, a, "UPDATE t SET v=10 WHERE id=2", 1)
					}
					referenceLockExec(t, p.ctx, a, "CREATE VIEW w AS "+tc.body, 0)
					ap, bp := referenceRowPIDs(t, p.ctx, a, b)
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					reader, err := b.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false), 10)
					referenceLockExec(t, p.ctx, holder, tc.change, 1)
					done := referenceRowAsync(t, p.ctx, reader, tc.query+" "+clause, prepared, true)
					referenceRowWait(t, p.ctx, bp, ap)
					referenceLockExec(t, p.ctx, p.postgres, "INSERT INTO t VALUES(3,10)", 1)
					if tc.commit {
						err = holder.Commit()
					} else {
						err = holder.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					referenceViewValues(t, referenceLockResult(t, done), "val", "INT4", tc.want...)
				})
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, clause := range referenceRowClauses {
			for _, deleted := range []bool{false, true} {
				t.Run("RR/"+clause+map[bool]string{false: "/update", true: "/delete"}[deleted]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := referenceViewFixture(t)
					for _, backend := range []struct {
						db     *sql.DB
						record *referenceRecorder
					}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						reader, err := b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
						if err != nil {
							t.Fatal(err)
						}
						func() {
							defer reader.Rollback()
							referenceViewValues(t, referenceRowQuery(p.ctx, reader, "SELECT val FROM w WHERE id=1", false), "val", "INT4", 11)
							change := "UPDATE t SET v=20 WHERE id=1"
							if deleted {
								change = "DELETE FROM t WHERE id=1"
							}
							referenceRowAffected(t, p.ctx, a, change, false, 1)
							query := "SELECT val FROM w WHERE val=11 " + clause
							if clause == "FOR KEY SHARE" && !deleted {
								referenceViewValues(t, referenceRowQuery(p.ctx, reader, query, prepared), "val", "INT4", 11)
							} else {
								step := referenceImplicitError(query, referenceDiagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
								step.selectRows, step.prepared, step.errorStage = true, prepared, "execute"
								referenceRunStep(t, p.ctx, reader, backend.record, step)
								step = referenceImplicitError("SELECT val FROM w", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
								step.selectRows = true
								referenceRunStep(t, p.ctx, reader, backend.record, step)
							}
						}()
					}
				})
			}
		}
	}
}
