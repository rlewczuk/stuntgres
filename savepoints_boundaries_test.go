package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestSavepointsDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, word string }{{"SAVEPOINT s", "SAVEPOINT"}, {"RELEASE s", "RELEASE SAVEPOINT"}, {"RELEASE SAVEPOINT s", "RELEASE SAVEPOINT"}, {"ROLLBACK TO s", "ROLLBACK TO SAVEPOINT"}, {"ROLLBACK WORK TO SAVEPOINT s", "ROLLBACK TO SAVEPOINT"}, {"ROLLBACK TRANSACTION TO s", "ROLLBACK TO SAVEPOINT"}} {
			t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, _, notices := controlsFixture(t)
				ctx := context.Background()
				_, err := transactionExec(ctx, a, tc.q, prepared)
				transactionDiagnostic(t, err, controlsDiagnostic("25P01", tc.word+" can only be used in transaction blocks"))
				controlsNotices(t, notices, "", "")
			})
		}
	}
	for _, q := range []string{`RELEASE "a""b"`, `ROLLBACK TO "a""b"`} {
		t.Run(q, func(t *testing.T) {
			_, a, _, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			transactionAffected(t, ctx, a, "SAVEPOINT known", false, 0)
			_, err := a.ExecContext(ctx, q)
			transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "a"b" does not exist`))
			for _, command := range []string{"SAVEPOINT other", "RELEASE known"} {
				_, err = a.ExecContext(ctx, command)
				transactionDiagnostic(t, err, controlsAborted())
			}
			transactionAffected(t, ctx, a, "ROLLBACK TO known", true, 0)
			transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
		})
	}
}

