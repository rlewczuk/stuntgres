package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func controlsDiagnostic(code, message string) stuntgres.Error {
	return stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func controlsAborted() stuntgres.Error {
	return controlsDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}
func controlsNull() stuntgres.Error {
	return stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
}
func controlsFixture(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn, *[]stuntgres.Error) {
	t.Helper()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	notices := new([]stuntgres.Error)
	db := sql.OpenDB(connector.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { *notices = append(*notices, n) }))
	t.Cleanup(func() { db.Close() })
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.ExecContext(context.Background(), "ROLLBACK"); a.Close(); b.Close() })
	transactionAffected(t, context.Background(), b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	return db, a, b, notices
}
func controlsNotices(t *testing.T, records *[]stuntgres.Error, code, message string) {
	t.Helper()
	var want []stuntgres.Error
	if code != "" {
		n := controlsDiagnostic(code, message)
		n.Severity = "WARNING"
		n.SeverityUnlocalized = "WARNING"
		want = []stuntgres.Error{n}
	}
	if len(*records) != len(want) || (len(want) > 0 && !reflect.DeepEqual(*records, want)) {
		t.Fatalf("notices got %#v want %#v", *records, want)
	}
	*records = nil
}
func TestTransactionControlsStateMatrix(t *testing.T) {
	queries := []string{"BEGIN", "BEGIN WORK", "BEGIN TRANSACTION", "START TRANSACTION"}
	for _, word := range []string{"COMMIT", "END", "ROLLBACK", "ABORT"} {
		for _, optional := range []string{"", " WORK", " TRANSACTION"} {
			for _, suffix := range []string{"", " AND CHAIN", " AND NO CHAIN"} {
				queries = append(queries, word+optional+suffix)
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, state := range []string{"idle", "active", "aborted"} {
			for _, q := range queries {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+state+"/"+strings.ReplaceAll(q, " ", "_"), func(t *testing.T) {
					_, a, b, notices := controlsFixture(t)
					ctx := context.Background()
					start := strings.HasPrefix(q, "BEGIN") || strings.HasPrefix(q, "START")
					commit := strings.HasPrefix(q, "COMMIT") || strings.HasPrefix(q, "END")
					chain := strings.HasSuffix(q, "AND CHAIN")
					var cached *sql.Stmt
					if prepared {
						var err error
						cached, err = a.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal("eligible control Prepare", err)
						}
						defer cached.Close()
						controlsNotices(t, notices, "", "")
					}
					if state != "idle" {
						transactionAffected(t, ctx, a, "BEGIN", false, 0)
						transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
						if state == "aborted" {
							err := transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(8),(NULL)", true)
							transactionDiagnostic(t, err, controlsNull())
						}
					}
					if prepared && state == "aborted" {
						s, err := a.PrepareContext(ctx, q)
						if start {
							transactionDiagnostic(t, err, controlsAborted())
							if s != nil {
								t.Fatal("failed Prepare returned handle")
							}
						} else {
							if err != nil {
								t.Fatal(err)
							}
							s.Close()
							_, err = a.ExecContext(ctx, "SELECT v FROM t")
							transactionDiagnostic(t, err, controlsAborted())
						}
						controlsNotices(t, notices, "", "")
					}
					transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
					var result sql.Result
					var err error
					if cached != nil {
						result, err = cached.ExecContext(ctx)
					} else {
						result, err = a.ExecContext(ctx, q)
					}
					if start && state == "aborted" {
						transactionDiagnostic(t, err, controlsAborted())
						controlsNotices(t, notices, "", "")
						_, err = a.ExecContext(ctx, "SELECT v FROM t")
						transactionDiagnostic(t, err, controlsAborted())
						transactionSelect(t, ctx, b, "SELECT v FROM t", false)
						return
					}
					if !start && state == "idle" && chain {
						word := "COMMIT"
						if !commit {
							word = "ROLLBACK"
						}
						transactionDiagnostic(t, err, controlsDiagnostic("25P01", word+" AND CHAIN can only be used in transaction blocks"))
						controlsNotices(t, notices, "", "")
						transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
						transactionSelect(t, ctx, b, "SELECT v FROM t", false, 2)
						return
					}
					if err != nil {
						t.Fatal(q, err)
					}
					n, err := result.RowsAffected()
					if err != nil || n != 0 {
						t.Fatal(n, err)
					}
					if start && state == "active" {
						controlsNotices(t, notices, "25001", "there is already a transaction in progress")
					} else if !start && state == "idle" {
						controlsNotices(t, notices, "25P01", "there is no transaction in progress")
					} else {
						controlsNotices(t, notices, "", "")
					}
					var published []int64
					if commit && state == "active" && !start {
						published = []int64{1}
					}
					transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, published...)
					transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
					if start || chain {
						transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, published...)
						transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
					} else {
						published = append(published, 2)
					}
					transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", true, published...)
				})
			}
		}
	}
}

