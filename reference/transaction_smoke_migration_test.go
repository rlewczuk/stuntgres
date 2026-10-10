package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

// These methods retain the original smoke routes; backend selection changes only
// capabilities, never the shared transaction assertions.
type migrationSmoke struct {
	t        *testing.T
	pg       bool
	native   *sql.DB
	recorder *referenceRecorder
}

func migrationDiagnostic(e stuntgres.Error) referenceDiagnostic {
	return referenceDiagnostic{Severity: e.Severity, SeverityUnlocalized: e.SeverityUnlocalized,
		Code: e.Code, Message: e.Message, Detail: e.Detail, Hint: e.Hint, Position: e.Position,
		InternalPosition: e.InternalPosition, InternalQuery: e.InternalQuery, Context: e.Context,
		Schema: e.Schema, Table: e.Table, Column: e.Column, DataType: e.DataType, Constraint: e.Constraint}
}

func (m migrationSmoke) reset(ctx context.Context, db *sql.DB) error {
	if !m.pg {
		return stuntgres.Reset(ctx, m.native)
	}
	_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS t CASCADE")
	return err
}

func TestReferenceTransactionSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			m := migrationSmoke{t: t, pg: backend.pg, native: p.native, recorder: backend.recorder}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			db := backend.db
			db.SetMaxOpenConns(3)
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			for _, prepared := range []bool{false, true} {
				if err := m.transactionVisibility(ctx, db, a, b, prepared); err != nil {
					t.Fatal(err)
				}
				if err := m.transactionFailures(ctx, db, a, b, prepared); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.transactionControls(ctx, db, a, b); err != nil {
				t.Fatal(err)
			}
			if err := m.transactionCancellation(ctx, db, a, b); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (m migrationSmoke) transactionExecute(ctx context.Context, x referenceExecutor, q string, prepared bool, args ...any) (sql.Result, error) {
	if !prepared {
		return x.ExecContext(ctx, q, args...)
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.ExecContext(ctx, args...)
}
func (m migrationSmoke) transactionAffected(ctx context.Context, x referenceExecutor, q string, prepared bool, want int64, args ...any) error {
	r, err := m.transactionExecute(ctx, x, q, prepared, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", q, err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("%s affected %d want %d", q, n, want)
	}
	return nil
}
func (m migrationSmoke) transactionValues(rows *sql.Rows, want ...int64) error {
	return m.transactionTypedValues(rows, "v", "INT4", want...)
}
func (m migrationSmoke) transactionTypedValues(rows *sql.Rows, name, typ string, want ...int64) error {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(columns, []string{name}) || len(types) != 1 || types[0].DatabaseTypeName() != typ || (!m.pg && types[0].ScanType() != reflect.TypeFor[int64]()) {
		return fmt.Errorf("wrong metadata %v %v", columns, types)
	}
	got := make([]int64, 0)
	for rows.Next() {
		var v any
		if err = rows.Scan(&v); err != nil {
			return err
		}
		referenceAssertRawValue(m.t, m.pg, typ, v)
		n, ok := v.(int64)
		if !ok {
			return fmt.Errorf("value has type %T", v)
		}
		got = append(got, n)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("rows %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("rows %v want %v", got, want)
		}
	}
	return nil
}
func (m migrationSmoke) transactionSelectValues(ctx context.Context, x referenceExecutor, q string, prepared bool, want ...int64) error {
	return m.transactionSelectTypedValues(ctx, x, q, prepared, "v", "INT4", want...)
}
func (m migrationSmoke) transactionSelectTypedValues(ctx context.Context, x referenceExecutor, q string, prepared bool, name, typ string, want ...int64) error {
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := x.PrepareContext(ctx, q)
		if e != nil {
			return e
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = x.QueryContext(ctx, q)
	}
	if err != nil {
		return err
	}
	if err := m.transactionTypedValues(rows, name, typ, want...); err != nil {
		return err
	}
	if x != m.native {
		expected := make([][]any, len(want))
		for i, v := range want {
			expected[i] = []any{v}
		}
		referenceViewRecorded(m.t, m.recorder, q, []string{name}, []string{typ}, expected, false)
	}
	return nil
}
func (m migrationSmoke) transactionDiagnostic(err error, want stuntgres.Error) error {
	expected := migrationDiagnostic(want)
	referenceAssertDiagnostic(m.t, err, &expected)
	return nil
}
func (m migrationSmoke) transactionOrdinary(err error, message string) error {
	if err == nil || err.Error() != message {
		return fmt.Errorf("ordinary error %v want %q", err, message)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return fmt.Errorf("ordinary error fabricated SQLSTATE: %w", err)
	}
	return nil
}
func (m migrationSmoke) transactionVisibility(ctx context.Context, db *sql.DB, a, b *sql.Conn, prepared bool) error {
	if err := m.reset(ctx, db); err != nil {
		return err
	}
	if err := m.transactionAffected(ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0); err != nil {
		return err
	}
	for _, commit := range []bool{false, true} {
		tx, err := a.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES($1)", prepared, 1, int64(1)); err != nil {
			return err
		}
		if err = m.transactionSelectValues(ctx, tx, "SELECT v FROM t ORDER BY v", prepared, 1); err != nil {
			return err
		}
		if err = m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared); err != nil {
			return err
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			return err
		}
		if commit {
			err = m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1)
		} else {
			err = m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
		}
		if err != nil {
			return err
		}
	}
	reader, err := b.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer reader.Rollback()
	if err = m.transactionSelectValues(ctx, reader, "SELECT v FROM t ORDER BY v", prepared, 1); err != nil {
		return err
	}
	cached, err := reader.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
	if err != nil {
		return err
	}
	defer cached.Close()
	if err = m.transactionAffected(ctx, a, "INSERT INTO t VALUES(3)", prepared, 1); err != nil {
		return err
	}
	rows, err := cached.QueryContext(ctx)
	if err != nil {
		return err
	}
	if err = m.transactionValues(rows, 1, 3); err != nil {
		return err
	}
	if err = m.transactionAffected(ctx, reader, "INSERT INTO t VALUES(2)", prepared, 1); err != nil {
		return err
	}
	if err = m.transactionSelectValues(ctx, reader, "SELECT v FROM t ORDER BY v", prepared, 1, 2, 3); err != nil {
		return err
	}
	if err = m.transactionSelectValues(ctx, a, "SELECT v FROM t ORDER BY v", prepared, 1, 3); err != nil {
		return err
	}
	return reader.Rollback()
}
func (m migrationSmoke) transactionFailures(ctx context.Context, db *sql.DB, a, b *sql.Conn, prepared bool) error {
	if err := m.reset(ctx, db); err != nil {
		return err
	}
	if err := m.transactionAffected(ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0); err != nil {
		return err
	}
	if err := m.transactionAffected(ctx, b, "INSERT INTO t VALUES(7)", prepared, 1); err != nil {
		return err
	}
	notNull := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
	_, err := m.transactionExecute(ctx, a, "INSERT INTO t VALUES(8),(NULL)", prepared)
	if err = m.transactionDiagnostic(err, notNull); err != nil {
		return err
	}
	if err = m.transactionSelectValues(ctx, a, "SELECT v FROM t ORDER BY v", prepared, 7); err != nil {
		return err
	}
	if err = m.transactionAffected(ctx, a, "INSERT INTO t VALUES(9)", prepared, 1); err != nil {
		return err
	}
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES(1)", prepared, 1); err != nil {
		return err
	}
	_, err = m.transactionExecute(ctx, tx, "INSERT INTO t VALUES(8),(NULL)", prepared)
	if err = m.transactionDiagnostic(err, notNull); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "SELECT v FROM missing")
	if err = m.transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}); err != nil {
		return err
	}
	if err = m.transactionOrdinary(tx.Commit(), "pq: could not complete operation in a failed transaction"); err != nil {
		return err
	}
	if err = m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 7, 9); err != nil {
		return err
	}
	tx, err = a.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES(10)", prepared, 1); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 7, 9, 10)
}
func (m migrationSmoke) transactionControls(ctx context.Context, db *sql.DB, a, b *sql.Conn) error {
	if m.pg {
		return nil
	}
	if err := m.reset(ctx, db); err != nil {
		return err
	}
	if err := m.transactionAffected(ctx, b, "CREATE TABLE t(v INT)", false, 0); err != nil {
		return err
	}
	if err := m.transactionAffected(ctx, b, "INSERT INTO t VALUES(77)", false, 1); err != nil {
		return err
	}
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES(1)", false, 1); err != nil {
		return err
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, m.native, &dump); err != nil {
		return err
	}
	if !strings.Contains(dump.String(), "VALUES (77)") || strings.Contains(dump.String(), "VALUES (1)") {
		return errors.New("Dump included private writes or lost committed data")
	}
	err = stuntgres.Restore(ctx, m.native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO missing VALUES(99)"))
	if err = m.transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`, Position: "36"}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77); err != nil {
		return err
	}
	tx, err = a.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES(2)", false, 1); err != nil {
		return err
	}
	if err = stuntgres.Reset(ctx, m.native); err != nil {
		return err
	}
	if err = m.transactionOrdinary(tx.Commit(), "stuntgres: catalog replaced before publication"); err != nil {
		return err
	}
	if err = stuntgres.Restore(ctx, m.native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(99)")); err != nil {
		return err
	}
	if err = m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", false, 99); err != nil {
		return err
	}
	tx, err = a.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES(3)", false, 1); err != nil {
		return err
	}
	if err = stuntgres.Restore(ctx, m.native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(55)")); err != nil {
		return err
	}
	if err = m.transactionOrdinary(tx.Commit(), "stuntgres: catalog replaced before publication"); err != nil {
		return err
	}
	return m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", false, 55)
}
func (m migrationSmoke) transactionCancellation(ctx context.Context, db *sql.DB, a, b *sql.Conn) error {
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	preCanceled, err := a.BeginTx(canceled, nil)
	if m.pg {
		// Pre-canceled pq admission and rollback race the adapter's context
		// watcher; their outcomes are not a PostgreSQL SQL contract. Dispose
		// any admitted transaction and explicitly retire its physical session
		// before asserting SQL state through an unaffected/fresh borrower.
		if preCanceled != nil {
			preCanceled.Rollback()
		}
		a.Raw(func(any) error { return driver.ErrBadConn })
		a.Close()
		a, err = db.Conn(ctx)
		if err != nil {
			return err
		}
		defer a.Close()
		if err := m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", false, 7, 9, 10); err != nil {
			return err
		}
		if err := m.transactionSelectValues(ctx, a, "SELECT v FROM t ORDER BY v", false, 7, 9, 10); err != nil {
			return err
		}
	} else {
		if preCanceled != nil {
			preCanceled.Rollback()
		}
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("pre-canceled BeginTx: %v", err)
		}
	}
	if err := m.reset(ctx, db); err != nil {
		return err
	}
	if err := m.transactionAffected(ctx, b, "CREATE TABLE t(v INT)", false, 0); err != nil {
		return err
	}
	txctx, txcancel := context.WithCancel(ctx)
	tx, err := a.BeginTx(txctx, nil)
	if err != nil {
		txcancel()
		return err
	}
	if err = m.transactionAffected(ctx, tx, "INSERT INTO t VALUES(1)", false, 1); err != nil {
		txcancel()
		tx.Rollback()
		return err
	}
	txcancel()
	err = tx.Commit()
	if !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("canceled Commit: %v", err)
	}
	// Releasing this borrower waits for automatic rollback's lease drain.
	if err = a.Close(); err != nil {
		return err
	}
	return m.transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", false)
}
