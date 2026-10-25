package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionDropTransactions(t *testing.T) {
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, peer *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
		defer owner.ExecContext(context.Background(), "ROLLBACK")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(fixture.Exec("CREATE VIEW ext_drop_tx_view AS SELECT unaccent('Hôtel') AS v", 0))
		id := extensionTempCapture(t, p, owner)
		q := "SELECT unaccent($1::text)"
		stmt, err := owner.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}
		call := func() {
			for _, value := range []any{"Hôtel", nil} {
				var want any = "Hotel"
				if value == nil {
					want = nil
				}
				s := fixture.Select(q, []string{"unaccent"}, []string{"TEXT"}, [][]any{{want}}, value)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, recorder, s)
			}
		}
		call()
		drop, err := owner.PrepareContext(p.Ctx, "DROP EXTENSION unaccent CASCADE")
		if err != nil {
			t.Fatal(err)
		}
		extensionTempPresent(t, p, owner, recorder, id)
		drop.Close()
		for _, savepoint := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			if savepoint {
				run(fixture.Exec("SAVEPOINT before_drop", 0))
			}
			run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
			extensionTempGone(t, p, owner, recorder, id)
			// Catalog-only peer checks avoid waiting for the real DROP's relation locks.
			extensionTempPresent(t, p, peer, r, id)
			missing := extensionMissing("SELECT unaccent(NULL::text)", "unaccent", "text")
			missing.SelectRows = true
			run(missing)
			if savepoint {
				run(fixture.Exec("ROLLBACK TO before_drop", 0))
				extensionTempPresent(t, p, owner, recorder, id)
				call()
				run(fixture.Exec("COMMIT", 0))
			} else {
				run(fixture.Exec("ROLLBACK", 0))
			}
			extensionTempPresent(t, p, peer, r, id)
			call()
			fixture.RunStep(t, p.Ctx, peer, r, fixture.Select("SELECT v FROM ext_drop_tx_view", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		}
		notices = nil
		run(fixture.Exec("BEGIN", 0))
		d := extensionDropRestrict("view ext_drop_tx_view depends on function unaccent(text)")
		run(fixture.ImplicitError("DROP EXTENSION unaccent RESTRICT", d))
		run(extensionError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		run(fixture.Exec("ROLLBACK", 0))
		extensionTempPresent(t, p, owner, recorder, id)
		call()
		// Native precancellation is a documented context boundary. Do not invent
		// PostgreSQL cancellation/admission observations from a fast client call.
		if !pg {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := owner.ExecContext(ctx, "DROP EXTENSION unaccent CASCADE")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled DROP result=%v error=%v", result, err)
			}
		}
		extensionTempPresent(t, p, owner, recorder, id)
		call()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
		run(fixture.Exec("COMMIT", 0))
		extensionTempGone(t, p, peer, r, id)
		extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to view ext_drop_tx_view"}})
		missing := extensionMissing("SELECT unaccent(NULL::text)", "unaccent", "text")
		missing.SelectRows = true
		fixture.RunStep(t, p.Ctx, peer, r, missing)
	})
}
