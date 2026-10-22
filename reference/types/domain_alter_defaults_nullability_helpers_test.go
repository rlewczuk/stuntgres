package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// domainAlterValueScenario shares the existing dual-mode fixture lifecycle.
func domainAlterValueScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			t.Cleanup(func() {
				if _, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS domain_s CASCADE"); err != nil {
					t.Error(err)
				}
				if _, err := b.DB.ExecContext(p.Ctx, "DROP TABLE IF EXISTS public.one"); err != nil {
					t.Error(err)
				}
			})
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA domain_s", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public.one(v integer)", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.one VALUES(1)", 1))
			body(t, p, b, c, r)
		})
	}
}

func domainAlterNullFailure(q, table, column string) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("23502", fmt.Sprintf("column %q of table %q contains null values", column, table))
	d.Schema, d.Table, d.Column = "domain_s", table, column
	s := fixture.ErrorDiagnostic(q, d)
	s.ErrorStage = "execute"
	return s
}

func domainAlterOwnNull(domain string, own bool, constraints [][]any) []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT typnotnull FROM pg_type WHERE oid='domain_s."+domain+"'::regtype::oid", []string{"typnotnull"}, []string{"BOOL"}, [][]any{{own}}),
		fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='domain_s."+domain+"'::regtype::oid AND contype='n' ORDER BY conname", []string{"conname"}, []string{"NAME"}, constraints),
	}
}

func domainAlterHold(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("quiet Prepare", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}
