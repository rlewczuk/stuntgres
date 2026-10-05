package smoke_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

func TestTransactionControlsSmoke(t *testing.T) {
	ctx := context.Background()
	base, err := stuntgres.NewDriver().OpenConnector("transaction-controls-smoke")
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
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
	defer a.ExecContext(ctx, "ROLLBACK")
	exec := func(x transactionExecutor, q string, prepared bool, n int64, args ...any) {
		t.Helper()
		if err := transactionAffected(ctx, x, q, prepared, n, args...); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(x transactionExecutor, want ...int64) {
		t.Helper()
		if err := transactionSelectValues(ctx, x, "SELECT v FROM t ORDER BY v", false, want...); err != nil {
			t.Fatal(err)
		}
	}
	exec(b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	begin, err := a.PrepareContext(ctx, "BEGIN")
	if err != nil {
		t.Fatal(err)
	}
	defer begin.Close()
	if len(notices) != 0 {
		t.Fatal("Prepare notice", notices)
	}
	exec(a, "BEGIN", false, 0)
	exec(a, "INSERT INTO t VALUES($1),($2)", true, 2, int64(1), int64(2))
	end, err := a.PrepareContext(ctx, "END")
	if err != nil {
		t.Fatal(err)
	}
	defer end.Close()
	rows(a, 1, 2)
	rows(b)
	if len(notices) != 0 {
		t.Fatal("Prepare changed notice state", notices)
	}
	if r, err := end.ExecContext(ctx); err != nil {
		t.Fatal(err)
	} else if n, _ := r.RowsAffected(); n != 0 {
		t.Fatal(n)
	}
	rows(b, 1, 2)
	exec(a, "START TRANSACTION", true, 0)
	exec(a, "INSERT INTO t VALUES(3)", false, 1)
	exec(a, "ABORT WORK", true, 0)
	rows(b, 1, 2)
	exec(a, "BEGIN TRANSACTION", false, 0)
	bad, err := a.PrepareContext(ctx, "INSERT INTO t VALUES(8),(NULL)")
	if err != nil {
		t.Fatal("constraint Prepare must succeed", err)
	}
	_, err = bad.ExecContext(ctx)
	bad.Close()
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
	if err = transactionDiagnostic(err, want); err != nil {
		t.Fatal(err)
	}
	_, err = begin.ExecContext(ctx)
	if err = transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}); err != nil {
		t.Fatal(err)
	}
	exec(a, "COMMIT", true, 0)
	rows(b, 1, 2)
	exec(a, "BEGIN WORK", false, 0)
	exec(a, "INSERT INTO t VALUES(4)", true, 1)
	chain, err := a.PrepareContext(ctx, "END TRANSACTION AND CHAIN")
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Close()
	rows(b, 1, 2)
	if r, err := chain.ExecContext(ctx); err != nil {
		t.Fatal(err)
	} else if n, _ := r.RowsAffected(); n != 0 {
		t.Fatal(n)
	}
	rows(b, 1, 2, 4)
	exec(a, "INSERT INTO t VALUES(5)", true, 1)
	rows(a, 1, 2, 4, 5)
	rows(b, 1, 2, 4)
	exec(a, "ROLLBACK AND NO CHAIN", true, 0)
	rows(b, 1, 2, 4)
	exec(a, "INSERT INTO t VALUES(6)", true, 1)
	rows(b, 1, 2, 4, 6)
	exec(a, "ABORT TRANSACTION", true, 0)
	wantNotice := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"}
	if !reflect.DeepEqual(notices, []stuntgres.Error{wantNotice}) {
		t.Fatal("outside warning fields", notices)
	}
	notices = nil
	exec(a, "BEGIN", false, 0)
	if _, err = begin.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	wantNotice.Code = "25001"
	wantNotice.Message = "there is already a transaction in progress"
	if !reflect.DeepEqual(notices, []stuntgres.Error{wantNotice}) {
		t.Fatal("redundant warning fields", notices)
	}
	exec(a, "ABORT", false, 0)
	rows(b, 1, 2, 4, 6)
}
