package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// REL_18_6 utility.c routes supported object kinds through ddl_command_start
// before ExecSecLabelStmt. Shared role/database/tablespace and event-trigger
// targets bypass that route; subscription does not. Failure never fires end.
func TestReferencePgSeclabelEvents(t *testing.T) {
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = recorder
		// Event triggers are database-global and must be removed before the
		// ordinary fixture schema reset, even if a witness fails partway.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.ExecContext(ctx, "ROLLBACK")
			for _, q := range []string{"DROP EVENT TRIGGER IF EXISTS sl_event_start", "DROP EVENT TRIGGER IF EXISTS sl_event_end"} {
				if _, err := b.DB.ExecContext(ctx, q); err != nil { t.Error("event cleanup", q, err) }
			}
		})
		pgSeclabelSetup(t, p, b, c)
		pgSeclabelExec(t, p, b, c,
			"CREATE TABLE public.sl_event_audit(v INT)",
			"CREATE FUNCTION public.sl_event_start_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.sl_event_audit VALUES(1); RAISE NOTICE '%:%',TG_EVENT,TG_TAG; END$$",
			"CREATE FUNCTION public.sl_event_end_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'unexpected end'; RAISE EXCEPTION 'security label end ran'; END$$",
			"CREATE EVENT TRIGGER sl_event_start ON ddl_command_start WHEN TAG IN ('SECURITY LABEL') EXECUTE FUNCTION public.sl_event_start_fn()",
			"CREATE EVENT TRIGGER sl_event_end ON ddl_command_end WHEN TAG IN ('SECURITY LABEL') EXECUTE FUNCTION public.sl_event_end_fn()",
		)
		start := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "ddl_command_start:SECURITY LABEL", Context: "PL/pgSQL function sl_event_start_fn() line 1 at RAISE"}
		unchanged := func() {
			t.Helper()
			pgSeclabelSentinel(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM public.sl_event_audit", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		for _, target := range []string{"TABLE public.sl_sentinel", "TABLE absent.missing", "COLUMN public.sl_sentinel.c", "INDEX absent.i", "SUBSCRIPTION absent"} {
			for _, prepared := range []bool{false, true} {
				q := "SECURITY LABEL ON " + target + " IS 'secret'"
				notices = nil
				x := fixture.Executor(c)
				if prepared { x = pgSeclabelHeld(t, p, c, q) }
				fixture.RegtypeAssertNotices(t, notices, nil)
				pgSeclabelError(t, p, b, x, q, pgSeclabelAbsent())
				// Notice observation and the end callback's veto independently
				// prove start ran before provider failure and end did not run.
				fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{start})
				unchanged()
			}
		}
		q := "SECURITY LABEL FOR missing ON TABLE public.sl_sentinel IS NULL"
		held := pgSeclabelHeld(t, p, c, q)
		notices = nil
		pgSeclabelError(t, p, b, held, q, pgSeclabelProvider("missing"))
		fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{start})
		unchanged()

		pgSeclabelExec(t, p, b, c, "CREATE OR REPLACE FUNCTION public.sl_event_start_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'security label start veto'; END$$")
		veto := pgSeclabelDiagnostic("P0001", "security label start veto")
		veto.Context = "PL/pgSQL function sl_event_start_fn() line 1 at RAISE"
		for _, x := range []fixture.Executor{c, held} {
			notices = nil
			pgSeclabelError(t, p, b, x, q, veto)
			fixture.RegtypeAssertNotices(t, notices, nil)
			unchanged()
		}
		for _, target := range []string{"DATABASE absent", "ROLE absent", "TABLESPACE absent", "EVENT TRIGGER sl_event_start"} {
			for _, prepared := range []bool{false, true} {
				query := "SECURITY LABEL FOR missing ON " + target + " IS NULL"
				notices = nil
				x := fixture.Executor(c)
				if prepared { x = pgSeclabelHeld(t, p, c, query) }
				pgSeclabelError(t, p, b, x, query, pgSeclabelProvider("missing"))
				fixture.RegtypeAssertNotices(t, notices, nil)
			}
		}
		for _, x := range []fixture.Executor{c, held} {
			pgSeclabelExec(t, p, b, c, "BEGIN READ ONLY")
			notices = nil
			pgSeclabelError(t, p, b, x, q, pgSeclabelReadOnly())
			pgSeclabelError(t, p, b, x, q, pgSeclabelAborted())
			fixture.RegtypeAssertNotices(t, notices, nil)
			pgSeclabelExec(t, p, b, c, "ROLLBACK")
			unchanged()
		}
		pgSeclabelExec(t, p, b, c, "ALTER EVENT TRIGGER sl_event_start DISABLE")
		notices = nil
		pgSeclabelError(t, p, b, held, q, pgSeclabelProvider("missing"))
		fixture.RegtypeAssertNotices(t, notices, nil)
		unchanged()
	})
}
