package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSequencesDiagnostics(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE SEQUENCE isq_diag")
		held := c.hold(c.conn, isqQuery)
		for _, column := range []string{"missing", "last_value"} {
			for _, prefix := range []string{"", "/* éλ */ "} {
				q := prefix + "SELECT " + column + " FROM information_schema.sequences"
				want := fixture.ControlsReferenceDiagnostic("42703", `column "`+column+`" does not exist`)
				want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, column)]) + 1)
				if column == "last_value" {
					want.Hint = `Perhaps you meant to reference the column "sequences.start_value".`
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, want)
					s.SelectRows, s.ErrorStage = true, "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
					c.all(held, "isq_diag", c.def("public", "isq_diag"))
				}
			}
		}
	})
}

func TestReferenceInfoSequencesFeatureSmoke(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE ROLE isq_actor", "CREATE SCHEMA isq_private", "REVOKE ALL ON SCHEMA isq_private FROM PUBLIC", "CREATE SEQUENCE isq_private.isq_smoke AS INTEGER START 15 MINVALUE -20 MAXVALUE 200 INCREMENT 7 CYCLE", "SET ROLE isq_actor")
		held := c.hold(c.conn, isqQuery)
		c.all(held, "isq_smoke")
		c.exec("RESET ROLE", "GRANT USAGE ON SEQUENCE isq_private.isq_smoke TO isq_actor", "SET ROLE isq_actor")
		c.all(held, "isq_smoke", c.custom("isq_private", "isq_smoke"))
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema isq_private")
		denied.Position = "16"
		s := fixture.ErrorDiagnostic("SELECT nextval('isq_private.isq_smoke')", denied)
		s.SelectRows, s.ErrorStage = true, "execute"
		c.run(c.conn, s)
		q := `SELECT a.attname,t.typname,b.typname AS base_name FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='sequences' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_type b ON b.oid=t.typbasetype ORDER BY a.attnum`
		want := make([][]any, 12)
		for i, name := range isqColumns {
			domain, base := "character_data", "varchar"
			if i < 3 {
				domain, base = "sql_identifier", "name"
			}
			if i >= 4 && i <= 6 {
				domain, base = "cardinal_number", "int4"
			}
			if i == 11 {
				domain = "yes_or_no"
			}
			want[i] = []any{name, domain, base}
		}
		c.run(c.conn, fixture.Select(q, []string{"attname", "typname", "base_name"}, []string{"NAME", "NAME", "NAME"}, want))
		c.exec("RESET ROLE", "ALTER SEQUENCE isq_private.isq_smoke START 21 INCREMENT 3 NO CYCLE", "SET ROLE isq_actor")
		c.all(held, "isq_smoke", c.row("isq_private", "isq_smoke", "integer", 32, "21", "-20", "200", "3", "NO"))
		c.exec("RESET ROLE", "REVOKE USAGE ON SEQUENCE isq_private.isq_smoke FROM isq_actor", "SET ROLE isq_actor")
		c.all(held, "isq_smoke")
		c.exec("RESET ROLE", "DROP SEQUENCE isq_private.isq_smoke", "SET ROLE isq_actor")
		c.all(held, "isq_smoke")
	})
}
