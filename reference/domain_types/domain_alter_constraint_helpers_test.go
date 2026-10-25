package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func domainAlterScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			t.Cleanup(func() {
				_, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS domain_s CASCADE")
				if err != nil {
					t.Error(err)
				}
				_, err = b.DB.ExecContext(p.Ctx, "DROP TABLE IF EXISTS public.one")
				if err != nil {
					t.Error(err)
				}
			})
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA domain_s", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public.one(v integer)", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.one VALUES(1)", 1))
			body(t, p, b, c, r, &notices)
		})
	}
}

// These records are independently specified from REL_18_0 typecmds.c,
// validateDomainCheckConstraint. They are not learned from native output.
func domainAlterOldFailure(q, table, column string) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic("23514", fmt.Sprintf("column %q of table %q contains values that violate the new constraint", column, table))
	want.Schema, want.Table, want.Column = "domain_s", table, column
	s := fixture.ErrorDiagnostic(q, want)
	s.ErrorStage = "execute"
	return s
}

func domainAlterContainerFailure(q, domain, table, column string) fixture.Step {
	want := fixture.ControlsReferenceDiagnostic("0A000", fmt.Sprintf("cannot alter type %q because column %q uses it", "domain_s."+domain, table+"."+column))
	s := fixture.ErrorDiagnostic(q, want)
	s.ErrorStage = "execute"
	return s
}

func domainAlterFlags(domain string, rows [][]any) fixture.Step {
	return fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE contypid='domain_s."+domain+"'::regtype::oid ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, rows)
}

func domainAlterOID(t *testing.T, p *fixture.Pair, x fixture.Executor, q string) int64 {
	t.Helper()
	var id int64
	rows, err := x.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing identity", rows.Err())
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra identity/error", rows.Err())
	}
	if id <= 0 {
		t.Fatal("invalid identity", id)
	}
	return id
}
