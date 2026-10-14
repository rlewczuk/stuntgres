package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegOperatorExternalBoundary(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "Reg Op", "regop_later")
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := `CREATE OPERATOR "Reg Op".#=# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4eq)`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SCHEMA "Reg Op"`, 0))
		if !b.PG {
			// Native custom operator DDL is a failure boundary, never fake success.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE regop_boundary_sentinel(v integer)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO regop_boundary_sentinel VALUES (73)", 1))
			result, e := c.ExecContext(p.Ctx, ddl)
			if result != nil || e == nil {
				t.Fatal("unsupported operator DDL succeeded", result, e)
			}
			diagnostic := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "OPERATOR"`)
			diagnostic.Position = "8"
			fixture.AssertDiagnostic(t, e, &diagnostic)
			var sentinel int
			if e := c.QueryRowContext(p.Ctx, "SELECT v FROM regop_boundary_sentinel").Scan(&sentinel); e != nil || sentinel != 73 {
				t.Fatal("unsupported DDL changed sentinel", sentinel, e)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE regop_boundary_sentinel", 0))
			return
		}
		for _, q := range []string{ddl, `CREATE SCHEMA regop_later`, `CREATE OPERATOR regop_later.#=# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4eq)`, `CREATE OPERATOR "Reg Op".##- (RIGHTARG=integer,FUNCTION=pg_catalog.int4um)`} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		var first, later, prefix int64
		for _, tc := range []struct {
			input string
			dest  *int64
		}{{`"Reg Op".#=#(integer,integer)`, &first}, {`regop_later.#=#(integer,integer)`, &later}, {`"Reg Op".##-(NONE,integer)`, &prefix}} {
			if e := c.QueryRowContext(p.Ctx, "SELECT $1::regoperator::oid", tc.input).Scan(tc.dest); e != nil {
				t.Fatal(e)
			}
		}
		if first == later || first == prefix || first == 0 {
			t.Fatal("custom operator identities", first, later, prefix)
		}
		for _, tc := range []struct {
			alias, text string
			oid         int64
		}{{"regoper", `"Reg Op".#=#`, first}, {"regoperator", `"Reg Op".#=#(integer,integer)`, first}, {"regoperator", `"Reg Op".##-(NONE,integer)`, prefix}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows("SELECT $1::oid::"+tc.alias+" AS v,$1::oid AS o", tc.alias, tc.text, tc.oid, b.PG, fmt.Sprint(tc.oid)))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET search_path TO "Reg Op",regop_later,public`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows("SELECT '#=#'::regoper AS v,'#=#'::regoper::oid AS o", "regoper", "#=#", first, b.PG))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows("SELECT $1::oid::regoperator AS v,$1::oid AS o", "regoperator", "regop_later.#=#(integer,integer)", later, b.PG, fmt.Sprint(later)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE OPERATOR regop_later.#=# (LEFTARG=bigint,RIGHTARG=bigint,FUNCTION=pg_catalog.int8eq)`, 0))
		regoperatorDiagnosticRoutes(t, p, b, c, "regoper", "#=#", "42725", "more than one operator named #=#", "")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorRows("SELECT '#=#(integer,integer)'::regoperator AS v,'#=#(integer,integer)'::regoperator::oid AS o", "regoperator", "#=#(integer,integer)", first, b.PG))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW regop_later.identity_view AS SELECT '"Reg Op".#=#(integer,integer)'::regoperator AS v`, 0))
		drop := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop operator #=#(integer,integer) because other objects depend on it")
		drop.Detail = "view identity_view depends on operator #=#(integer,integer)"
		drop.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		failure := fixture.ErrorDiagnostic(`DROP OPERATOR "Reg Op".#=# (integer,integer)`, drop)
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
	})
}
