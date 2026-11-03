package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeCancellation(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stCancellation(t, p, b, c)
		stScanCancellation(t, p, b, c)
	})
}

func stCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT id,payload FROM sr TABLESAMPLE system_time($1)"
	h := srHeld(t, p, c, q)
	if !b.PG {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, executor := range []fixture.Executor{c,h} {
			rows, err := executor.QueryContext(ctx, q, 0.25)
			if rows != nil { rows.Close(); t.Fatal("already canceled sample published rows") }
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
	}
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'sr'::regclass::oid").Scan(&oid); err != nil { t.Fatal(err) }
	holder, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer holder.Close()
	defer holder.ExecContext(p.Ctx, "ROLLBACK")
	waiterPID, holderPID := 0, 0
	if b.PG {
		waiterPID = fixture.LockPID(t, p.Ctx, c)
		holderPID = fixture.LockPID(t, p.Ctx, holder)
	}
	for _, executor := range []fixture.Executor{c,h} {
		srRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
		srRun(t, p, b, holder, fixture.Exec("LOCK TABLE sr IN ACCESS EXCLUSIVE MODE", 0))
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG { fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock") }
		}, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := executor.QueryContext(ctx, q, 0.25)
			return fixture.RelationOutcome{Rows:rows,Err:err}
		})
		srRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		s := fixture.Select(q, srNames, srTypes, srRows(), "Infinity")
		s.Bag = true
		srRun(t, p, b, executor, s)
		stExclusive(t, p, b, "sr")
	}
}

// The wait is inside RLS's demanded source work, not an outer projection.
// Observe actual PgSleep/native sleep admission, then cancel and join. Infinity
// prevents sampling-budget EOF from racing the cancellation witness.
func stScanCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE ROLE st_scan_reader NOLOGIN NOSUPERUSER NOBYPASSRLS",
		"GRANT USAGE ON SCHEMA public TO st_scan_reader",
		"GRANT SELECT ON sr TO st_scan_reader",
		"ALTER TABLE sr ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY st_scan_wait ON sr FOR SELECT TO st_scan_reader USING(pg_sleep(30) IS NOT NULL)",
	} { srRun(t, p, b, c, fixture.Exec(q, 0)) }
	q := "SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity') ORDER BY id"
	for _, prepared := range []bool{false,true} {
		srRun(t, p, b, c, fixture.Exec("SET ROLE st_scan_reader", 0))
		var executor fixture.Executor = c
		var statement *sql.Stmt
		if prepared {
			var err error
			statement, err = c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer statement.Close()
			executor = fixture.HeldStatement{Executor:c,Statement:statement,SQL:q}
		}
		fixture.SleepActiveCancel(t, p, b, c, q, nil, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := executor.QueryContext(ctx, q)
			return fixture.RelationOutcome{Rows:rows,Err:err}
		})
		srRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		srRun(t, p, b, c, fixture.Exec("ALTER POLICY st_scan_wait ON sr USING(true)", 0))
		srRun(t, p, b, c, fixture.Exec("SET ROLE st_scan_reader", 0))
		srRun(t, p, b, executor, fixture.Select(q, srNames, srTypes, srRows()))
		srRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		if statement != nil { if err := statement.Close(); err != nil { t.Fatal(err) } }
		stExclusive(t, p, b, "sr")
		srRun(t, p, b, c, fixture.Exec("ALTER POLICY st_scan_wait ON sr USING(pg_sleep(30) IS NOT NULL)", 0))
	}
	for _, prepared := range []bool{false,true} {
		// The short timer's complete diagnostic proves real scan interruption.
		// Dispose of its armed owner rather than running RESET under that timer.
		t.Run(map[bool]string{false:"scan_timeout_direct",true:"scan_timeout_prepared"}[prepared], func(t *testing.T) {
			timed, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer timed.Close()
			srRun(t, p, b, timed, fixture.Exec("SET ROLE st_scan_reader", 0))
			var executor fixture.Executor = timed
			if prepared {
				st, err := timed.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer st.Close()
				executor = fixture.HeldStatement{Executor:timed,Statement:st,SQL:q}
			}
			srRun(t, p, b, timed, fixture.Exec("SET statement_timeout='100ms'", 0))
			defer timed.Raw(func(any) error { return driver.ErrBadConn })
			srQueryDiagnostic(t, p, executor, q, fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout"))
		})
		stExclusive(t, p, b, "sr")
	}
	srRun(t, p, b, c, fixture.Exec("DROP POLICY st_scan_wait ON sr", 0))
	srRun(t, p, b, c, fixture.Exec("ALTER TABLE sr DISABLE ROW LEVEL SECURITY", 0))
	srBoth(t, p, b, c, fixture.Select(q, srNames, srTypes, srRows()))
}
