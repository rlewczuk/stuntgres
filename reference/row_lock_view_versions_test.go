package reference_test

import (
	"testing"
)

func referenceViewReadCommitted(t *testing.T) {
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
					p := newReferenceScenario(t)
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
					referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, referenceRawPostgres), 10)
					referenceLockExec(t, p.ctx, holder, tc.change, 1)
					done := referenceRowAsync(t, p.ctx, reader, tc.query+" "+clause, prepared, true, referenceRawPostgres)
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
}
