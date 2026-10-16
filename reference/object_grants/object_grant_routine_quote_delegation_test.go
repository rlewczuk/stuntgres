package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineQuoteDelegation(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.quote_literal(text)", "pg_catalog.quote_nullable(text)"})
		literal := fixture.Select("SELECT quote_literal(1::INTEGER) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"'1'"}})
		nullable := fixture.Select("SELECT quote_nullable(1::INTEGER) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"'1'"}})
		nullLiteral := fixture.Select("SELECT quote_literal(NULL::INTEGER) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}})
		nullNullable := fixture.Select("SELECT quote_nullable(NULL::INTEGER) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"NULL"}})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(literal)
			run(nullable)
			run(nullLiteral)
			run(nullNullable)
		})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.quote_literal(TEXT) FROM PUBLIC", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.quote_nullable(TEXT) FROM PUBLIC", 0))
		bodyDenied := func(query, name string) {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name, Context: `SQL function "` + name + `" during inlining`})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			bodyDenied(literal.Query, "quote_literal")
			bodyDenied(nullable.Query, "quote_nullable")
			// The strict literal-NULL wrapper does not invoke its TEXT body.
			run(nullLiteral)
			// The nonstrict wrapper actually delegates its NULL to the primitive.
			bodyDenied(nullNullable.Query, "quote_nullable")
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.quote_literal(TEXT),pg_catalog.quote_nullable(TEXT) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(literal)
			run(nullable)
			run(nullLiteral)
			run(nullNullable)
		})
	})
}
