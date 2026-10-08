package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
)

func lockModesBackends(t *testing.T, run func(*testing.T, context.Context, *sql.Conn, *sql.Conn)) {
	_, a, b := transactionConnections(t)
	run(t, context.Background(), a, b)
}
func lockModesDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	want := controlsDiagnostic(code, message)
	want.Detail = detail
	transactionDiagnostic(t, err, want)
}

var lockModesNames = []string{"ACCESS SHARE", "ROW SHARE", "ROW EXCLUSIVE", "SHARE UPDATE EXCLUSIVE", "SHARE", "SHARE ROW EXCLUSIVE", "EXCLUSIVE", "ACCESS EXCLUSIVE"}

// Literal PostgreSQL Table 13.2, requested rows / held columns; never derived
// from the implementation's conflict predicate or a strongest-mode ranking.
var lockModesConflicts = [8][8]bool{
	{false, false, false, false, false, false, false, true},
	{false, false, false, false, false, false, true, true},
	{false, false, false, false, true, true, true, true},
	{false, false, false, true, true, true, true, true},
	{false, false, true, true, false, true, true, true},
	{false, false, true, true, true, true, true, true},
	{false, true, true, true, true, true, true, true},
	{true, true, true, true, true, true, true, true},
}

func lockModesExec(t *testing.T, ctx context.Context, x *sql.Conn, q string) {
	t.Helper()
	r, err := x.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(q, n, err)
	}
}
func lockModesProbe(t *testing.T, ctx context.Context, b *sql.Conn, q, code, message string) {
	t.Helper()
	lockModesExec(t, ctx, b, "BEGIN")
	defer b.ExecContext(ctx, "ROLLBACK")
	r, err := b.ExecContext(ctx, q)
	if code != "" {
		lockModesDiagnostic(t, err, code, message, "")
	} else {
		if err != nil {
			t.Fatal(q, err)
		}
		n, e := r.RowsAffected()
		if e != nil || n != 0 {
			t.Fatal(n, e)
		}
	}
}
func TestLockModesMatrix(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		for held, h := range lockModesNames {
			for requested, r := range lockModesNames {
				t.Run(h+"/"+r, func(t *testing.T) {
					lockModesExec(t, ctx, a, "BEGIN")
					defer a.ExecContext(ctx, "ROLLBACK")
					lockModesExec(t, ctx, a, "LOCK TABLE t IN "+h+" MODE")
					lockModesExec(t, ctx, b, "BEGIN")
					defer b.ExecContext(ctx, "ROLLBACK")
					result, err := b.ExecContext(ctx, "LOCK TABLE t IN "+r+" MODE NOWAIT")
					if lockModesConflicts[requested][held] {
						lockModesDiagnostic(t, err, "55P03", `could not obtain lock on relation "t"`, "")
						_, err = b.ExecContext(ctx, "LOCK TABLE missing")
						lockModesDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
					} else {
						if err != nil {
							t.Fatal(err)
						}
						n, e := result.RowsAffected()
						if e != nil || n != 0 {
							t.Fatal(n, e)
						}
					}
					// The failed or successful requester never alters the holder's live block.
					lockModesExec(t, ctx, a, "LOCK TABLE t IN "+h+" MODE NOWAIT")
				})
			}
		}
		lockModesExec(t, ctx, a, "BEGIN")
		lockModesExec(t, ctx, a, "LOCK t")
		lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesExec(t, ctx, a, "ROLLBACK")
		lockModesExec(t, ctx, a, "BEGIN")
		for _, m := range lockModesNames {
			lockModesExec(t, ctx, a, "LOCK t IN "+m+" MODE NOWAIT")
		}
		lockModesExec(t, ctx, a, "ROLLBACK")
	})
}
func TestLockModesSyntaxAndPrepare(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE SEQUENCE seq")
		for _, q := range []string{"LOCK t", "LOCK missing", "LOCK seq", "LOCK ONLY(t) IN SHARE MODE NOWAIT"} {
			stmt, err := a.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("metadata preparation", q, err)
			}
			_, err = stmt.ExecContext(ctx)
			stmt.Close()
			lockModesDiagnostic(t, err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")
		}
		lockModesExec(t, ctx, a, "BEGIN READ ONLY")
		for _, m := range lockModesNames {
			stmt, err := a.PrepareContext(ctx, "LOCK t IN "+m+" MODE NOWAIT")
			if err != nil {
				t.Fatal(err)
			}
			result, err := stmt.ExecContext(ctx)
			stmt.Close()
			if err != nil {
				t.Fatal(err)
			}
			n, e := result.RowsAffected()
			if e != nil || n != 0 {
				t.Fatal(n, e)
			}
		}
		lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesExec(t, ctx, a, "ROLLBACK")
		tx, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		result, err := tx.ExecContext(ctx, "LOCK t IN ROW SHARE MODE")
		if err != nil {
			t.Fatal(err)
		}
		n, e := result.RowsAffected()
		if e != nil || n != 0 {
			t.Fatal(n, e)
		}
	})
}
func TestLockModesDiagnostics(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE SEQUENCE seq")
		cases := []struct{ q, code, message, detail string }{
			{"LOCK missing", "42P01", `relation "missing" does not exist`, ""},
			{"LOCK public.missing", "42P01", `relation "public.missing" does not exist`, ""},
			{"LOCK missing.t", "3F000", `schema "missing" does not exist`, ""},
			{"LOCK seq", "42809", `cannot lock relation "seq"`, "This operation is not supported for sequences."},
		}
		for _, c := range cases {
			for _, prepared := range []bool{false, true} {
				lockModesExec(t, ctx, a, "BEGIN")
				var err error
				if prepared {
					var stmt *sql.Stmt
					stmt, err = a.PrepareContext(ctx, c.q)
					if err != nil {
						t.Fatal(err)
					}
					_, err = stmt.ExecContext(ctx)
					stmt.Close()
				} else {
					_, err = a.ExecContext(ctx, c.q)
				}
				lockModesDiagnostic(t, err, c.code, c.message, c.detail)
				_, err = a.ExecContext(ctx, "LOCK t")
				lockModesDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
				lockModesExec(t, ctx, a, "ROLLBACK")
			}
		}
		lockModesExec(t, ctx, a, "BEGIN")
		lockModesExec(t, ctx, a, "LOCK t")
		lockModesProbe(t, ctx, b, "LOCK public.t NOWAIT", "55P03", `could not obtain lock on relation "public.t"`)
		lockModesExec(t, ctx, a, "ROLLBACK")
	})
}
func TestLockModesOrder(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "BEGIN")
		defer a.ExecContext(ctx, "ROLLBACK")
		lockModesExec(t, ctx, a, "LOCK t")
		lockModesProbe(t, ctx, b, "LOCK t, missing NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesProbe(t, ctx, b, "LOCK missing, t NOWAIT", "42P01", `relation "missing" does not exist`)
	})
}
func TestLockModesStandaloneMarkers(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, `CREATE TABLE "Mixed"(v INT)`)
		lockModesExec(t, ctx, a, "CREATE TABLE b(v INT)")
		for _, target := range []string{`ONLY public."Mixed"`, `ONLY(public."Mixed")`, `public."Mixed" *`, `ONLY public."Mixed", b *, ONLY(b)`} {
			lockModesExec(t, ctx, a, "BEGIN")
			lockModesExec(t, ctx, a, "LOCK "+target+" IN SHARE MODE")
			lockModesProbe(t, ctx, b, `LOCK "Mixed" IN ROW EXCLUSIVE MODE NOWAIT`, "55P03", `could not obtain lock on relation "Mixed"`)
			lockModesExec(t, ctx, a, "ROLLBACK")
		}
	})
}
func TestLockModesViews(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE VIEW v AS SELECT v FROM t")
		lockModesExec(t, ctx, a, "CREATE VIEW nested AS SELECT v FROM v")
		lockModesExec(t, ctx, a, "CREATE SEQUENCE seq")
		lockModesExec(t, ctx, a, "CREATE VIEW seqview AS SELECT last_value FROM seq")
		for _, target := range []string{"v", "ONLY(v)", "nested"} {
			lockModesExec(t, ctx, a, "BEGIN")
			lockModesExec(t, ctx, a, "LOCK "+target+" IN SHARE MODE")
			lockModesProbe(t, ctx, b, "LOCK t IN ROW EXCLUSIVE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
			lockModesProbe(t, ctx, b, "LOCK t IN ACCESS SHARE MODE NOWAIT", "", "")
			lockModesExec(t, ctx, a, "ROLLBACK")
		}
		lockModesExec(t, ctx, a, "BEGIN")
		lockModesExec(t, ctx, a, "LOCK seqview")
		lockModesExec(t, ctx, a, "ROLLBACK")
		lockModesExec(t, ctx, b, "BEGIN")
		lockModesExec(t, ctx, b, "LOCK t")
		lockModesProbe(t, ctx, a, "LOCK v IN ACCESS SHARE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
		lockModesExec(t, ctx, b, "ROLLBACK")
	})
}
func TestLockModesSavepoints(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		lockModesExec(t, ctx, a, "CREATE TABLE child(v INT)")
		for _, before := range []string{"ACCESS SHARE", "SHARE UPDATE EXCLUSIVE"} {
			t.Run(before, func(t *testing.T) {
				lockModesExec(t, ctx, a, "BEGIN")
				defer a.ExecContext(ctx, "ROLLBACK")
				lockModesExec(t, ctx, a, "LOCK t IN "+before+" MODE")
				lockModesExec(t, ctx, a, "SAVEPOINT s")
				for i := 0; i < 2; i++ {
					after := "ACCESS EXCLUSIVE"
					if before == "SHARE UPDATE EXCLUSIVE" {
						after = "SHARE"
					}
					lockModesExec(t, ctx, a, "LOCK t IN "+after+" MODE")
					lockModesExec(t, ctx, a, "LOCK child")
					lockModesExec(t, ctx, a, "SAVEPOINT nested")
					lockModesExec(t, ctx, a, "RELEASE nested")
					lockModesExec(t, ctx, a, "ROLLBACK TO s")
					lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "", "")
					lockModesProbe(t, ctx, b, "LOCK t IN ROW EXCLUSIVE MODE NOWAIT", "", "")
					lockModesProbe(t, ctx, b, "LOCK t NOWAIT", "55P03", `could not obtain lock on relation "t"`)
					if before == "SHARE UPDATE EXCLUSIVE" {
						lockModesProbe(t, ctx, b, "LOCK t IN SHARE UPDATE EXCLUSIVE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
					}
				}
				lockModesExec(t, ctx, a, "LOCK child")
				_, err := a.ExecContext(ctx, "LOCK missing")
				lockModesDiagnostic(t, err, "42P01", `relation "missing" does not exist`, "")
				// Error escape releases child contribution immediately, before ROLLBACK TO.
				lockModesProbe(t, ctx, b, "LOCK child NOWAIT", "", "")
				lockModesProbe(t, ctx, b, "LOCK t NOWAIT", "55P03", `could not obtain lock on relation "t"`)
				lockModesExec(t, ctx, a, "ROLLBACK TO s")
			})
		}
	})
}
func TestLockModesLifetime(t *testing.T) {
	lockModesBackends(t, func(t *testing.T, ctx context.Context, a, b *sql.Conn) {
		lockModesExec(t, ctx, a, "CREATE TABLE t(v INT)")
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			lockModesExec(t, ctx, a, "BEGIN")
			stmt, err := a.PrepareContext(ctx, "LOCK t IN SHARE MODE")
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.ExecContext(ctx)
			stmt.Close()
			if err != nil {
				t.Fatal(err)
			}
			lockModesProbe(t, ctx, b, "LOCK t IN ROW EXCLUSIVE MODE NOWAIT", "55P03", `could not obtain lock on relation "t"`)
			lockModesExec(t, ctx, a, end)
			lockModesProbe(t, ctx, b, "LOCK t IN ROW EXCLUSIVE MODE NOWAIT", "", "")
		}
	})
}
