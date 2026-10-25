package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceAggregateLifecycleSignatures(t *testing.T) {
	for _, signature := range []string{"integer", "int", "pg_catalog.int4", "IN ignored integer"} {
		t.Run(signature, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lifecycleSetup(t, p, b, c)
				lifecycleExec(t, p, b, c, "ALTER AGGREGATE al_sum("+signature+") RENAME TO al_renamed")
				lifecycleValues(t, p, b, c, "al_renamed")
				lifecycleExec(t, p, b, c, "DROP AGGREGATE al_renamed("+signature+")")
			})
		})
	}
	t.Run("declared_types", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lifecycleExec(t, p, b, c,
				"CREATE FUNCTION al_tick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
				"CREATE AGGREGATE al_zero(*) (SFUNC=al_tick,STYPE=int4,INITCOND='0')",
				"CREATE DOMAIN al_domain AS integer",
				"CREATE FUNCTION al_step(integer,al_domain,integer[]) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
				"CREATE AGGREGATE al_many(al_domain,integer[]) (SFUNC=al_step,STYPE=int4,INITCOND='0')",
				"ALTER AGGREGATE al_zero(*) RENAME TO al_zero_new",
				"ALTER AGGREGATE al_many(IN d al_domain,IN a integer[]) RENAME TO al_many_new")
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT al_zero_new(*) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(3)))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT al_many_new(x::al_domain,ARRAY[1]) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6)))
			lifecycleExec(t, p, b, c, "DROP AGGREGATE al_zero_new(*),al_many_new(al_domain,integer[])")
		})
	})
	t.Run("ordered_identity", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ sig, display string }{
				{"ORDER BY integer", "integer"}, {"float8 ORDER BY integer", "double precision, integer"}, {"float8,integer", "double precision, integer"}, {`VARIADIC "any" ORDER BY VARIADIC "any"`, `"any"`},
				{"VARIADIC integer[] ORDER BY VARIADIC pg_catalog.int4[]", "integer[]"},
				{"VARIADIC pg_catalog.int4[] ORDER BY VARIADIC integer[]", "integer[]"},
			} {
				lifecycleError(t, p, b, c, "DROP AGGREGATE al_missing("+tc.sig+")", "42883", "aggregate al_missing("+tc.display+") does not exist")
			}
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			lifecycleExec(t, p, b, c, "BEGIN", "ALTER AGGREGATE pg_catalog.percentile_disc(double precision ORDER BY anyelement) RENAME TO al_percentile")
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT 'pg_catalog.al_percentile(double precision,anyelement)'::regprocedure::oid=3972::oid AS identity", []string{"identity"}, []string{"BOOL"}, [][]any{{true}}))
			lifecycleExec(t, p, b, c, "ROLLBACK")
		})
	})
	t.Run("namespace_masking", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "al_mask")
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lifecycleSetup(t, p, b, c)
			lifecycleExec(t, p, b, c, "CREATE SCHEMA al_mask", "CREATE AGGREGATE al_mask.al_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='10')", "SET search_path=al_mask,public")
			id := lifecycleOID(t, p, c, "public.al_sum(integer)")
			lifecycleExec(t, p, b, c, "ALTER AGGREGATE al_sum(integer) RENAME TO al_masked")
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT al_masked(x) AS v FROM al_input", int64(16)))
			lifecycleFacts(t, p, b, c, "public.al_sum", id)
			lifecycleExec(t, p, b, c, "SET search_path=public", "DROP AGGREGATE al_mask.al_masked(integer)")
		})
	})
}

