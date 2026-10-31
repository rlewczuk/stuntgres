package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSecurityLabelAtomicityControls(t *testing.T) {
	securityLabelScenarios(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		securityLabelSeed(t, p, b, c)
		before := ""
		if !b.PG { before = fixture.CommentInquiryDump(t, p) }
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		for _, label := range []string{"'classified'", "NULL"} {
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON TABLE sl_t IS "+label, securityLabelNoProvider)
		}
		if !b.PG && fixture.CommentInquiryDump(t, p) != before { t.Fatal("failed label changed canonical dump") }
		run("BEGIN")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run("SAVEPOINT sl_save")
		securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON TABLE sl_t IS NULL", securityLabelNoProvider)
		aborted := fixture.ImplicitError("SELECT 1", fixture.TransactionAborted())
		aborted.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, aborted)
		run("ROLLBACK TO SAVEPOINT sl_save")
		securityLabelUnchanged(t, p, b, c, "sl_t")
		run("COMMIT")
		run("BEGIN READ ONLY")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RoleReferenceError("SECURITY LABEL FOR missing ON TABLE sl_t IS NULL", "25006", "cannot execute SECURITY LABEL in a read-only transaction", "", prepared, "execute"))
		run("ROLLBACK")
		securityLabelUnchanged(t, p, b, c, "sl_t")
		if !b.PG {
			if fixture.CommentInquiryDump(t, p) != before { t.Fatal("transaction failures changed canonical dump") }
			// A held protocol statement never caches an address or provider authority.
			held, err := c.PrepareContext(p.Ctx, "SECURITY LABEL FOR 'MissingProvider' ON TABLE sl_t IS NULL")
			if err != nil { t.Fatal(err) }
			defer held.Close()
			want := fixture.ControlsReferenceDiagnostic("22023", `security label provider "MissingProvider" is not loaded`)
			checkHeld := func() {
				t.Helper()
				_, err := held.ExecContext(p.Ctx)
				fixture.AssertDiagnostic(t, err, &want)
			}
			checkHeld()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='sl_t'", []string{"relname"}, []string{"NAME"}, nil))
			checkHeld()
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON TABLE sl_t IS NULL", securityLabelNoProvider)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before)); err != nil { t.Fatal(err) }
			checkHeld()
			securityLabelError(t, p, b, c, prepared, "SECURITY LABEL ON TABLE sl_t IS NULL", securityLabelNoProvider)
			securityLabelUnchanged(t, p, b, c, "sl_t")
			if fixture.CommentInquiryDump(t, p) != before { t.Fatal("noncanonical native round trip") }
		}
		// Pre-canceled admission is a native adapter contract, not a fast pq command guarantee.
		if !b.PG {
			var cancelledStatement *sql.Stmt
			if prepared {
				var err error
				cancelledStatement, err = c.PrepareContext(p.Ctx, "SECURITY LABEL ON TABLE sl_t IS NULL")
				if err != nil { t.Fatal(err) }
				defer cancelledStatement.Close()
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			var err error
			if prepared {
				_, err = cancelledStatement.ExecContext(ctx)
			} else {
				_, err = c.ExecContext(ctx, "SECURITY LABEL ON TABLE sl_t IS NULL")
			}
			if !errors.Is(err, context.Canceled) { t.Fatalf("cancelled admission: %v", err) }
		}
		securityLabelUnchanged(t, p, b, c, "sl_t")
	})
}
