package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUserOperatorComparisonConsumerBoundary(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_consumer")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA userop_consumer",
			"CREATE OPERATOR userop_consumer.= (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne)",
			"CREATE OPERATOR userop_consumer.<> (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq)",
			"CREATE OPERATOR userop_consumer.+ (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq)",
			"CREATE TYPE public.userop_consumer_pair AS (v integer)",
			"CREATE TABLE public.userop_consumer_left(k integer)",
			"CREATE TABLE public.userop_consumer_right(k integer)",
			"SET search_path TO userop_consumer,pg_catalog,public")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_consumer_left VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_consumer_right VALUES(2)", 1))
		userOperatorValue(t, p, b, c, "SELECT 2 = 2 AS v", "BOOL", false)
		userOperatorValue(t, p, b, c, "SELECT 2 <> 2 AS v", "BOOL", true)
		for _, tc := range []struct {
			name, query, typ, position string
			value                      any
		}{
			{"array_any", "SELECT 2 = ANY(ARRAY[2]) AS v", "BOOL", "10", false},
			{"array_some", "SELECT 2 = SOME(ARRAY[2]) AS v", "BOOL", "10", false},
			{"array_all", "SELECT 2 = ALL(ARRAY[2]) AS v", "BOOL", "10", false},
			{"array_unknown_scalar", "SELECT '2' = ANY(ARRAY[2]) AS v", "BOOL", "12", false},
			{"array_null", "SELECT 2 = ANY(NULL::int4[]) AS v", "BOOL", "10", nil},
			{"array_empty", "SELECT 2 = ANY(ARRAY[]::int4[]) AS v", "BOOL", "10", false},
			{"array_boolean_plus", "SELECT 2 OPERATOR(userop_consumer.+) ANY(ARRAY[2]) AS v", "BOOL", "10", true},
			{"list_pair", "SELECT 2 IN (2) AS v", "BOOL", "10", false},
			{"list_batch", "SELECT 2 IN (2,2) AS v", "BOOL", "10", false},
			{"list_var", "SELECT 2 IN (k,2) AS v FROM public.userop_consumer_left", "BOOL", "10", false},
			{"list_not", "SELECT 2 NOT IN (2) AS v", "BOOL", "10", true},
			{"subquery_any", "SELECT 2 = ANY(SELECT 2) AS v", "BOOL", "10", false},
			{"subquery_all", "SELECT 2 = ALL(SELECT 2) AS v", "BOOL", "10", false},
			{"subquery_in", "SELECT 2 IN (SELECT 2) AS v", "BOOL", "10", false},
			{"subquery_not", "SELECT 2 NOT IN (SELECT 2) AS v", "BOOL", "10", true},
			{"simple_case", "SELECT CASE 2 WHEN 2 THEN 10 ELSE 20 END AS v", "INT4", "15", int64(20)},
			{"nullif", "SELECT NULLIF(2,2) AS v", "INT4", "8", int64(2)},
			{"distinct", "SELECT 2 IS DISTINCT FROM 2 AS v", "BOOL", "10", true},
			{"not_distinct", "SELECT 2 IS NOT DISTINCT FROM 2 AS v", "BOOL", "10", false},
			{"row_scalar", "SELECT ROW(2) = ROW(2) AS v", "BOOL", "15", false},
			{"row_list", "SELECT ROW(2) IN (ROW(2)) AS v", "BOOL", "15", false},
			{"cast_row_list", "SELECT ROW(2)::public.userop_consumer_pair IN (ROW(2)::public.userop_consumer_pair) AS v", "BOOL", "44", false},
			{"row_subquery", "SELECT ROW(2) = ANY(SELECT 2) AS v", "BOOL", "15", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if !b.PG {
					userOperatorError(t, p, b, c, tc.query, "0A000", "custom operators are unsupported in this comparison context", "", "", tc.position, true)
					return
				}
				userOperatorValue(t, p, b, c, tc.query, tc.typ, tc.value)
			})
		}
		for _, query := range []string{
			"SELECT l.k AS v FROM public.userop_consumer_left l JOIN public.userop_consumer_right r USING(k)",
			"SELECT l.k AS v FROM public.userop_consumer_left l NATURAL JOIN public.userop_consumer_right r",
		} {
			t.Run(query, func(t *testing.T) {
				if !b.PG {
					userOperatorError(t, p, b, c, query, "0A000", "custom operators are unsupported in this comparison context", "", "", "", true)
					return
				}
				s := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(pg_catalog.=) ANY(ARRAY[2]) AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(pg_catalog.=) ANY(SELECT 2) AS v", "BOOL", true)
	})
}

func TestReferenceUserOperatorComparisonConsumerResultContracts(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_consumer_result")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA userop_consumer_result",
			"CREATE OPERATOR userop_consumer_result.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)",
			"CREATE OPERATOR userop_consumer_result.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(userop_consumer_result.#!#))")
		for _, tc := range []struct{ query, code, message, hint, position string }{
			{"SELECT 2 OPERATOR(userop_consumer_result.#~#) ANY(ARRAY[2]) AS v", "42809", "op ANY/ALL (array) requires operator to yield boolean", "", "10"},
			{"SELECT 2 OPERATOR(userop_consumer_result.#~#) ALL(ARRAY[2]) AS v", "42809", "op ANY/ALL (array) requires operator to yield boolean", "", "10"},
			{"SELECT 2 OPERATOR(userop_consumer_result.#!#) ANY(ARRAY[2]) AS v", "42883", "operator is only a shell: integer userop_consumer_result.#!# integer", "", "10"},
			{"SELECT 2 OPERATOR(userop_consumer_result.#!#) ANY(3) AS v", "42809", "op ANY/ALL (array) requires array on right side", "", "10"},
			{"SELECT 'a'::text OPERATOR(userop_consumer_result.#~#) ANY(ARRAY['b'::text]) AS v", "42883", "operator does not exist: text userop_consumer_result.#~# text", "No operator matches the given name and argument types. You might need to add explicit type casts.", "18"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				userOperatorError(t, p, b, c, tc.query, tc.code, tc.message, "", tc.hint, tc.position, true)
			})
		}
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_consumer_result.#~#) 3 AS v", "INT4", int64(5))
	})
}

