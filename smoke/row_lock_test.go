package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func rowLockSmokeFixture(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(3)
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)"} {
		if _, err := a.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	return db, a, b
}
func rowLockSmokeCheck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func rowLockSmokeBlocked(t *testing.T, x transactionExecutor, q string, prepared bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := transactionExecute(ctx, x, q, prepared)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("conflict %s: %v", q, err)
	}
	var diagnostic *stuntgres.Error
	if errors.As(err, &diagnostic) {
		t.Fatal("native context error acquired SQLSTATE", diagnostic)
	}
}
func TestRowLockSmoke(t *testing.T) {
	ctx := context.Background()
	_, a, b := rowLockSmokeFixture(t)
	for _, prepared := range []bool{false, true} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				rowLockSmokeCheck(t, transactionSelectValues(ctx, tx, "SELECT v FROM t WHERE id=1 "+clause, prepared, 10))
				rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", prepared, 20))
				rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1", prepared, 10))
				if clause == "FOR KEY SHARE" {
					rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=10 WHERE id=1", prepared, 1))
				} else {
					rowLockSmokeBlocked(t, b, "UPDATE t SET v=11 WHERE id=1", prepared)
				}
				rowLockSmokeBlocked(t, b, "DELETE FROM t WHERE id=1", prepared)
			}()
			rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=10 WHERE id=1", prepared, 1))
		}
	}
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "SELECT v FROM t WHERE id=1 FOR UPDATE")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	stmt.Close()
	rowLockSmokeBlocked(t, b, "UPDATE t SET v=11 WHERE id=1", false)
	rowLockSmokeCheck(t, tx.Commit())
	rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=11 WHERE id=1", true, 1))
}
func TestRowLockJoinSmoke(t *testing.T) {
	ctx := context.Background()
	_, a, b := rowLockSmokeFixture(t)
	rowLockSmokeCheck(t, transactionAffected(ctx, a, "CREATE TABLE u(id INT)", false, 0))
	rowLockSmokeCheck(t, transactionAffected(ctx, a, "INSERT INTO u VALUES(1),(3)", false, 2))
	for _, prepared := range []bool{false, true} {
		for _, from := range []string{"t JOIN u ON t.id=u.id", "t JOIN u USING(id)", "t a JOIN t b ON a.id=b.id"} {
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				query := "SELECT t.v FROM " + from + " WHERE t.id=1 FOR SHARE"
				if from == "t a JOIN t b ON a.id=b.id" {
					query = "SELECT a.v FROM " + from + " WHERE a.id=1 FOR SHARE"
				}
				rowLockSmokeCheck(t, transactionSelectValues(ctx, tx, query, prepared, 10))
				rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=20 WHERE id=2", prepared, 1))
				rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE u SET id=3 WHERE id=3", prepared, 1))
				rowLockSmokeBlocked(t, b, "UPDATE t SET v=11 WHERE id=1", prepared)
				if from != "t a JOIN t b ON a.id=b.id" {
					rowLockSmokeBlocked(t, b, "DELETE FROM u WHERE id=1", prepared)
				}
			}()
		}
	}
}
func TestRowLockVersionSmoke(t *testing.T) {
	ctx := context.Background()
	_, a, b := rowLockSmokeFixture(t)
	// Public boundaries and current-head outcomes accompany deterministic native
	// journal and PostgreSQL wait-observer tests; deadlines are not queue proof.
	for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		tx, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		rowLockSmokeCheck(t, transactionAffected(ctx, tx, change, false, 1))
		rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1", true, 10))
		rowLockSmokeBlocked(t, b, "SELECT v FROM t WHERE id=1 FOR SHARE", false)
		rowLockSmokeCheck(t, tx.Commit())
		if change[0] == 'D' {
			rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1 FOR SHARE", true))
		} else {
			rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE id=1 FOR SHARE", true, 30))
			rowLockSmokeCheck(t, transactionSelectValues(ctx, b, "SELECT v FROM t WHERE v=10 FOR UPDATE", false))
			rowLockSmokeCheck(t, transactionAffected(ctx, b, "UPDATE t SET v=10 WHERE id=1", false, 1))
		}
	}
	for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
		rowLockSmokeCheck(t, transactionAffected(ctx, a, "INSERT INTO t VALUES(1,10)", false, 1))
		reader, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		rowLockSmokeCheck(t, transactionSelectValues(ctx, reader, "SELECT v FROM t WHERE id=1", false, 10))
		rowLockSmokeCheck(t, transactionAffected(ctx, a, change, false, 1))
		if change[0] == 'U' {
			for _, prepared := range []bool{false, true} {
				rowLockSmokeCheck(t, transactionSelectValues(ctx, reader, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", prepared, 10))
				rowLockSmokeCheck(t, transactionSelectValues(ctx, reader, "SELECT v FROM t WHERE v=10 FOR KEY SHARE", prepared, 10))
			}
			rowLockSmokeCheck(t, reader.Rollback())
			rowLockSmokeCheck(t, transactionAffected(ctx, a, "DELETE FROM t WHERE id=1", false, 1))
			continue
		}
		rows, err := reader.QueryContext(ctx, "SELECT v FROM t WHERE id=1 FOR KEY SHARE")
		if rows != nil {
			rows.Close()
			reader.Rollback()
			t.Fatal("RR failure exposed cursor")
		}
		var diagnostic *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			reader.Rollback()
			t.Fatal(err, diagnostic)
		}
		rowLockSmokeCheck(t, reader.Rollback())
		rowLockSmokeCheck(t, transactionAffected(ctx, a, "DELETE FROM t WHERE id=1", false, map[bool]int64{false: 1, true: 0}[change[0] == 'D']))
	}
}
