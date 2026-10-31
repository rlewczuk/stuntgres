package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func pgSeclabelAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	pgSeclabelExec(t, p, b, c, "CREATE ROLE sl_acl_reader", "SET ROLE sl_acl_reader")
	// Cleanup runs before scenario connection cleanup, even after Fatal.
	t.Cleanup(func() {
		c.ExecContext(p.Ctx, "ROLLBACK")
		c.ExecContext(p.Ctx, "RESET ROLE")
		c.ExecContext(p.Ctx, "REVOKE SELECT(provider,label) ON pg_catalog.pg_seclabel FROM sl_acl_reader")
		c.ExecContext(p.Ctx, "REVOKE SELECT ON pg_catalog.pg_seclabel FROM sl_acl_reader")
		c.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_seclabel TO PUBLIC")
		c.ExecContext(p.Ctx, "DROP ROLE sl_acl_reader")
	})
	full := pgSeclabelHeld(t, p, c, pgSeclabelSelect)
	provider := pgSeclabelHeld(t, p, c, "SELECT provider FROM pg_catalog.pg_seclabel")
	pgSeclabelEmpty(t, p, b, c)
	fixture.RunStep(t, p.Ctx, full, b.Recorder, pgSeclabelMetadata(b, full.SQL))
	pgSeclabelExec(t, p, b, c, "RESET ROLE", "REVOKE SELECT ON pg_catalog.pg_seclabel FROM PUBLIC", "SET ROLE sl_acl_reader")
	denied := pgSeclabelDiagnostic("42501", "permission denied for table pg_seclabel")
	checkDenied := func(x fixture.Executor, q string) {
		s := fixture.ErrorDiagnostic(q, denied)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	}
	checkDenied(c, pgSeclabelSelect)
	checkDenied(full, full.SQL)
	checkDenied(provider, provider.SQL)
	// Preparing must not cache the old PUBLIC privilege or deny new handles.
	late := pgSeclabelHeld(t, p, c, pgSeclabelSelect)
	checkDenied(late, late.SQL)
	pgSeclabelExec(t, p, b, c, "RESET ROLE", "GRANT SELECT(provider) ON pg_catalog.pg_seclabel TO sl_acl_reader", "SET ROLE sl_acl_reader")
	for _, x := range []fixture.Executor{c, provider} {
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(provider.SQL, []string{"provider"}, []string{"TEXT"}, nil))
	}
	for _, q := range []string{pgSeclabelSelect, "SELECT * FROM pg_catalog.pg_seclabel", "SELECT label FROM pg_catalog.pg_seclabel", "SELECT objoid FROM pg_catalog.pg_seclabel"} {
		checkDenied(c, q)
	}
	checkDenied(full, full.SQL)
	// Ordinary PostgreSQL table-source authorization accepts any column
	// SELECT privilege when no column itself is referenced.
	pgSeclabelEmpty(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 1 FROM pg_catalog.pg_seclabel", []string{"?column?"}, []string{"INT4"}, nil))
	pgSeclabelExec(t, p, b, c, "RESET ROLE", "GRANT SELECT(label) ON pg_catalog.pg_seclabel TO sl_acl_reader", "SET ROLE sl_acl_reader")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT provider,label FROM pg_catalog.pg_seclabel", []string{"provider", "label"}, []string{"TEXT", "TEXT"}, nil))
	checkDenied(full, full.SQL)
	pgSeclabelExec(t, p, b, c, "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_seclabel TO sl_acl_reader", "SET ROLE sl_acl_reader")
	fixture.RunStep(t, p.Ctx, full, b.Recorder, pgSeclabelMetadata(b, full.SQL))
	fixture.RunStep(t, p.Ctx, late, b.Recorder, pgSeclabelMetadata(b, late.SQL))
	pgSeclabelExec(t, p, b, c, "RESET ROLE", "REVOKE SELECT ON pg_catalog.pg_seclabel FROM sl_acl_reader", "SET ROLE sl_acl_reader")
	checkDenied(full, full.SQL)
	pgSeclabelExec(t, p, b, c, "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_seclabel TO PUBLIC", "SET ROLE sl_acl_reader")
	fixture.RunStep(t, p.Ctx, full, b.Recorder, pgSeclabelMetadata(b, full.SQL))
	pgSeclabelExec(t, p, b, c, "RESET ROLE")
}

func TestReferencePgSeclabelAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sl_acl_reader"})
	pgSeclabelScenario(t, pgSeclabelAuthorization)
}

func pgSeclabelAborted() fixture.Diagnostic {
	return pgSeclabelDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}

