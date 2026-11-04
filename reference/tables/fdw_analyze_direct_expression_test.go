package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeDirectExpression(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeDirectSetup(t, p, b, r, run)
				run(fixture.Exec("DROP FOREIGN TABLE fdw_direct CASCADE", 0))
				// Genuine SQL effects distinguish domain import from expression
				// evaluation. The deliberately instrumented immutable wrapper is not
				// a portable SQL side-effect promise. Native owns one evaluation.
				// REL_18_6 extended_stats.c first evaluates in make_build_data, then
				// again in compute_expr_stats for expression summaries; both consume
				// the same imported sample. PostgreSQL therefore advances twice.
				expressionCalls := int64(1000)
				if b.PG {
					expressionCalls = 2000
				}
				run(fixture.Exec("CREATE SEQUENCE fdw_direct_imports", 0))
				run(fixture.Exec("CREATE SEQUENCE fdw_direct_evaluations", 0))
				// ANALYZE evaluates under a restricted search_path. The SQL string
				// body is parsed there, so its real sequence needs an explicit schema.
				run(fixture.Exec("CREATE DOMAIN fdw_direct_nonnegative AS integer CONSTRAINT fdw_direct_nonnegative_check CHECK(VALUE>=0 AND nextval('public.fdw_direct_imports')>0)", 0))
				// A distinct ELSE keeps the sequence semantically demanded even
				// for NULL input; do not hide it under annihilating arithmetic.
				run(fixture.Exec("CREATE FUNCTION fdw_direct_value(integer) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT CASE WHEN nextval(''public.fdw_direct_evaluations'')>0 THEN $1+1 ELSE -1 END'", 0))
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_direct(a fdw_direct_nonnegative,b integer,tag text) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'direct')", 0))
				run(fixture.Exec("CREATE STATISTICS fdw_direct_expression ON (fdw_direct_value(a::integer)) FROM fdw_direct", 0))
				statement, err := c.PrepareContext(p.Ctx, "ANALYZE fdw_direct(tag)")
				if err != nil {
					t.Fatal(err)
				}
				statement.Close()
				fdwAnalyzeDirectSequence(run, "prepare", "fdw_direct_imports", 1, false)
				fdwAnalyzeDirectSequence(run, "prepare", "fdw_direct_evaluations", 1, false)
				// a is neither a selected scalar nor a simple extended key. It must
				// nevertheless be imported under its actual domain for the expression.
				fdwAnalyzeDirectCommand(run, prepared, "(tag)")
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
				fdwAnalyzeDirectExpression(run, 3, "")
				fdwAnalyzeDirectSequence(run, "non-null", "fdw_direct_imports", 1000, true)
				fdwAnalyzeDirectSequence(run, "non-null", "fdw_direct_evaluations", expressionCalls, true)
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=9,tag='rare'", 1000)
				run(fixture.Exec("ALTER SEQUENCE fdw_direct_imports RESTART WITH 1", 0))
				run(fixture.Exec("ALTER SEQUENCE fdw_direct_evaluations RESTART WITH 1", 0))
				fdwAnalyzeDirectCommand(run, prepared, "(tag)")
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				fdwAnalyzeDirectSequence(run, "constant", "fdw_direct_imports", 1000, true)
				fdwAnalyzeDirectSequence(run, "constant", "fdw_direct_evaluations", expressionCalls, true)
				fdwAnalyzeDirectEstimate(t, p, c, 1000)
				before := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression")
				// The independent source has base integer, so this genuine bad row
				// reaches the consumer's domain input rather than failing source DML.
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=-1", 1000)
				run(fixture.Exec("SET search_path=pg_catalog", 0))
				want := fdwDiagnostic("23514", `value for domain public.fdw_direct_nonnegative violates check constraint "fdw_direct_nonnegative_check"`)
				want.Schema, want.DataType, want.Constraint = "public", "fdw_direct_nonnegative", "fdw_direct_nonnegative_check"
				if b.PG {
					want.Context = `column "a" of foreign table "fdw_direct"`
				}
				failure := fixture.ErrorDiagnostic("ANALYZE public.fdw_direct(tag)", want)
				failure.Prepared, failure.ErrorStage = prepared, "execute"
				run(failure)
				run(fixture.Exec("SET search_path=public", 0))
				fdwAnalyzeRelationPopulation(run, "fdw_direct", 1000)
				fdwAnalyzeDirectExpression(run, 1, "{10}")
				if got := fdwAnalyzeDirectPayload(t, p, c, "fdw_direct_expression"); got != before {
					t.Fatal("failed direct domain import replaced expression payload", got, before)
				}
				// Real NULLs pass a nullable domain CHECK and remain NULL through
				// its non-strict SQL expression, replacing rather than retaining10.
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.direct SET a=NULL", 1000)
				run(fixture.Exec("ALTER SEQUENCE fdw_direct_imports RESTART WITH 1", 0))
				run(fixture.Exec("ALTER SEQUENCE fdw_direct_evaluations RESTART WITH 1", 0))
				fdwAnalyzeDirectCommand(run, prepared, "(tag)")
				run(fixture.Select("SELECT stxdinherit,(stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values"+fdwAnalyzeDirectExpressionData, []string{"stxdinherit", "stanullfrac", "stadistinct", "stakind1", "values"}, []string{"BOOL", "FLOAT4", "FLOAT4", "INT2", "TEXT"}, [][]any{{false, float64(1), float64(0), int64(0), nil}}))
				fdwAnalyzeDirectSequence(run, "null", "fdw_direct_imports", 1000, true)
				fdwAnalyzeDirectSequence(run, "null", "fdw_direct_evaluations", expressionCalls, true)
				query := fixture.Select("SELECT count(*) FROM fdw_direct WHERE a IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(1000)}})
				query.Prepared = prepared
				run(query)
			})
		})
	}
}

func fdwAnalyzeDirectSequence(run func(fixture.Step), phase, name string, value int64, called bool) {
	run(fixture.Select("SELECT '"+phase+"'::text AS phase,'"+name+"'::text AS sequence_name,last_value,is_called FROM "+name, []string{"phase", "sequence_name", "last_value", "is_called"}, []string{"TEXT", "TEXT", "INT8", "BOOL"}, [][]any{{phase, name, value, called}}))
}