func TestReferenceUserOperatorComparisonConsumerStructuralControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "userop_consumer_structure")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA userop_consumer_structure",
			"SET search_path TO userop_consumer_structure,pg_catalog,public")
		for _, phase := range []string{"seed_only", "unary_only"} {
			t.Run(phase, func(t *testing.T) {
				if phase == "unary_only" {
					userOperatorExec(t, p, b, c, "CREATE OPERATOR userop_consumer_structure.= (RIGHTARG=int4,FUNCTION=pg_catalog.int4um)")
					userOperatorValue(t, p, b, c, "SELECT OPERATOR(userop_consumer_structure.=) 2 AS v", "INT4", int64(-2))
				}
				userOperatorValue(t, p, b, c, "SELECT 2 = 2 AS v", "BOOL", true)
				userOperatorValue(t, p, b, c, "SELECT 2 = ANY(ARRAY[2]) AS v", "BOOL", true)
				for _, query := range []string{
					"SELECT 2 OPERATOR(userop_consumer_structure.=) 2 AS v",
					"SELECT 2 OPERATOR(userop_consumer_structure.=) ANY(ARRAY[2]) AS v",
				} {
					userOperatorError(t, p, b, c, query, "42883",
						"operator does not exist: integer userop_consumer_structure.= integer", "",
						"No operator matches the given name and argument types. You might need to add explicit type casts.", "10", true)
				}
			})
		}
		userOperatorError(t, p, b, c, "SELECT 2 OPERATOR(pg_catalog.+) ANY(ARRAY[2]) AS v",
			"42809", "op ANY/ALL (array) requires operator to yield boolean", "", "", "10", true)
		userOperatorExec(t, p, b, c,
			"CREATE OPERATOR userop_consumer_structure.= (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne)",
			"CREATE OPERATOR userop_consumer_structure.>= (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4lt)",
			"CREATE OPERATOR userop_consumer_structure.<= (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4gt)",
			"CREATE TYPE public.userop_consumer_pair AS (v integer)",
			"CREATE TABLE public.userop_consumer_struct_values(k integer)",
			"CREATE TABLE public.userop_consumer_composite(v public.userop_consumer_pair)",
			"CREATE TABLE public.userop_consumer_wide_left(k int8)",
			"CREATE TABLE public.userop_consumer_wide_right(k int8)",
			"SET search_path TO userop_consumer_structure,pg_catalog,public")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_consumer_struct_values VALUES(2),(2)", 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_consumer_composite VALUES(ROW(2)::public.userop_consumer_pair)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_consumer_wide_left VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_consumer_wide_right VALUES(2)", 1))
		for _, tc := range []struct {
			query, typ string
			value      any
		}{
			{"SELECT 2::int8 = ANY(ARRAY[2::int8]) AS v", "BOOL", true},
			{"SELECT 2 IN (2::int8) AS v", "BOOL", true},
			{"SELECT ARRAY[2] = ARRAY[2] AS v", "BOOL", true},
			{"SELECT ARRAY[2] IN (ARRAY[2]) AS v", "BOOL", true},
			{"SELECT ROW(2)::public.userop_consumer_pair = ROW(2)::public.userop_consumer_pair AS v", "BOOL", true},
			{"SELECT a.v IN (b.v) AS v FROM public.userop_consumer_composite a CROSS JOIN public.userop_consumer_composite b", "BOOL", true},
			{"SELECT NULLIF(2::int8,2::int8) AS v", "INT8", nil},
			{"SELECT CASE 2::int8 WHEN 2::int8 THEN 1 ELSE 0 END AS v", "INT4", int64(1)},
			{"SELECT 2 IS DISTINCT FROM NULL AS v", "BOOL", true},
			{"SELECT GREATEST(2,2) AS v", "INT4", int64(2)},
			{"SELECT LEAST(2,2) AS v", "INT4", int64(2)},
			{"SELECT 2 BETWEEN 1 AND 3 AS v", "BOOL", false},
			{"SELECT count(*) AS v FROM public.userop_consumer_struct_values GROUP BY k", "INT8", int64(2)},
			{"SELECT l.k AS v FROM public.userop_consumer_wide_left l JOIN public.userop_consumer_wide_right r USING(k)", "INT8", int64(2)},
		} {
			t.Run(tc.query, func(t *testing.T) { userOperatorValue(t, p, b, c, tc.query, tc.typ, tc.value) })
		}
		userOperatorExec(t, p, b, c, "SET search_path TO pg_catalog,userop_consumer_structure,public")
		userOperatorValue(t, p, b, c, "SELECT 2 = 2 AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT 2 = ANY(ARRAY[2]) AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT ROW(2) IN (ROW(2)) AS v", "BOOL", true)
		userOperatorValue(t, p, b, c, "SELECT CASE 2 WHEN 2 THEN 10 ELSE 20 END AS v", "INT4", int64(10))
	})
}
