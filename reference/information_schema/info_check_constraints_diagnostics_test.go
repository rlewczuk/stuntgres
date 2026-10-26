package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintsDiagnostics(t *testing.T) {
	t.Run("projection_positions_and_stages", func(t *testing.T) {
		iccScenario(t, func(c *iccCase) {
			for _, phase := range []string{"empty", "populated", "denied"} {
				if phase == "populated" {
					c.exec("CREATE TABLE icc_diag(v integer CONSTRAINT icc_diag CHECK(v>0))")
				}
				if phase == "denied" {
					c.exec("CREATE ROLE icc_reader", "GRANT SELECT ON icc_diag TO icc_reader", "SET SESSION AUTHORIZATION icc_reader")
				}
				for _, column := range []string{"missing", "table_name", "convalidated"} {
					for _, prefix := range []string{"", "/* λ雪 */ "} {
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError(prefix+"SELECT "+column+" FROM information_schema.check_constraints", fixture.Diagnostic{Code: "42703", Message: `column "` + column + `" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							c.run(s)
						}
					}
				}
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			iccScenario(t, func(c *iccCase) {
				c.exec("CREATE TABLE icc_failure(v integer CONSTRAINT icc_prior CHECK(v<100))")
				c.run(fixture.Exec("INSERT INTO icc_failure VALUES(-1)", 1))
				want := c.row("public", "icc_prior", "(v < 100)")
				check := c.consumer()
				check(want)
				original := c.constraintOID("icc_prior")
				diagnostic := fixture.Diagnostic{Code: "23514", Message: `check constraint "icc_failed" of relation "icc_failure" is violated by some row`, Schema: "public", Table: "icc_failure", Constraint: "icc_failed"}
				s := fixture.ImplicitError("ALTER TABLE icc_failure ADD CONSTRAINT icc_failed CHECK(v>0)", diagnostic)
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(s)
				check(want)
				c.run(fixture.Select("SELECT oid::bigint,conname,convalidated,conenforced FROM pg_constraint WHERE conrelid='icc_failure'::regclass", []string{"oid", "conname", "convalidated", "conenforced"}, []string{"INT8", "NAME", "BOOL", "BOOL"}, [][]any{{original, "icc_prior", true, true}}))
				c.run(fixture.Select("SELECT v FROM icc_failure", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
				c.exec("ALTER TABLE icc_failure ADD CONSTRAINT icc_failed CHECK(v>0) NOT VALID")
				pending := c.row("public", "icc_failed", "(v > 0)")
				check(want, pending)
				pendingOID := c.constraintOID("icc_failed")
				c.flags("icc_failed", false)
				s = fixture.ImplicitError("ALTER TABLE icc_failure VALIDATE CONSTRAINT icc_failed", diagnostic)
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(s)
				check(want, pending)
				c.flags("icc_failed", false)
				if c.constraintOID("icc_failed") != pendingOID || c.constraintOID("icc_prior") != original {
					c.t.Fatal("failed validation replaced actual definition identities")
				}
				c.run(fixture.Select("SELECT v FROM icc_failure", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
			})
		})
	}
}
