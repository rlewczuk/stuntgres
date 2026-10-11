package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneReviewQualifiedLiteralNamespace(t *testing.T) {
	if *fixture.Mode {
		fixture.RoleReferenceCleanup(t, []string{"tz_literal_schema_reader"})
	}
	p := fixture.TimezoneReferencePair(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE tz_literal_anchor(id INT)", 0), fixture.Exec("INSERT INTO tz_literal_anchor VALUES(1)", 1), fixture.Exec("SET TIME ZONE 'UTC'", 0), fixture.Exec("SET search_path TO missing,public", 0)} {
		p.Step(t, s)
	}
	for _, q := range []string{
		"SELECT pg_catalog.timestamptz '2004-07-01 12:00+00'::TEXT AS z,pg_catalog.timetz '05:34:17-05'::TEXT AS q FROM tz_literal_anchor",
		"SELECT pg_catalog.\"timestamptz\" '2004-07-01 12:00+00'::TEXT AS z,pg_catalog.\"timetz\" '05:34:17-05'::TEXT AS q FROM tz_literal_anchor",
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(q, []string{"z", "q"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-07-01 12:00:00+00", "05:34:17-05"}})
			if prepared {
				s = fixture.Prepared(s)
			}
			p.Step(t, s)
		}
	}
	// GRANT/REVOKE is an actual PostgreSQL-only authority witness.
	if p.Postgres == nil {
		return
	}
	defer func() {
		if _, err := p.Postgres.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
			t.Error(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA pg_catalog TO PUBLIC"); err != nil {
			t.Error(err)
		}
	}()
	for _, q := range []string{"CREATE ROLE tz_literal_schema_reader", "GRANT USAGE ON SCHEMA public TO tz_literal_schema_reader", "GRANT SELECT ON tz_literal_anchor TO tz_literal_schema_reader", "REVOKE USAGE ON SCHEMA pg_catalog FROM PUBLIC", "SET ROLE tz_literal_schema_reader"} {
		if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, name := range []string{"timestamptz", "\"timestamptz\"", "timetz", "\"timetz\""} {
		input := "2004-07-01 12:00+00"
		if name == "timetz" || name == "\"timetz\"" {
			input = "05:34:17-05"
		}
		q := "SELECT pg_catalog." + name + " '" + input + "' FROM tz_literal_anchor"
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pg_catalog", Position: "8"})
			s.SelectRows = true
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
	}
}
