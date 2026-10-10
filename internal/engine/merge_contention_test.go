package engine

import (
	"context"
	"errors"
	"testing"
)

func TestMergeInsertContention(t *testing.T) {
	for _, table := range []string{"t", "s"} {
		for _, prepare := range []bool{false, true} {
			t.Run(table+map[bool]string{false: "/execute", true: "/prepare"}[prepare], func(t *testing.T) {
				e := New()
				holder := sessionStart(t, e, nil)
				worker := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE s(id INT)", "INSERT INTO s VALUES(1)"} {
					sessionSQL(t, holder, q)
				}
				sessionSQL(t, holder, "BEGIN")
				sessionSQL(t, holder, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				q := "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
				done := lockAsync(e, worker, ctx, q, prepare)
				lockPending(t, e, 1)
				cancel()
				lockAwait(t, done, context.Canceled)
				sessionSQL(t, holder, "ROLLBACK")
				transactionRows(t, holder, "SELECT id FROM t")
				updateDeleteExecute(t, e, worker, q, 1)
				transactionRows(t, holder, "SELECT id FROM t", 1)
				lockEmpty(t, e)
			})
		}
	}
}

func TestMergeInsertConcurrentUnique(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			sessionSQL(t, holder, "CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)")
			sessionSQL(t, holder, "BEGIN")
			sessionSQL(t, holder, "INSERT INTO t VALUES(7)")
			join := updateDeleteAsync(t, e, worker, context.Background(), "MERGE INTO t USING (VALUES(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)")
			lockPending(t, e, 1)
			if commit {
				sessionSQL(t, holder, "COMMIT")
			} else {
				sessionSQL(t, holder, "ROLLBACK")
			}
			result, err := join()
			if commit {
				var diagnostic interface{ SQLState() string }
				if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "23505" || result.Count != 0 || result.Rows != nil {
					t.Fatal("must not restart matching after unique wait", result, err)
				}
			} else if err != nil || result.Count != 1 {
				t.Fatal(result, err)
			}
			transactionRows(t, holder, "SELECT id FROM t", 7)
			uniqueClaimsEmpty(t, e)
			lockEmpty(t, e)
		})
	}
}

func TestMergeInsertIsolation(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(isolation, func(t *testing.T) {
			e := New()
			writer := sessionStart(t, e, nil)
			reader := sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE t(id INT)")
			sessionSQL(t, writer, "CREATE TABLE s(id INT)")
			sessionSQL(t, reader, "BEGIN ISOLATION LEVEL "+isolation)
			transactionRows(t, reader, "SELECT id FROM s")
			sessionSQL(t, writer, "INSERT INTO s VALUES(1)")
			want := int64(0)
			if isolation == "READ COMMITTED" {
				want = 1
			}
			updateDeleteExecute(t, e, reader, "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", want)
			sessionSQL(t, reader, "COMMIT")
			if want == 1 {
				transactionRows(t, writer, "SELECT id FROM t", 1)
			} else {
				transactionRows(t, writer, "SELECT id FROM t")
			}
			lockEmpty(t, e)
		})
	}
}
