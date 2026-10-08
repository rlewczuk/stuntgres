package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"testing"
)

func insertSelectAffected(t *testing.T, result sql.Result, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	referenceLockAffected(t, result, want)
	id, err := result.LastInsertId()
	if id != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatalf("LastInsertId = %d, %v; want unsupported boundary", id, err)
	}
}

// Keep the INSERT family's result/LastInsertId contract without changing the
// common runner. Reads still use its metadata, duplicate-bag and recorder checks.
func insertSelectRun(t *testing.T, ctx context.Context, x referenceExecutor, recorder *referenceRecorder, s referenceStep) {
	t.Helper()
	if s.selectRows {
		referenceRunStep(t, ctx, x, recorder, s)
		return
	}
	ctx = context.WithValue(ctx, referenceRecorderKey{}, recorder)
	var stmt *sql.Stmt
	var err error
	if s.prepared {
		stmt, err = x.PrepareContext(ctx, s.query)
		if err != nil {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a handle")
			}
			referenceState(t, err, s.state)
			referenceCheckDiagnostic(t, err, s, "prepare")
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
		result, err = stmt.ExecContext(ctx, s.args...)
	} else {
		result, err = x.ExecContext(ctx, s.query, s.args...)
	}
	referenceState(t, err, s.state)
	if err != nil {
		if result != nil {
			t.Fatal("failed Exec exposed a result")
		}
		referenceCheckDiagnostic(t, err, s, "execute")
		return
	}
	if s.errorStage != "" {
		t.Fatalf("expected %s failure", s.errorStage)
	}
	insertSelectAffected(t, result, err, s.affected)
}

func insertSelectRecordedPool(t *testing.T, connector driver.Connector) (*sql.DB, *referenceRecorder) {
	t.Helper()
	wrapped := sqlmw.Driver(noticeReferenceDriver{connector}, referenceInterceptor{})
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
	return db, &referenceRecorder{rawBackend: referenceRawNative}
}