func pgSeclabelReadOnly() fixture.Diagnostic {
	return pgSeclabelDiagnostic("25006", "cannot execute SECURITY LABEL in a read-only transaction")
}

func pgSeclabelTransactions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"SECURITY LABEL ON TABLE public.sl_sentinel IS 'secret'",
		"SECURITY LABEL FOR missing ON INDEX absent.i IS NULL",
	} {
		held := pgSeclabelHeld(t, p, c, q)
		for _, x := range []fixture.Executor{c, held} {
			for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
				pgSeclabelExec(t, p, b, c, begin, "SAVEPOINT sl_recover")
				pgSeclabelEmpty(t, p, b, c)
				want := pgSeclabelAbsent()
				if q == held.SQL && q != "SECURITY LABEL ON TABLE public.sl_sentinel IS 'secret'" { want = pgSeclabelProvider("missing") }
				if begin == "BEGIN READ ONLY" { want = pgSeclabelReadOnly() }
				pgSeclabelError(t, p, b, x, q, want)
				aborted := fixture.ErrorDiagnostic("SELECT count(*) FROM pg_catalog.pg_seclabel", pgSeclabelAborted())
				aborted.SelectRows, aborted.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, aborted)
				pgSeclabelError(t, p, b, x, q, pgSeclabelAborted())
				pgSeclabelExec(t, p, b, c, "ROLLBACK TO SAVEPOINT sl_recover")
				pgSeclabelSentinel(t, p, b, c)
				pgSeclabelExec(t, p, b, c, "COMMIT")
			}
			pgSeclabelExec(t, p, b, c, "BEGIN", "SAVEPOINT sl_recover")
			bad := fixture.ErrorDiagnostic("SELECT 1/0", pgSeclabelDiagnostic("22012", "division by zero"))
			bad.SelectRows, bad.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, bad)
			pgSeclabelError(t, p, b, x, q, pgSeclabelAborted())
			pgSeclabelExec(t, p, b, c, "ROLLBACK TO SAVEPOINT sl_recover")
			want := pgSeclabelAbsent()
			if q != "SECURITY LABEL ON TABLE public.sl_sentinel IS 'secret'" { want = pgSeclabelProvider("missing") }
			pgSeclabelError(t, p, b, x, q, want)
			pgSeclabelExec(t, p, b, c, "ROLLBACK")
			pgSeclabelSentinel(t, p, b, c)
		}
	}
	if !b.PG {
		// pq pre-canceled Prepare may invalidate its connection: this is the
		// native public admission contract, not a different SQL expectation.
		q := "SECURITY LABEL ON TABLE public.sl_sentinel IS NULL"
		held := pgSeclabelHeld(t, p, c, q)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		stmt, err := c.PrepareContext(ctx, q)
		if stmt != nil { stmt.Close() }
		if stmt != nil || !errors.Is(err, context.Canceled) { t.Fatalf("canceled Prepare handle=%v error=%v", stmt, err) }
		for _, x := range []fixture.Executor{c, held} {
			result, err := x.ExecContext(ctx, q)
			if result != nil || !errors.Is(err, context.Canceled) { t.Fatalf("canceled Exec result=%v error=%v", result, err) }
		}
		pgSeclabelError(t, p, b, held, q, pgSeclabelAbsent())
	}
}

func TestReferencePgSeclabelTransactions(t *testing.T) {
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgSeclabelSetup(t, p, b, c)
		pgSeclabelTransactions(t, p, b, c)
	})
}

func TestReferencePgSeclabelSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sl_acl_reader"})
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgSeclabelSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, pgSeclabelMetadata(b, pgSeclabelSelect))
		q := "SECURITY LABEL ON TABLE public.sl_sentinel IS 'secret'"
		held := pgSeclabelHeld(t, p, c, q)
		pgSeclabelError(t, p, b, held, q, pgSeclabelAbsent())
		pgSeclabelError(t, p, b, c, `SECURITY LABEL FOR "MiSsInG" ON TABLE absent.missing IS NULL`, pgSeclabelProvider("MiSsInG"))
		pgSeclabelExec(t, p, b, c, "BEGIN READ ONLY")
		pgSeclabelError(t, p, b, held, q, pgSeclabelReadOnly())
		pgSeclabelError(t, p, b, held, q, pgSeclabelAborted())
		pgSeclabelExec(t, p, b, c, "ROLLBACK")
		pgSeclabelSentinel(t, p, b, c)
		pgSeclabelAuthorization(t, p, b, c)
	})
}
