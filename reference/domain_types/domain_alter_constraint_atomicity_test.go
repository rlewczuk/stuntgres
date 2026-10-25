package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintAtomicity(t *testing.T) {
	for _, action := range []string{"add", "validate"} {
		t.Run(action, func(t *testing.T) {
			domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer", "CREATE TABLE domain_s.t(v domain_s.d)"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO domain_s.t VALUES(1)", 1))
				q := "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)"
				flags := [][]any{}
				if action == "validate" {
					run(fixture.Exec(q+" NOT VALID", 0))
					q = "ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive"
					flags = [][]any{{"positive", false}}
				}
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
				// The existing public fixture proves a real native manager wait or
				// server admission, not a sleep or a presumed worker start.
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
					if b.PG {
						fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, tableOID, "ShareLock")
					}
					fixture.RunStep(t, ctx, reader, b.Recorder, fixture.Select("SELECT v FROM domain_s.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.RunStep(t, ctx, reader, b.Recorder, domainAlterFlags("d", flags))
				}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, q, false) })
				// Same physical owner remains healthy; the writer is still open.
				run(fixture.Select("SELECT v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(domainAlterFlags("d", flags))
				run(fixture.Exec("SET lock_timeout='100ms'", 0))
				t.Run("lock_timeout", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
				})
				run(fixture.Exec("SET lock_timeout=0", 0))
				run(domainAlterFlags("d", flags))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec(q, 0))
				run(domainAlterFlags("d", [][]any{{"positive", true}}))
				run(fixture.Select("SELECT v FROM domain_s.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				t.Run("enforcement_after_atomic_install", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("INSERT INTO domain_s.t VALUES(-1)", "d", "positive", false, false))
				})
			})
		})
	}
}