func TestReferenceAggregateLifecycleDiagnostics(t *testing.T) {
	t.Run("semantic", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lifecycleSetup(t, p, b, c)
			lifecycleExec(t, p, b, c, "CREATE FUNCTION al_function(integer DEFAULT 1) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "CREATE PROCEDURE al_procedure(integer) LANGUAGE SQL AS $$SELECT $1$$")
			for _, tc := range []struct{ q, code, message string }{
				{"DROP AGGREGATE al_missing(integer)", "42883", "aggregate al_missing(integer) does not exist"},
				{"ALTER AGGREGATE al_missing(*) RENAME TO bad", "42883", "aggregate al_missing(*) does not exist"},
				{"DROP AGGREGATE IF EXISTS al_function(integer)", "42809", "function al_function(integer) is not an aggregate"},
				{"DROP AGGREGATE IF EXISTS al_procedure(integer)", "42809", "function al_procedure(integer) is not an aggregate"},
				{"DROP AGGREGATE al_function(*)", "42883", "aggregate al_function(*) does not exist"},
				{"ALTER AGGREGATE al_sum(integer) RENAME TO al_sum", "42723", `function al_sum(integer) already exists in schema "public"`},
				{"ALTER AGGREGATE al_sum(integer) SET SCHEMA al_absent", "3F000", `schema "al_absent" does not exist`},
				{"DROP AGGREGATE al_missing(al_no_type)", "42704", `type "al_no_type" does not exist`},
				{"DROP AGGREGATE al_missing(VARIADIC integer[])", "42883", "aggregate al_missing(integer[]) does not exist"},
			} {
				lifecycleError(t, p, b, c, tc.q, tc.code, tc.message)
			}
			for _, verb := range []string{"ALTER", "DROP"} {
				q := verb + " ROUTINE al_sum"
				if verb == "ALTER" {
					q += " RENAME TO bad"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42725", Message: `routine name "al_sum" is not unique`, Hint: "Specify the argument list to select the routine unambiguously."})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					createAggregateRun(t, p, b, c, s)
				}
			}
			lifecycleValues(t, p, b, c, "al_sum")
		})
	})
	t.Run("parse", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ q, token, code, message string }{
				{"DROP AGGREGATE al_missing()", ")", "42601", `syntax error at or near ")"`},
				{"DROP AGGREGATE al_missing", "", "42601", "syntax error at end of input"},
				{"/*é*/ ALTER AGGREGATE al_missing(OUT x int) RENAME TO bad", "OUT", "0A000", "aggregates cannot have output arguments"},
				{"DROP AGGREGATE al_missing(INOUT integer)", "INOUT", "0A000", "aggregates cannot have output arguments"},
				{"DROP AGGREGATE al_missing(IN OUT integer)", "IN OUT", "0A000", "aggregates cannot have output arguments"},
				{"DROP AGGREGATE al_missing(VARIADIC integer[] ORDER BY integer)", "integer)", "0A000", "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type"},
				{"DROP AGGREGATE al_missing(VARIADIC integer[] ORDER BY VARIADIC bigint[])", "VARIADIC bigint[]", "0A000", "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type"},
				{"DROP AGGREGATE al_missing(VARIADIC integer[] ORDER BY VARIADIC int4[])", "VARIADIC int4[]", "0A000", "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type"},
			} {
				at := len(tc.q)
				if tc.token != "" {
					at = strings.Index(tc.q, tc.token)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(utf8.RuneCountInString(tc.q[:at]) + 1)})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					createAggregateRun(t, p, b, c, s)
				}
			}
		})
	})
	t.Run("notices", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				for _, tc := range []struct{ sig, display string }{{"*", ""}, {"integer", "pg_catalog.int4"}} {
					q := "DROP AGGREGATE IF EXISTS al_missing(" + tc.sig + ")"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if len(notices) != 0 {
						t.Fatal("prepare emitted notices", notices)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, rec, fixture.Exec(q, 0))
					stmt.Close()
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("aggregate al_missing(%s) does not exist, skipping", tc.display)}
					if len(notices) != 1 || notices[0] != want {
						t.Fatalf("notices=%#v want=%#v", notices, want)
					}
					notices = nil
				}
			})
		}
	})
}
