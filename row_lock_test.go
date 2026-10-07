package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

var rowLockPublicConflicts = [4][4]bool{{false, false, false, true}, {false, false, true, true}, {false, true, true, true}, {true, true, true, true}}

func TestRowLockMatrix(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for requested := range rowLockPublicClauses {
			for held := range rowLockPublicClauses {
				t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+rowLockPublicClauses[requested]+"/"+rowLockPublicClauses[held], func(t *testing.T) {
					_, a, b := transactionConnections(t)
					ctx := context.Background()
					transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
					transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", false, 2)
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					transactionSelect(t, ctx, tx, "SELECT v FROM t WHERE id=1 "+rowLockPublicClauses[held], prepared, 10)
					transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=2 "+rowLockPublicClauses[requested], prepared, 20)
					transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1", prepared, 10)
					query := "SELECT v FROM t WHERE id=1 " + rowLockPublicClauses[requested]
					bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
					rows, closeQuery, err := transactionQuery(bounded, b, query, prepared)
					if rowLockPublicConflicts[requested][held] {
						if rows != nil {
							rows.Close()
							t.Fatal("conflict exposed cursor")
						}
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Fatalf("conflict: %v", err)
						}
						var diagnostic *stuntgres.Error
						if errors.As(err, &diagnostic) {
							t.Fatal("native context acquired SQLSTATE", diagnostic)
						}
					} else {
						if err != nil {
							t.Fatal("compatible modes blocked", err)
						}
						transactionValues(t, rows, 10)
					}
					if closeQuery != nil {
						closeQuery()
					}
					cancel()
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					transactionSelect(t, ctx, b, query, prepared, 10)
				})
			}
		}
	}
}
func TestRowLockDMLModes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for i, clause := range rowLockPublicClauses {
			for _, deleting := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+clause+map[bool]string{false: "/update", true: "/delete"}[deleting], func(t *testing.T) {
					_, a, b := transactionConnections(t)
					ctx := context.Background()
					transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
					transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", false, 2)
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					transactionSelect(t, ctx, tx, "SELECT v FROM t WHERE id=1 "+clause, prepared, 10)
					transactionAffected(t, ctx, b, "UPDATE t SET v=21 WHERE id=2", prepared, 1)
					query := "UPDATE t SET v=11 WHERE id=1"
					if deleting {
						query = "DELETE FROM t WHERE id=1"
					}
					bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
					result, err := transactionExec(bounded, b, query, prepared)
					cancel()
					if deleting || i != 0 {
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("DML conflict", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						n, err := result.RowsAffected()
						if err != nil || n != 1 {
							t.Fatal(n, err)
						}
					}
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					transactionAffected(t, ctx, b, query, prepared, 1)
					if deleting {
						transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1", prepared)
					} else {
						transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1", prepared, 11)
					}
				})
			}
		}
	}
}
func TestRowLockIndependence(t *testing.T) {
	for _, change := range []string{"UPDATE t SET v=99 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		t.Run(change, func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", false, 2)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			transactionAffected(t, ctx, tx, change, false, 1)
			transactionSelect(t, ctx, b, "SELECT v FROM t WHERE id=1", true, 10)
			transactionAffected(t, ctx, b, "DELETE FROM t WHERE id=2", true, 1)
		})
	}
}
func TestRowLockRepeatableRead(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clause := range rowLockPublicClauses {
			for _, change := range []string{"UPDATE t SET v=20 WHERE id=1", "DELETE FROM t WHERE id=1"} {
				t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+clause+"/"+change, func(t *testing.T) {
					_, a, b := transactionConnections(t)
					ctx := context.Background()
					transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
					transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10)", false, 1)
					tx, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					transactionSelect(t, ctx, tx, "SELECT v FROM t", false, 10)
					transactionAffected(t, ctx, a, change, false, 1)
					if clause == "FOR KEY SHARE" && change[0] == 'U' {
						transactionSelect(t, ctx, tx, "SELECT v FROM t "+clause, prepared, 10)
						transactionSelect(t, ctx, tx, "SELECT v FROM t WHERE v=10 "+clause, prepared, 10)
						return
					}
					rows, closeQuery, err := transactionQuery(ctx, tx, "SELECT v FROM t "+clause, prepared)
					if rows != nil {
						rows.Close()
						t.Fatal("serialization failure exposed rows")
					}
					if closeQuery != nil {
						defer closeQuery()
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "40001", Message: "could not serialize access due to concurrent update"})
					_, err = tx.ExecContext(ctx, "SELECT v FROM t")
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
				})
			}
		}
	}
}
func TestRowLockLifetime(t *testing.T) {
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "CREATE TABLE t(id INT,v INT)", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", false, 2)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "SELECT v FROM t WHERE id=1 FOR UPDATE")
	if err != nil {
		t.Fatal(err)
	}
	// Analysis relation authority is retained, but Prepare does not lock rows.
	transactionAffected(t, ctx, b, "UPDATE t SET v=11 WHERE id=1", false, 1)
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transactionValues(t, rows, 11)
	stmt.Close()
	transactionAffected(t, ctx, tx, "SAVEPOINT s", false, 0)
	transactionSelect(t, ctx, tx, "SELECT v FROM t WHERE id=2 FOR SHARE", false, 20)
	transactionAffected(t, ctx, tx, "ROLLBACK TO s", false, 0)
	// Existing native boundary intentionally retains outer-ticket contributions.
	for _, id := range []string{"1", "2"} {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		_, err = b.ExecContext(bounded, "UPDATE t SET v=30 WHERE id="+id)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("hold lost at cursor/statement/savepoint close", id, err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, b, "UPDATE t SET v=30", false, 2)
}
func TestRowLockBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	for _, suffix := range []string{"FOR UPDATE OF t", "FOR SHARE NOWAIT", "FOR KEY SHARE SKIP LOCKED", "FOR UPDATE FOR SHARE", "FOR UPDATE garbage"} {
		rows, err := db.Query("SELECT id FROM t " + suffix)
		if rows != nil {
			rows.Close()
			t.Fatal("excluded clause exposed rows", suffix)
		}
		if err == nil {
			t.Fatal("excluded clause accepted", suffix)
		}
		stmt, err := db.Prepare("SELECT id FROM t " + suffix)
		if stmt != nil {
			stmt.Close()
			t.Fatal("excluded prepare succeeded", suffix)
		}
		if err == nil {
			t.Fatal("excluded prepare accepted", suffix)
		}
	}
	execSQL(t, db, "UPDATE t SET id=2")
}

func TestRowLockCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "CREATE TABLE t(v INT)", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(10)", false, 1)
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			transactionSelect(t, ctx, holder, "SELECT v FROM t FOR UPDATE", false, 10)
			reader, err := b.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			rows, closeQuery, err := transactionQuery(bounded, reader, "SELECT v FROM t FOR KEY SHARE", prepared)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled locker exposed rows")
			}
			if closeQuery != nil {
				closeQuery()
			}
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			var diagnostic *stuntgres.Error
			if errors.As(err, &diagnostic) {
				t.Fatal("fabricated native context SQLSTATE", diagnostic)
			}
			_, err = reader.ExecContext(ctx, "SELECT v FROM t")
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
			if err := reader.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t FOR SHARE", prepared, 10)
		})
	}
}
