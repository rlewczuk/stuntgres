//go:build reference

package reference_test

import "testing"

func TestReferenceTimezoneReviewQualifiedLiteralNamespace(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE tz_literal_anchor(id INT)", 0), referenceExec("INSERT INTO tz_literal_anchor VALUES(1)", 1), referenceExec("SET TIME ZONE 'UTC'", 0), referenceExec("SET search_path TO missing,public", 0)} {
		p.step(t, s)
	}
	for _, q := range []string{
		"SELECT pg_catalog.timestamptz '2004-07-01 12:00+00'::TEXT AS z,pg_catalog.timetz '05:34:17-05'::TEXT AS q FROM tz_literal_anchor",
		"SELECT pg_catalog.\"timestamptz\" '2004-07-01 12:00+00'::TEXT AS z,pg_catalog.\"timetz\" '05:34:17-05'::TEXT AS q FROM tz_literal_anchor",
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceSelect(q, []string{"z", "q"}, []string{"TEXT", "TEXT"}, [][]any{{"2004-07-01 12:00:00+00", "05:34:17-05"}})
			if prepared {
				s = referencePrepared(s)
			}
			p.step(t, s)
		}
	}
	// Native SQL GRANT remains outside the dialect. The engine unit counterpart
	// uses the existing native ACL authority; PostgreSQL independently grounds it.
	defer func() {
		if _, err := p.postgres.ExecContext(p.ctx, "RESET ROLE"); err != nil {
			t.Error(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA pg_catalog TO PUBLIC"); err != nil {
			t.Error(err)
		}
	}()
	for _, q := range []string{"CREATE ROLE tz_literal_schema_reader", "GRANT USAGE ON SCHEMA public TO tz_literal_schema_reader", "GRANT SELECT ON tz_literal_anchor TO tz_literal_schema_reader", "REVOKE USAGE ON SCHEMA pg_catalog FROM PUBLIC", "SET ROLE tz_literal_schema_reader"} {
		if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
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
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for schema pg_catalog", Position: "8"})
			s.selectRows = true
			s.prepared = prepared
			if prepared {
				s.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		}
	}
}
