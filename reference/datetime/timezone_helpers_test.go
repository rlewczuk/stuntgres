package reference_test

import (
	"context"
	"database/sql"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Admit a real cursor with its recorder, then consume it through the common
// runner after a session/control change. This preserves consumed-row ownership.
type timezoneAdmittedRows struct {
	fixture.Executor
	rows *sql.Rows
}

func (x timezoneAdmittedRows) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return x.rows, nil
}

func timezoneHeld(t *testing.T, p *fixture.Pair, x fixture.Executor, query string) fixture.HeldStatement {
	t.Helper()
	statement, err := x.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := statement.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: x, Statement: statement, SQL: query}
}

func timezoneTransactionClock(t *testing.T, p *fixture.Pair, tx *sql.Tx, record *fixture.Recorder) time.Time {
	t.Helper()
	var clock time.Time
	if err := tx.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, record), "SELECT TIMESTAMPTZ 'now' AS clock FROM tz_witness WHERE id=1").Scan(&clock); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, tx, record, fixture.Select("SELECT TIMESTAMPTZ 'now' AS clock FROM tz_witness WHERE id=1", []string{"clock"}, []string{"TIMESTAMPTZ"}, [][]any{{clock.UTC()}}))
	return clock.UTC()
}

func timezoneDestination(t *testing.T) (*sql.DB, *sql.DB, *fixture.Recorder) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name() + "/destination")
	if err != nil {
		t.Fatal(err)
	}
	native := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := native.Close(); err != nil {
			t.Error(err)
		}
	})
	wrapped, record := fixture.InsertSelectRecordedPool(t, connector)
	wrapped.SetMaxOpenConns(1)
	wrapped.SetMaxIdleConns(1)
	return native, wrapped, record
}

type timezoneFailingReader struct{ err error }

func (r timezoneFailingReader) Read([]byte) (int, error) { return 0, r.err }

type timezoneCancelReader struct {
	cancel context.CancelFunc
	sent   bool
}

func (r *timezoneCancelReader) Read(dst []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	r.cancel()
	return copy(dst, "CREATE TABLE replacement(v INTEGER);"), nil
}
