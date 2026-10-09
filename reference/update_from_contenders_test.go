package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromContenderRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
				run(referenceExec("CREATE TABLE s(lo INT,hi INT,n INT)", 0))
				// At captured v10, pairs101 and202 qualify;303 does not. At
				// latest v20,101 fails,202 still qualifies,303 is newly eligible.
				// PG observes the real queue below. Native public execution has
				// no such barrier: assert only timing-independent eligibility;
				// the genuine internal companion proves its frozen-pair ordering.
				run(referenceExec("INSERT INTO s VALUES(0,10,101),(0,30,202),(11,30,303)", 3))
				q := "UPDATE t x SET v=x.v+s.n FROM s WHERE x.id=1 AND x.v>=s.lo AND x.v<=s.hi RETURNING old.v,s.n,new.v,s.lo,s.hi"
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				holder, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				updateFromRun(t, p, b, holder)(referenceExec("UPDATE t SET v=20 WHERE id=1", 1))
				pid := 0
				if b.pg {
					pid = referenceLockPID(t, p.ctx, c)
				}
				join := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					var o relationOutcome
					if prepared {
						o.rows, o.err = stmt.QueryContext(ctx)
					} else {
						o.rows, o.err = c.QueryContext(ctx, q)
					}
					return o
				})
				if b.pg {
					updateFromPGWait(t, p.ctx, pid)
				}
				if err := holder.Commit(); err != nil {
					t.Fatal(err)
				}
				o := join()
				if o.err != nil {
					if o.rows != nil {
						o.rows.Close()
					}
					t.Fatal(o.err)
				}
				defer o.rows.Close()
				columns := []string{"v", "n", "v", "lo", "hi"}
				types := []string{"INT4", "INT4", "INT4", "INT4", "INT4"}
				actualColumns, err := o.rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				actualTypes, err := o.rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(actualColumns) != len(columns) || len(actualTypes) != len(types) {
					t.Fatal(actualColumns, actualTypes)
				}
				for i := range columns {
					if actualColumns[i] != columns[i] || actualTypes[i].DatabaseTypeName() != types[i] {
						t.Fatal("contender metadata", i, actualColumns[i], actualTypes[i].DatabaseTypeName())
					}
				}
				count := 0
				expectedFinal := int64(222)
				for o.rows.Next() {
					var old, source, next, lo, hi int64
					if err := o.rows.Scan(&old, &source, &next, &lo, &hi); err != nil {
						t.Fatal(err)
					}
					eligible := source == 202 && lo == 0 && hi == 30 && next == 222
					if !b.pg && source == 303 && lo == 11 && hi == 30 && next == 323 {
						eligible = true
						expectedFinal = 323
					}
					if old != 20 || !eligible {
						t.Fatal("current predecessor/source eligibility", old, source, next, lo, hi)
					}
					count++
				}
				if err := o.rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := o.rows.Close(); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatal("physical target updated/returned once", count)
				}
				run(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), expectedFinal}}))
			})
		})
	}
}
