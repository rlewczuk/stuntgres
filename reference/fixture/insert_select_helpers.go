package fixture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"testing"
)

func InsertSelectAffected(t *testing.T, result sql.Result, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	LockAffected(t, result, want)
	id, err := result.LastInsertId()
	if id != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatalf("LastInsertId = %d, %v; want unsupported boundary", id, err)
	}
}

// Keep the INSERT family's result/LastInsertId contract without changing the
// common runner. Reads still use its metadata, duplicate-bag and recorder checks.
func InsertSelectRun(t *testing.T, ctx context.Context, x Executor, recorder *Recorder, s Step) {
	t.Helper()
	if s.SelectRows {
		RunStep(t, ctx, x, recorder, s)
		return
	}
	ctx = context.WithValue(ctx, RecorderKey{}, recorder)
	var stmt *sql.Stmt
	var err error
	if s.Prepared {
		stmt, err = x.PrepareContext(ctx, s.Query)
		if err != nil {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a handle")
			}
			State(t, err, s.State)
			CheckDiagnostic(t, err, s, "prepare")
			return
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	var result sql.Result
	if stmt != nil {
		result, err = stmt.ExecContext(ctx, s.Args...)
	} else {
		result, err = x.ExecContext(ctx, s.Query, s.Args...)
	}
	State(t, err, s.State)
	if err != nil {
		if result != nil {
			t.Fatal("failed Exec exposed a result")
		}
		CheckDiagnostic(t, err, s, "execute")
		return
	}
	if s.ErrorStage != "" {
		t.Fatalf("expected %s failure", s.ErrorStage)
	}
	InsertSelectAffected(t, result, err, s.Affected)
}

func InsertSelectRecordedPool(t *testing.T, connector driver.Connector) (*sql.DB, *Recorder) {
	t.Helper()
	wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, &Recorder{RawBackend: RawNative}
}
