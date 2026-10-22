package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterNotNullAtomicity(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer", "CREATE TABLE domain_s.t(v domain_s.d)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(1)", 1))
		writer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		reader, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		waiterPID, holderPID := 0, 0
		var tableOID int64
		if b.PG {
			waiterPID, holderPID = fixture.RowPIDs(t, p.Ctx, c, writer)
			tableOID = fixture.LockOID(t, p.Ctx, "domain_s.t")
		}
		tx, err := writer.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, r, fixture.Exec("INSERT INTO domain_s.t VALUES(2)", 1))
		q := "ALTER DOMAIN domain_s.d SET NOT NULL"
		check := func(ctx context.Context, x fixture.Executor, rec *fixture.Recorder) {
			fixture.RunStep(t, ctx, x, rec, fixture.Select("SELECT v FROM domain_s.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			for _, s := range domainAlterOwnNull("d", false, nil) {
				fixture.RunStep(t, ctx, x, rec, s)
			}
			fixture.RunStep(t, ctx, x, rec, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_constraint c ON c.oid=d.objid WHERE d.classid='pg_constraint'::regclass::oid AND c.contypid='domain_s.d'::regtype::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, tableOID, "ShareLock")
			}
			check(ctx, reader, b.Recorder)
		}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, q, false) })
		// Cancellation joins the worker and leaves the SAME owner usable.
		run(fixture.Select("SELECT v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		check(p.Ctx, c, r)
		run(fixture.Exec("SET lock_timeout='100ms'", 0))
		t.Run("timeout", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
		})
		run(fixture.Exec("SET lock_timeout=0", 0))
		check(p.Ctx, c, r)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(q, 0))
		for _, s := range domainAlterOwnNull("d", true, [][]any{{"d_not_null"}}) {
			run(s)
		}
		run(domainDefaultFailure("INSERT INTO domain_s.t VALUES(NULL)", domainNotNullDiagnostic("domain_s", "d"), false, false))
		run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
