package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintRoutineUsageDiagnostics(t *testing.T) {
	t.Run("column_position_and_stage", func(t *testing.T) {
		ccruScenario(t, func(c *ccruCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(prefix+"SELECT missing FROM information_schema.check_constraint_routine_usage", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(s)
				}
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			ccruScenario(t, func(c *ccruCase) {
				t := c.t
				f, _ := c.seed()
				c.exec("CREATE TABLE ccru_failure(v integer CONSTRAINT ccru_prior CHECK(ccru_g(v)))")
				c.run(fixture.Exec("INSERT INTO ccru_failure VALUES(-1)", 1))
				g := c.identity("public", "ccru_g", "integer")
				want := c.row("public", "ccru_prior", "public", g)
				c.read(want)
				var original int64
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conrelid='ccru_failure'::regclass::oid AND conname='ccru_prior'").Scan(&original); err != nil || original <= 0 {
					t.Fatalf("constraint identity=%d error=%v", original, err)
				}
				s := fixture.ImplicitError("ALTER TABLE ccru_failure ADD CONSTRAINT ccru_failed CHECK(ccru_f(v))", fixture.Diagnostic{Code: "23514", Message: `check constraint "ccru_failed" of relation "ccru_failure" is violated by some row`, Schema: "public", Table: "ccru_failure", Constraint: "ccru_failed"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(s)
				c.read(want)
				c.run(fixture.Select("SELECT oid::bigint,conname,convalidated,conenforced FROM pg_constraint WHERE conrelid='ccru_failure'::regclass::oid", []string{"oid", "conname", "convalidated", "conenforced"}, []string{"INT8", "NAME", "BOOL", "BOOL"}, [][]any{{original, "ccru_prior", true, true}}))
				c.run(fixture.Select("SELECT v FROM ccru_failure", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
				// The failed routine edge was not published. Adding it NOT VALID is real DDL.
				c.exec("ALTER TABLE ccru_failure ADD CONSTRAINT ccru_failed CHECK(ccru_f(v)) NOT VALID")
				c.read(want, c.row("public", "ccru_failed", "public", f))
			})
		})
	}
}
