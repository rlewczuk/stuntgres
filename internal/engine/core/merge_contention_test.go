package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestMergeInsertContention(t *testing.T) {
	for _, table := range []string{"t", "s"} {
		for _, prepare := range []bool{false, true} {
			t.Run(table+map[bool]string{false: "/execute", true: "/prepare"}[prepare], func(t *testing.T) {
				e := engine.New()
				holder := enginetest.StartSession(t, e, nil)
				worker := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE s(id INT)", "INSERT INTO s VALUES(1)"} {
					enginetest.SessionSQL(t, holder, q)
				}
				enginetest.SessionSQL(t, holder, "BEGIN")
				enginetest.SessionSQL(t, holder, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				q := "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
				done := enginetest.LockAsync(e, worker, ctx, q, prepare)
				enginetest.LockPending(t, e, 1)
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
				enginetest.SessionSQL(t, holder, "ROLLBACK")
				enginetest.TransactionRows(t, holder, "SELECT id FROM t")
				updateDeleteExecute(t, e, worker, q, 1)
				enginetest.TransactionRows(t, holder, "SELECT id FROM t", 1)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestMergeInsertConcurrentUnique(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, holder, "CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)")
			enginetest.SessionSQL(t, holder, "BEGIN")
			enginetest.SessionSQL(t, holder, "INSERT INTO t VALUES(7)")
			join := updateDeleteAsync(t, e, worker, context.Background(), "MERGE INTO t USING (VALUES(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)")
			enginetest.LockPending(t, e, 1)
			if commit {
				enginetest.SessionSQL(t, holder, "COMMIT")
			} else {
				enginetest.SessionSQL(t, holder, "ROLLBACK")
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
			enginetest.TransactionRows(t, holder, "SELECT id FROM t", 7)
			uniqueClaimsEmpty(t, e)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestMergeInsertIsolation(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(isolation, func(t *testing.T) {
			e := engine.New()
			writer := enginetest.StartSession(t, e, nil)
			reader := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "CREATE TABLE t(id INT)")
			enginetest.SessionSQL(t, writer, "CREATE TABLE s(id INT)")
			enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL "+isolation)
			enginetest.TransactionRows(t, reader, "SELECT id FROM s")
			enginetest.SessionSQL(t, writer, "INSERT INTO s VALUES(1)")
			want := int64(0)
			if isolation == "READ COMMITTED" {
				want = 1
			}
			updateDeleteExecute(t, e, reader, "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", want)
			enginetest.SessionSQL(t, reader, "COMMIT")
			if want == 1 {
				enginetest.TransactionRows(t, writer, "SELECT id FROM t", 1)
			} else {
				enginetest.TransactionRows(t, writer, "SELECT id FROM t")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