func TestSavepointsPrepareBoundaries(t *testing.T) {
	_, a, b, notices := controlsFixture(t)
	ctx := context.Background()
	prepare := func(q string) *sql.Stmt {
		t.Helper()
		s, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	create := prepare("SAVEPOINT s")
	release := prepare("RELEASE s")
	rollback := prepare("ROLLBACK TO s")
	_, err := rollback.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsDiagnostic("25P01", "ROLLBACK TO SAVEPOINT can only be used in transaction blocks"))
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	unknown := prepare("RELEASE missing")
	transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
	if _, err := create.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
	// Merely preparing rollback/release cannot undo rows or remove the mark.
	prepare("RELEASE s")
	prepare("ROLLBACK TO s")
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2)
	_, err = unknown.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "missing" does not exist`))
	for _, q := range []string{"SAVEPOINT s", "RELEASE s"} {
		_, err = a.PrepareContext(ctx, q)
		transactionDiagnostic(t, err, controlsAborted())
	}
	prepare("ROLLBACK TO s")
	_, err = release.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsAborted())
	_, err = create.ExecContext(ctx)
	transactionDiagnostic(t, err, controlsAborted())
	if _, err = rollback.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	if _, err = create.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	if _, err = rollback.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = release.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false, 1)
	controlsNotices(t, notices, "", "")
}

func TestSavepointsIsolation(t *testing.T) {
	for _, mode := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(mode, func(t *testing.T) {
			_, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
			transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL "+mode, false, 0)
			transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
			transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(9)", false, 1)
			transactionAffected(t, ctx, a, "ROLLBACK TO s", false, 0)
			want := []int64{1}
			if mode == "READ COMMITTED" {
				want = append(want, 9)
			}
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, want...)
		})
	}
	for _, command := range []string{"SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "SET transaction_isolation='repeatable read'", "SELECT set_config('transaction_isolation','repeatable read',false)"} {
		t.Run(command, func(t *testing.T) {
			_, a, _, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
			_, err := a.ExecContext(ctx, command)
			message := "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction"
			if command == "SELECT set_config('transaction_isolation','repeatable read',false)" {
				message = "SET TRANSACTION ISOLATION LEVEL must be called before any query"
			}
			transactionDiagnostic(t, err, controlsDiagnostic("25001", message))
			transactionAffected(t, ctx, a, "ROLLBACK TO s", false, 0)
			transactionAffected(t, ctx, a, "SET TRANSACTION READ ONLY", false, 0)
			_, err = a.ExecContext(ctx, "SET TRANSACTION READ WRITE")
			transactionDiagnostic(t, err, controlsDiagnostic("25001", "cannot set transaction read-write mode inside a read-only transaction"))
			transactionAffected(t, ctx, a, "ROLLBACK TO s", false, 0)
			transactionAffected(t, ctx, a, "SET TRANSACTION READ ONLY", false, 0)
			transactionAffected(t, ctx, a, "RELEASE s", false, 0)
			var got string
			if err := a.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestSavepointsSnapshotTimingAndNestedModes(t *testing.T) {
	_, a, _, _ := controlsFixture(t)
	ctx := context.Background()
	exec := func(q string) { transactionAffected(t, ctx, a, q, false, 0) }
	show := func(want string) {
		var got string
		if err := a.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	exec("BEGIN")
	exec("SAVEPOINT parent_mark")
	exec("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
	exec("SET TRANSACTION READ ONLY")
	exec("SAVEPOINT child")
	exec("RELEASE child")
	show("on")
	exec("RELEASE parent_mark")
	show("off")
	exec("SAVEPOINT s")
	transactionSelect(t, ctx, a, "SELECT v FROM t", false)
	exec("ROLLBACK TO s")
	exec("RELEASE s")
	_, err := a.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	transactionDiagnostic(t, err, controlsDiagnostic("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query"))
	exec("ROLLBACK")
}

func TestSavepointsSyntaxFirst(t *testing.T) {
	_, a, _, _ := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	transactionAffected(t, ctx, a, "SAVEPOINT s", false, 0)
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, message, position string }{{"SAVEPOINT", "syntax error at end of input", "10"}, {"RELEASE", "syntax error at end of input", "8"}, {"ROLLBACK TO", "syntax error at end of input", "12"}, {"ABORT TO s", `syntax error at or near "TO"`, "7"}} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+tc.q, func(t *testing.T) {
				_, err := transactionExec(ctx, a, tc.q, prepared)
				want := controlsDiagnostic("42601", tc.message)
				want.Position = tc.position
				transactionDiagnostic(t, err, want)
			})
		}
	}
	transactionAffected(t, ctx, a, "ROLLBACK TO s", true, 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
}

func TestSavepointsKeywordName(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			_, a, b, notices := controlsFixture(t)
			ctx := context.Background()
			for _, tc := range []struct{ q, tag string }{{"RELEASE SAVEPOINT", "RELEASE SAVEPOINT"}, {"ROLLBACK TO SAVEPOINT", "ROLLBACK TO SAVEPOINT"}} {
				_, err := transactionExec(ctx, a, tc.q, prepared)
				transactionDiagnostic(t, err, controlsDiagnostic("25P01", tc.tag+" can only be used in transaction blocks"))
			}
			exec := func(q string, n int64) { transactionAffected(t, ctx, a, q, prepared, n) }
			exec("BEGIN", 0)
			exec("INSERT INTO t VALUES(1)", 1)
			exec("SAVEPOINT SAVEPOINT", 0)
			exec("INSERT INTO t VALUES(2)", 1)
			exec("ROLLBACK TO SAVEPOINT", 0)
			transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
			exec("INSERT INTO t VALUES(3)", 1)
			exec("RELEASE SAVEPOINT", 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t", false)
			exec("COMMIT", 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3)
			exec("BEGIN", 0)
			exec("SAVEPOINT savepoint", 0)
			transactionDiagnostic(t, transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(NULL)", true), controlsNull())
			_, err := transactionExec(ctx, a, "RELEASE SAVEPOINT", prepared)
			transactionDiagnostic(t, err, controlsAborted())
			exec("ROLLBACK TO SAVEPOINT", 0)
			exec("INSERT INTO t VALUES(5)", 1)
			exec("RELEASE SAVEPOINT SAVEPOINT", 0)
			exec("COMMIT", 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3, 5)
			exec("BEGIN", 0)
			exec("SAVEPOINT known", 0)
			for range 2 {
				if prepared {
					stmt, err := a.PrepareContext(ctx, "ROLLBACK TO SAVEPOINT")
					if err != nil {
						t.Fatal("rollback keyword-name Prepare", err)
					}
					_, err = stmt.ExecContext(ctx)
					stmt.Close()
					transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "savepoint" does not exist`))
				} else {
					_, err := a.ExecContext(ctx, "ROLLBACK TO SAVEPOINT")
					transactionDiagnostic(t, err, controlsDiagnostic("3B001", `savepoint "savepoint" does not exist`))
				}
			}
			exec("ROLLBACK TO known", 0)
			exec("ROLLBACK", 0)
			controlsNotices(t, notices, "", "")
		})
	}
}
