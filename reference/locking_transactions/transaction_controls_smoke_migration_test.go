package reference_test

import (
	"context"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionControlsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, a, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, recorder)
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			m := fixture.MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: recorder}
			defer a.ExecContext(ctx, "ROLLBACK")
			exec := func(x fixture.Executor, q string, prepared bool, n int64, args ...any) {
				t.Helper()
				if err := m.TransactionAffected(ctx, x, q, prepared, n, args...); err != nil {
					t.Fatal(err)
				}
			}
			rows := func(x fixture.Executor, want ...int64) {
				t.Helper()
				if err := m.TransactionSelectValues(ctx, x, "SELECT v FROM t ORDER BY v", false, want...); err != nil {
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
			if err = m.TransactionDiagnostic(err, want); err != nil {
				t.Fatal(err)
			}
			_, err = begin.ExecContext(ctx)
			if err = m.TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}); err != nil {
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
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{fixture.MigrationDiagnostic(wantNotice)}) {
				t.Fatal("outside warning fields", notices)
			}
			notices = nil
			exec(a, "BEGIN", false, 0)
			if _, err = begin.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			wantNotice.Code = "25001"
			wantNotice.Message = "there is already a transaction in progress"
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{fixture.MigrationDiagnostic(wantNotice)}) {
				t.Fatal("redundant warning fields", notices)
			}
			exec(a, "ABORT", false, 0)
			rows(b, 1, 2, 4, 6)
		})
	}
}
