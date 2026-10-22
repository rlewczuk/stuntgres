package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintBoundaries(t *testing.T) {
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE DOMAIN domain_s.d AS integer CONSTRAINT keep CHECK(VALUE<10)", 0))
		for _, q := range []string{"ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", "ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT keep", "ALTER DOMAIN domain_s.d DROP CONSTRAINT keep", "ALTER DOMAIN domain_s.d SET DEFAULT 7", "ALTER DOMAIN domain_s.d DROP DEFAULT", "ALTER DOMAIN domain_s.d SET NOT NULL", "ALTER DOMAIN domain_s.d DROP NOT NULL"} {
			t.Run(q, func(t *testing.T) {
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				stmt, err := tx.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("valid utility Prepare must be quiet", err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, tx, r, domainAlterFlags("d", [][]any{{"keep", true}}))
				if b.PG {
					fixture.RunStep(t, p.Ctx, held, r, fixture.Exec(q, 0))
				} else {
					fixture.RunStep(t, p.Ctx, held, r, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")))
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, c, r, domainAlterFlags("d", [][]any{{"keep", true}}))
			})
		}
		t.Run("read_only_precedence", func(t *testing.T) {
			tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			q := "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)"
			s := fixture.Prepared(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER DOMAIN in a read-only transaction")))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, tx, r, s)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
		run(domainAlterFlags("d", [][]any{{"keep", true}}))
		q := "ALTER DOMAIN domain_s.d ADD CONSTRAINT rejected CHECK(VALUE>0), DROP CONSTRAINT keep"
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ","`)
		want.Position = fixture.Position(strings.Index(q, ",") + 1)
		t.Run("one_action_only", func(t *testing.T) {
			s := fixture.Prepared(fixture.ErrorDiagnostic(q, want))
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, r, s)
		})
		run(domainAlterFlags("d", [][]any{{"keep", true}}))
	})
}