func TestTransactionControlsPrepareBoundaries(t *testing.T) {
	_, a, b, notices := controlsFixture(t)
	ctx := context.Background()
	begin, err := a.PrepareContext(ctx, "BEGIN")
	if err != nil {
		t.Fatal(err)
	}
	defer begin.Close()
	controlsNotices(t, notices, "", "")
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(7)", false, 1)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 7)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	end, err := a.PrepareContext(ctx, "END WORK AND CHAIN")
	if err != nil {
		t.Fatal(err)
	}
	defer end.Close()
	controlsNotices(t, notices, "", "")
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 7)
	if _, err = begin.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	controlsNotices(t, notices, "25001", "there is already a transaction in progress")
	transactionDiagnostic(t, transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(8),(NULL)", true), controlsNull())
	_, err = begin.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsAborted())
	recovery, err := a.PrepareContext(ctx, "COMMIT")
	if err != nil {
		t.Fatal("aborted end Prepare", err)
	}
	defer recovery.Close()
	_, err = a.ExecContext(ctx, "SELECT v FROM t")
	transactionDiagnostic(t, err, controlsAborted())
	for _, q := range []string{"COMMIT AND NO", "BEGIN AND CHAIN", "ROLLBACK WORK TRANSACTION", "SELECT * FROM t WHERE v = )"} {
		positions := map[string]string{"COMMIT AND NO": "14", "BEGIN AND CHAIN": "7", "ROLLBACK WORK TRANSACTION": "15", "SELECT * FROM t WHERE v = )": "27"}
		messages := map[string]string{"COMMIT AND NO": "syntax error at end of input", "BEGIN AND CHAIN": `syntax error at or near "AND"`, "ROLLBACK WORK TRANSACTION": `syntax error at or near "TRANSACTION"`, "SELECT * FROM t WHERE v = )": `syntax error at or near ")"`}
		want := controlsDiagnostic("42601", messages[q])
		want.Position = positions[q]
		_, err = a.ExecContext(ctx, q)
		transactionDiagnostic(t, err, want)
		s, err := a.PrepareContext(ctx, q)
		transactionDiagnostic(t, err, want)
		if s != nil {
			t.Fatal("malformed Prepare returned handle")
		}
	}
	if r, err := recovery.ExecContext(ctx); err != nil {
		t.Fatal(err)
	} else if n, _ := r.RowsAffected(); n != 0 {
		t.Fatal(n)
	}
	controlsNotices(t, notices, "", "")
	_, err = end.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsDiagnostic("25P01", "COMMIT AND CHAIN can only be used in transaction blocks"))
	if _, err = begin.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(9)", true, 1)
	if _, err = end.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", true, 7, 9)
	transactionAffected(t, ctx, a, "ROLLBACK AND NO CHAIN", false, 0)
	if _, err = recovery.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	controlsNotices(t, notices, "25P01", "there is no transaction in progress")
}

func TestTransactionControlsVisibilityAndRecovery(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN", prepared, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES($1),($2)", true, 2, int64(1), int64(2))
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", true, 1, 2)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
			transactionAffected(t, ctx, a, "END", prepared, 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", true, 1, 2)
			transactionAffected(t, ctx, a, "START TRANSACTION", prepared, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", prepared, 1)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(7)", false, 1)
			transactionAffected(t, ctx, a, "ABORT WORK", prepared, 0)
			transactionAffected(t, ctx, a, "BEGIN TRANSACTION", prepared, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", prepared, 1)
			transactionDiagnostic(t, transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(8),(NULL)", true), controlsNull())
			_, err := a.ExecContext(ctx, "SELECT v FROM t")
			transactionDiagnostic(t, err, controlsAborted())
			transactionAffected(t, ctx, a, "COMMIT", prepared, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(9)", true, 1)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", true, 1, 2, 7, 9)
		})
	}
}
func TestTransactionControlsChainVisibility(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "COMMIT WORK AND CHAIN", "ABORT TRANSACTION AND CHAIN"} {
		transactionAffected(t, ctx, a, "BEGIN", false, 0)
		transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
		if strings.Contains(q, "WORK") || strings.HasPrefix(q, "ABORT") {
			transactionDiagnostic(t, transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(NULL)", true), controlsNull())
		}
		transactionAffected(t, ctx, a, q, true, 0)
		transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", true, 1)
		want := []int64{}
		if q == "COMMIT AND CHAIN" {
			want = []int64{1}
		}
		transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, want...)
		transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
		transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, want...)
		transactionAffected(t, ctx, b, "DROP TABLE t", false, 0)
		transactionAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	}
}
func TestTransactionControlsGoInterop(t *testing.T) {
	_, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	defaultTx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, defaultTx, "INSERT INTO t VALUES(21)", true, 1)
	if err = defaultTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, b, "SELECT v FROM t", false)
	for _, chain := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			tx, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(13)", true, 1)
			q := "COMMIT"
			if chain {
				q += " AND CHAIN"
			}
			transactionAffected(t, ctx, tx, q, false, 0)
			if !chain {
				transactionAffected(t, ctx, tx, "BEGIN", false, 0)
			}
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(17)", false, 1)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 13)
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			transactionOrdinary(t, err, "stuntgres: stale transaction reference")
			if !errors.Is(tx.Commit(), sql.ErrTxDone) || !errors.Is(tx.Rollback(), sql.ErrTxDone) {
				t.Fatal("double end")
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 13)
			transactionAffected(t, ctx, a, "END", false, 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 13, 17)
			transactionAffected(t, ctx, b, "DROP TABLE t", false, 0)
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
		}
	}
}
