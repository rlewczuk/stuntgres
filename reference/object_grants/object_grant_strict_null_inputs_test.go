package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantStrictNullImports(t *testing.T) {
	objectGrantScenario(t, []string{"og_strict_types"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.currval(regclass)", "pg_catalog.lastval()"})
		strictNullSetup(run)
		strictNullSequence(run, "og_strict_import")
		run(fixture.Exec("CREATE SCHEMA og_strict_types", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_strict_types TO og_object_reader", 0))
		run(fixture.Exec("CREATE DOMAIN og_strict_types.positive AS integer CONSTRAINT strict_positive CHECK(VALUE>0)", 0))
		run(fixture.Exec("CREATE DOMAIN og_strict_types.present AS integer CONSTRAINT strict_present CHECK(VALUE IS NOT NULL)", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			before := strictNullState(t, p, b, c, run, "og_strict_import", 1, false)
			q := "SELECT array_sample($1::integer[],nextval('og_strict_import')::integer) AS v"
			run(strictNullValue(q, nil, nil))
			strictNullUnchanged(run, "og_strict_import", before)
			strictNullSession(run, "og_strict_import", false, 41)
			bad := "SELECT array_sample(NULL::integer[],$1::integer) AS v"
			s := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			run(strictNullValue(bad, nil, int64(1)))
			literal := "SELECT array_sample(NULL::integer[],'bad'::integer) AS v"
			s = fixture.ImplicitError(literal, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(strings.Index(literal, "'bad'") + 1)})
			s.SelectRows, s.ErrorStage = true, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
			// The parameter itself has domain type; input validation must happen
			// before execution planning can discard the parent and its siblings.
			domain := "SELECT array_sample(NULL::integer[],$1::og_strict_types.positive) AS v"
			s = fixture.ImplicitError(domain, fixture.Diagnostic{
				Code: "23514", Message: `value for domain og_strict_types.positive violates check constraint "strict_positive"`,
				Context: "unnamed portal parameter $1 = '...'", Schema: "og_strict_types", DataType: "positive", Constraint: "strict_positive",
			}, "-1")
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			run(strictNullValue(domain, nil, "1"))
			// This is a removable runtime coercion, not a domain-typed import.
			run(strictNullValue("SELECT array_sample(NULL::integer[],(-1)::og_strict_types.positive) AS v", nil))
			strictNullUnchanged(run, "og_strict_import", before)
			strictNullSession(run, "og_strict_import", false, 41)
			// A domain conversion supplying NULL cannot erase its own CHECK.
			barrier := "SELECT array_sample(ARRAY[7],($1::integer)::og_strict_types.present) AS v"
			s = fixture.ImplicitError(barrier, fixture.Diagnostic{
				Code: "23514", Message: `value for domain og_strict_types.present violates check constraint "strict_present"`,
				Schema: "og_strict_types", DataType: "present", Constraint: "strict_present",
			}, nil)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			strictNullUnchanged(run, "og_strict_import", before)
			strictNullSession(run, "og_strict_import", false, 41)
		})
		// A fresh nonNULL binding cannot inherit an earlier NULL command's
		// erased invocation. Its independent sequence keeps count within bounds.
		strictNullSequence(run, "og_strict_import_live")
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(strictNullValue("SELECT array_sample($1::integer[],nextval('og_strict_import_live')::integer) AS v", "{7}", "{7}"))
			strictNullState(t, p, b, c, run, "og_strict_import_live", 1, true)
			strictNullSession(run, "og_strict_import_live", true, 1)
		})
	})
}
