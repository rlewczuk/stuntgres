package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

type transactionExecutor interface {
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func transactionExec(ctx context.Context, x transactionExecutor, q string, prepared bool, args ...any) (sql.Result, error) {
	if !prepared {
		return x.ExecContext(ctx, q, args...)
	}
	s, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.ExecContext(ctx, args...)
}

// A constraint witness must prove the actual API boundary independently:
// successful preparation, then the execution-time constraint diagnostic.
func transactionConstraintExec(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool) error {
	t.Helper()
	if !prepared {
		_, err := x.ExecContext(ctx, q)
		return err
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		t.Fatalf("constraint INSERT must prepare successfully: %v", err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(ctx)
	return err
}

// transactionQuery keeps a prepared statement alive while its result is active.
// The caller closes rows before closing the statement, including on failure.
func transactionQuery(ctx context.Context, x transactionExecutor, q string, prepared bool) (*sql.Rows, func() error, error) {
	if !prepared {
		rows, err := x.QueryContext(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		return rows, rows.Close, nil
	}
	s, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.QueryContext(ctx)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	return rows, func() error {
		rowErr := rows.Close()
		return errors.Join(rowErr, s.Close())
	}, nil
}
func transactionAffected(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool, want int64, args ...any) {
	t.Helper()
	r, err := transactionExec(ctx, x, q, prepared, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	count, err := r.RowsAffected()
	if err != nil || count != want {
		t.Fatal(q, count, err)
	}
}
func transactionValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != reflect.TypeFor[int64]() {
		t.Fatal("metadata", types, err)
	}
	transactionRowValues(t, rows, want...)
}
func transactionRowValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	got := make([]int64, 0)
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		n, ok := v.(int64)
		if !ok {
			t.Fatalf("value type %T", v)
		}
		got = append(got, n)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if len(got) != len(want) {
		t.Fatalf("rows %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("rows %v want %v", got, want)
		}
	}
}
func transactionSelect(t *testing.T, ctx context.Context, x transactionExecutor, q string, prepared bool, want ...int64) {
	t.Helper()
	rows, closeQuery, err := transactionQuery(ctx, x, q, prepared)
	if err != nil {
		t.Fatal(q, err)
	}
	defer closeQuery()
	transactionValues(t, rows, want...)
	if err := closeQuery(); err != nil {
		t.Fatal(err)
	}
}
func transactionDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic got %#v (%v) want %#v", got, err, want)
	}
}
func transactionOrdinary(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || err.Error() != message {
		t.Fatalf("ordinary error %v want %q", err, message)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("fabricated SQLSTATE", err)
	}
}
func transactionConnections(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn) {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(3)
	ctx := context.Background()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return db, a, b
}
func TestTransactionPublicVisibilityAndRefresh(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			transactionAffected(t, ctx, b, "CREATE TABLE u(v INT)", false, 0)
			for _, commit := range []bool{false, true} {
				tx, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
				if err != nil {
					t.Fatal(err)
				}
				transactionAffected(t, ctx, tx, "INSERT INTO t VALUES($1)", prepared, 1, int64(1))
				transactionSelect(t, ctx, tx, "SELECT v FROM t ORDER BY v", prepared, 1)
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if !errors.Is(tx.Commit(), sql.ErrTxDone) || !errors.Is(tx.Rollback(), sql.ErrTxDone) {
					t.Fatal("public end not fenced")
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO t VALUES(8)"); !errors.Is(err, sql.ErrTxDone) {
					t.Fatal(err)
				}
				if commit {
					transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1)
				} else {
					transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared)
				}
			}
			reader, err := b.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Rollback()
			transactionSelect(t, ctx, reader, "SELECT v FROM t ORDER BY v", prepared, 1)
			stmt, err := reader.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", prepared, 1)
			rows, err := stmt.QueryContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			transactionValues(t, rows, 1, 3)
			transactionAffected(t, ctx, reader, "INSERT INTO t VALUES(2)", prepared, 1)
			writer, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			transactionAffected(t, ctx, writer, "INSERT INTO t VALUES(4),(4)", prepared, 2)
			transactionAffected(t, ctx, writer, "INSERT INTO u VALUES(9)", prepared, 1)
			transactionSelect(t, ctx, reader, "SELECT v FROM t ORDER BY v", prepared, 1, 2, 3)
			transactionSelect(t, ctx, reader, "SELECT v FROM u ORDER BY v", prepared)
			if err = writer.Commit(); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, reader, "SELECT v FROM t ORDER BY v", prepared, 1, 2, 3, 4, 4)
			transactionSelect(t, ctx, reader, "SELECT v FROM u ORDER BY v", prepared, 9)
			if err = reader.Rollback(); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", prepared, 1, 3, 4, 4)
		})
	}
}
func TestTransactionPublicFailuresAndAutocommit(t *testing.T) {
	notNull := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
	aborted := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(7)", prepared, 1)
			err := transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(8),(NULL)", prepared)
			transactionDiagnostic(t, err, notNull)
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", prepared, 7)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(9)", prepared, 1)
			for _, commit := range []bool{false, true} {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", prepared, 1)
				err = transactionConstraintExec(t, ctx, tx, "INSERT INTO t VALUES(8),(NULL)", prepared)
				transactionDiagnostic(t, err, notNull)
				_, err = tx.QueryContext(ctx, "SELECT v FROM missing")
				transactionDiagnostic(t, err, aborted)
				_, err = tx.PrepareContext(ctx, "SELECT v FROM t")
				transactionDiagnostic(t, err, aborted)
				_, err = tx.ExecContext(ctx, "SELECT * FROM t WHERE v = )")
				transactionDiagnostic(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"})
				if commit {
					transactionOrdinary(t, tx.Commit(), "pq: could not complete operation in a failed transaction")
				} else if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared, 7, 9)
			}
			syntaxTx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer syntaxTx.Rollback()
			transactionAffected(t, ctx, syntaxTx, "INSERT INTO t VALUES(1)", false, 1)
			syntax := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}
			for _, state := range []string{"active", "aborted"} {
				stmt, err := syntaxTx.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
				if stmt != nil {
					stmt.Close()
					t.Fatalf("%s malformed Prepare returned a statement", state)
				}
				transactionDiagnostic(t, err, syntax)
			}
			_, err = syntaxTx.ExecContext(ctx, "INSERT INTO t VALUES(2)")
			transactionDiagnostic(t, err, aborted)
			if err = syntaxTx.Rollback(); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared, 7, 9)
			for _, fail := range []string{"syntax", "prepare", "execute_prepared"} {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
				var cached *sql.Stmt
				if fail == "execute_prepared" {
					cached, err = tx.PrepareContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
				}
				if fail == "prepare" {
					_, err = tx.PrepareContext(ctx, "SELECT v FROM missing")
					sqlState(t, err, "42P01")
				} else {
					_, err = tx.ExecContext(ctx, "SELECT FROM")
					sqlState(t, err, "42601")
				}
				if cached != nil {
					_, err = cached.QueryContext(ctx)
					transactionDiagnostic(t, err, aborted)
					cached.Close()
				}
				_, err = tx.ExecContext(ctx, "INSERT INTO t VALUES(2)")
				transactionDiagnostic(t, err, aborted)
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(10)", prepared, 1)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", prepared, 7, 9, 10)
		})
	}
}
func TestTransactionOptionsAndExplicitUtilityBoundary(t *testing.T) {
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
	for _, level := range []sql.IsolationLevel{sql.LevelSerializable, sql.LevelSnapshot, sql.LevelLinearizable} {
		_, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: level})
		transactionOrdinary(t, err, "stuntgres: unsupported transaction isolation level")
	}
	for _, q := range []string{"CREATE TABLE forbidden(v INT)"} {
		tx, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
		_, err = tx.ExecContext(ctx, q)
		transactionDiagnostic(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
		tx.Rollback()
	}
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
	_, err := b.QueryContext(ctx, "SELECT v FROM forbidden")
	sqlState(t, err, "42P01")
}
func TestTransactionBeginContextCancellation(t *testing.T) {
	db, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.BeginTx(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	txctx, txcancel := context.WithCancel(ctx)
	tx, err := a.BeginTx(txctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
	txcancel()
	err = tx.Commit()
	if !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	// Conn.Close waits for the transaction's borrower lease to be released by
	// automatic rollback. Reborrow only after that deterministic drain.
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
	next, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, next, "INSERT INTO t VALUES(2)", false, 1)
	if err = next.Commit(); err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 2)
}
