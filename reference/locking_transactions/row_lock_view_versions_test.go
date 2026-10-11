package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referenceViewReadCommitted(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range fixture.RowClauses {
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
					p := fixture.NewScenario(t)
					a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
					if tc.name == "on_reject" {
						fixture.LockExec(t, p.Ctx, a, "UPDATE t SET v=10 WHERE id=2", 1)
					}
					fixture.LockExec(t, p.Ctx, a, "CREATE VIEW w AS "+tc.body, 0)
					ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					reader, err := b.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, fixture.RawPostgres), 10)
					fixture.LockExec(t, p.Ctx, holder, tc.change, 1)
					done := fixture.RowAsync(t, p.Ctx, reader, tc.query+" "+clause, prepared, true, fixture.RawPostgres)
					fixture.RowWait(t, p.Ctx, bp, ap)
					fixture.LockExec(t, p.Ctx, p.Postgres, "INSERT INTO t VALUES(3,10)", 1)
					if tc.commit {
						err = holder.Commit()
					} else {
						err = holder.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					fixture.ViewValues(t, fixture.LockResult(t, done), "val", "INT4", tc.want...)
				})
			}
		}
	}
}
