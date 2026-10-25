package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantMathLogPowerAuthority(t *testing.T) {
	// REL_18_0 declarations: the first fourteen targets are fixed signatures;
	// threshold arrays select one ANYCOMPATIBLE/ANYCOMPATIBLEARRAY declaration.
	// Values and numeric scales are independent mathematical witnesses, not
	// results copied from the native implementation.
	for _, tc := range []struct {
		label, name, target, inputType, input, rest, resultType string
		argument, want                                          any
		sibling, siblingType                                    string
		siblingWant                                             any
	}{
		{"lgamma_float", "lgamma", "lgamma(double precision)", "DOUBLE PRECISION", "1", "", "FLOAT8", float64(1), float64(0), "ln(x)", "FLOAT8", float64(0)},
		{"ln_float", "ln", "ln(double precision)", "DOUBLE PRECISION", "1", "", "FLOAT8", float64(1), float64(0), "ln(x::NUMERIC)", "NUMERIC", "0.0000000000000000"},
		{"ln_numeric", "ln", "ln(numeric)", "NUMERIC", "1", "", "NUMERIC", "1", "0.0000000000000000", "ln(x::DOUBLE PRECISION)", "FLOAT8", float64(0)},
		{"log_float", "log", "log(double precision)", "DOUBLE PRECISION", "100", "", "FLOAT8", float64(100), float64(2), "log(x::NUMERIC)", "NUMERIC", "2.0000000000000000"},
		{"log_numeric", "log", "log(numeric)", "NUMERIC", "100", "", "NUMERIC", "100", "2.0000000000000000", "log10(x)", "NUMERIC", "2.0000000000000000"},
		{"log_binary", "log", "log(numeric,numeric)", "NUMERIC", "2", ",64::NUMERIC", "NUMERIC", "2", "6.0000000000000000", "log(x::DOUBLE PRECISION * 50)", "FLOAT8", float64(2)},
		{"log10_float", "log10", "log10(double precision)", "DOUBLE PRECISION", "1000", "", "FLOAT8", float64(1000), float64(3), "log10(x::NUMERIC)", "NUMERIC", "3.0000000000000000"},
		{"log10_numeric", "log10", "log10(numeric)", "NUMERIC", "1000", "", "NUMERIC", "1000", "3.0000000000000000", "log(x)", "NUMERIC", "3.0000000000000000"},
		{"power_float", "power", "power(double precision,double precision)", "DOUBLE PRECISION", "2", ",3::DOUBLE PRECISION", "FLOAT8", float64(2), float64(8), "x ^ 3::DOUBLE PRECISION", "FLOAT8", float64(8)},
		{"power_numeric", "power", "power(numeric,numeric)", "NUMERIC", "2", ",3::NUMERIC", "NUMERIC", "2", "8.0000000000000000", "x ^ 3::NUMERIC", "NUMERIC", "8.0000000000000000"},
		{"sqrt_float", "sqrt", "sqrt(double precision)", "DOUBLE PRECISION", "4", "", "FLOAT8", float64(4), float64(2), "sqrt(x::NUMERIC)", "NUMERIC", "2.000000000000000"},
		{"sqrt_numeric", "sqrt", "sqrt(numeric)", "NUMERIC", "4", "", "NUMERIC", "4", "2.000000000000000", "sqrt(x::DOUBLE PRECISION)", "FLOAT8", float64(2)},
		{"bucket_float", "width_bucket", "width_bucket(double precision,double precision,double precision,integer)", "DOUBLE PRECISION", "2", ",0::DOUBLE PRECISION,10::DOUBLE PRECISION,5", "INT4", float64(2), int64(2), "width_bucket(x::NUMERIC,0::NUMERIC,10::NUMERIC,5)", "INT4", int64(2)},
		{"bucket_numeric", "width_bucket", "width_bucket(numeric,numeric,numeric,integer)", "NUMERIC", "2", ",0::NUMERIC,10::NUMERIC,5", "INT4", "2", int64(2), "width_bucket(x::DOUBLE PRECISION,0::DOUBLE PRECISION,10::DOUBLE PRECISION,5)", "INT4", int64(2)},
		{"bucket_threshold", "width_bucket", "width_bucket(anycompatible,anycompatiblearray)", "INTEGER", "10", ",ARRAY[0,10,20]", "INT4", int64(10), int64(2), "width_bucket(x::DOUBLE PRECISION,0::DOUBLE PRECISION,20::DOUBLE PRECISION,4)", "INT4", int64(3)},
	} {
		t.Run(tc.label, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_math_source(id INT,x "+tc.inputType+")", 0))
				run(fixture.Exec("INSERT INTO og_math_source VALUES(1,"+tc.input+"),(2,NULL)", 2))
				run(fixture.Exec("GRANT SELECT ON og_math_source TO og_object_reader", 0))
				call := func(first string) string { return tc.name + "(" + first + tc.rest + ")" }
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_math_source"+tail, []string{"v"}, []string{tc.resultType}, [][]any{{want}}, args...)
				}
				row := call("x")
				run(fixture.Exec("CREATE VIEW og_math_view AS SELECT "+row+" AS v FROM og_math_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_math_view TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					{value(row, " WHERE id=1", tc.want), true, true},
					{value("pg_catalog."+row, " WHERE id=1", tc.want), true, true},
					{value(call("CASE WHEN id=1 THEN $1::"+tc.inputType+" ELSE x END"), " WHERE id=1", tc.want, tc.argument), true, true},
					{fixture.Select("SELECT v FROM og_math_view", []string{"v"}, []string{tc.resultType}, [][]any{{tc.want}}), true, true},
					{value(row, " WHERE id=2", nil), true, true},
					// Constants and fresh parameters are deliberately NOT held: an
					// immutable folded PostgreSQL plan need not be invalidated by ACLs.
					{value(call(tc.input+"::"+tc.inputType), " WHERE id=1", tc.want), true, false},
					{value(call("$1::"+tc.inputType), " WHERE id=1", tc.want, tc.argument), true, false},
					{value(call("NULL::"+tc.inputType), " WHERE id=1", nil), false, false},
					{value(call("$1::"+tc.inputType), " WHERE id=1", nil, nil), false, false},
					{value("CASE WHEN FALSE THEN "+row+" ELSE NULL END", " WHERE id=1", nil), false, true},
					{fixture.Select("SELECT "+row+" AS v FROM og_math_source WHERE id>99", []string{"v"}, []string{tc.resultType}, nil), true, true},
					{fixture.Select("SELECT "+row+" AS v FROM og_math_source WHERE FALSE", []string{"v"}, []string{tc.resultType}, nil), true, true},
					{fixture.Select("SELECT "+row+" AS v FROM og_math_source LIMIT 0", []string{"v"}, []string{tc.resultType}, nil), true, true},
				}
				controls := []fixture.Step{
					fixture.Select("SELECT "+tc.sibling+" AS v FROM og_math_source WHERE id=1", []string{"v"}, []string{tc.siblingType}, [][]any{{tc.siblingWant}}),
				}
				if tc.label == "power_float" {
					controls = append(controls, fixture.Select("SELECT power(x::NUMERIC,3::NUMERIC) AS v FROM og_math_source WHERE id=1", []string{"v"}, []string{"NUMERIC"}, [][]any{{"8.0000000000000000"}}))
				}
				if tc.label == "power_numeric" {
					controls = append(controls, fixture.Select("SELECT power(x::DOUBLE PRECISION,3::DOUBLE PRECISION) AS v FROM og_math_source WHERE id=1", []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(8)}}))
				}
				// PostgreSQL can retain an inlined unary SQL wrapper even with
				// variable inputs: its binary log body survives, its outer identity
				// does not. Fresh executions still require the outer EXECUTE.
				// Keep every original held route, with independent expectations,
				// rather than pretending that preparing anew proves held freshness.
				var wrapperHeld []objectGrantProviderCase
				if tc.label == "log_numeric" || tc.label == "log10_numeric" {
					for i := range cases {
						if cases[i].held {
							wrapperHeld = append(wrapperHeld, cases[i])
							cases[i].held = false
						}
					}
				}
				target := "pg_catalog." + tc.target
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, target, "function", tc.name, cases, controls)
				// The lifecycle already ran these cases fresh in the same ACL states;
				// the held block below repeats only the held executions, which do
				// not depend on the route, so only the prepared route runs it.
				if len(wrapperHeld) == 0 || !prepared {
					return
				}
				// The common lifecycle restores PUBLIC and clears actor grants
				// before returning. Warm these actual statements once as the actor.
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				held := make([]fixture.HeldStatement, len(wrapperHeld))
				defer func() {
					for _, h := range held {
						if h.Statement != nil {
							h.Statement.Close()
						}
					}
				}()
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					for i, wc := range wrapperHeld {
						stmt, err := c.PrepareContext(p.Ctx, wc.step.Query)
						if err != nil {
							t.Fatal(err)
						}
						held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: wc.step.Query}
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, wc.step)
					}
				})
				for phase, allowed := range []bool{false, true, false} {
					if phase == 0 {
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
					} else if allowed {
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
					} else {
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
					}
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for i, wc := range wrapperHeld {
							warm := wc.step
							// Parameterized PostgreSQL custom plans reanalyze the
							// wrapper; only its cached parameter-free body survives.
							if !allowed && wc.needed && (!b.PG || len(wc.step.Args) != 0) {
								warm = fixture.ImplicitError(wc.step.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name}, wc.step.Args...)
								warm.SelectRows, warm.ErrorStage = true, "execute"
							}
							objectGrantStep(t, p.Ctx, held[i], b.Recorder, warm)
						}
					})
				}
			})
		})
	}

	for _, name := range []string{"log", "log10"} {
		t.Run(name+"_body_current", func(t *testing.T) {
			// Reuse the existing real authenticated/original held body seam.
			// 1741 and 1481 each call 1736, not each other or numeric_log.
			objectGrantBodyConsumer(t, objectGrantBodyCase{
				name: name, columns: "n NUMERIC", values: "100",
				expression: name + "(n)", nullExpression: name + "(NULL::NUMERIC)",
				deadExpression: "CASE WHEN FALSE THEN " + name + "(n) ELSE 2.0000000000000000::NUMERIC END",
				resultType:     "NUMERIC", value: "2.0000000000000000",
				target: "pg_catalog.log(numeric,numeric)", deniedName: "log",
				primitive: "log(10::NUMERIC,n)", primitiveType: "NUMERIC", primitiveValue: "2.0000000000000000",
				independent: name + "(n::DOUBLE PRECISION)", independentType: "FLOAT8", independentValue: float64(2),
			})
		})
		t.Run(name+"_body_demand", func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				targets := []string{"pg_catalog." + name + "(numeric)", "pg_catalog.log(numeric,numeric)"}
				defer objectGrantRoutineCleanup(t, p, c, targets)
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_math_body(n NUMERIC)", 0))
				run(fixture.Exec("INSERT INTO og_math_body VALUES(100)", 1))
				run(fixture.Exec("GRANT SELECT ON og_math_body TO og_object_reader", 0))
				run(fixture.Exec("CREATE VIEW og_math_body_view AS SELECT "+name+"(n) AS v FROM og_math_body", 0))
				run(fixture.Exec("GRANT SELECT ON og_math_body_view TO og_object_reader", 0))
				denied := func(q, deniedName, context string) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + deniedName, Context: context})
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				for _, bodyAllowed := range []bool{false, true, false} {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.log(numeric,numeric) FROM PUBLIC", 0))
					verb := "REVOKE EXECUTE ON FUNCTION pg_catalog.log(numeric,numeric) FROM og_object_reader"
					if bodyAllowed {
						verb = "GRANT EXECUTE ON FUNCTION pg_catalog.log(numeric,numeric) TO og_object_reader"
					}
					run(fixture.Exec(verb, 0))
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for _, q := range []string{"SELECT " + name + "(100::NUMERIC) AS v", "SELECT " + name + "($1::NUMERIC) AS v"} {
							var args []any
							if strings.Contains(q, "$1") {
								args = []any{"100"}
							}
							s := fixture.Select(q, []string{"v"}, []string{"NUMERIC"}, [][]any{{"2.0000000000000000"}}, args...)
							if !bodyAllowed {
								s = denied(q, "log", "SQL function \""+name+"\" statement 1")
								s.Args = args
							}
							run(s)
						}
						for _, expr := range []string{name + "(NULL::NUMERIC)", name + "($1::NUMERIC)"} {
							var args []any
							if strings.Contains(expr, "$1") {
								args = []any{nil}
							}
							run(fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"NUMERIC"}, [][]any{{nil}}, args...))
						}
						// Use a correlated LATERAL FROM so the input is genuinely row-backed.
						q := "SELECT v FROM og_math_body CROSS JOIN LATERAL " + name + "(n) AS v"
						fromStep := func(s fixture.Step) fixture.Step {
							if b.PG {
								return s
							}
							s = fixture.ImplicitError(s.Query, fixture.Diagnostic{
								Code: "0A000", Message: "function table source is unsupported",
								Position: fixture.Position(strings.Index(s.Query, name+"(") + 1),
							}, s.Args...)
							s.SelectRows, s.ErrorStage = true, "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							return s
						}
						if bodyAllowed {
							run(fromStep(fixture.Select(q, []string{"v"}, []string{"NUMERIC"}, [][]any{{"2.0000000000000000"}})))
						} else {
							run(fromStep(denied(q, "log", "")))
						}
						if bodyAllowed {
							run(fixture.Select("SELECT v FROM og_math_body_view", []string{"v"}, []string{"NUMERIC"}, [][]any{{"2.0000000000000000"}}))
						} else {
							run(denied("SELECT v FROM og_math_body_view", "log", ""))
						}
						run(fromStep(fixture.Select("SELECT v FROM "+name+"(NULL::NUMERIC) AS v", []string{"v"}, []string{"NUMERIC"}, [][]any{{nil}})))
						run(fromStep(fixture.Select("SELECT v FROM "+name+"($1::NUMERIC) AS v", []string{"v"}, []string{"NUMERIC"}, [][]any{{nil}}, nil)))
					})
				}
				// Granting the inner function does not authorize the outer wrapper.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.log(numeric,numeric) TO og_object_reader", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+targets[0]+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(denied("SELECT "+name+"(n) AS v FROM og_math_body", name, ""))
					run(denied("SELECT "+name+"(100::NUMERIC) AS v", name, ""))
				})
			})
		})
	}

	t.Run("threshold_instances", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
			run(fixture.Exec("CREATE TYPE og_math_enum AS ENUM('a','m','z')", 0))
			run(fixture.Exec("CREATE DOMAIN og_math_domain AS INTEGER CHECK(VALUE>=0)", 0))
			run(fixture.Exec("CREATE TABLE og_math_threshold(id INT,i INT,t TEXT,e og_math_enum,d og_math_domain,dt DATE,a INT[])", 0))
			run(fixture.Exec("INSERT INTO og_math_threshold VALUES(1,10,'m','m',10,'2000-01-10','{0,10,20}'),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
			run(fixture.Exec("GRANT SELECT ON og_math_threshold TO og_object_reader", 0))
			value := func(expr, tail string, want any, args ...any) fixture.Step {
				return fixture.Select("SELECT "+expr+" AS v FROM og_math_threshold"+tail, []string{"v"}, []string{"INT4"}, [][]any{{want}}, args...)
			}
			cases := []objectGrantProviderCase{
				{value("width_bucket(i,a)", " WHERE id=1", int64(2)), true, true},
				{value("width_bucket(t,ARRAY['a','m','z']::TEXT[])", " WHERE id=1", int64(2)), true, true},
				{value("width_bucket(e,ARRAY['a','m','z']::og_math_enum[])", " WHERE id=1", int64(2)), true, true},
				{value("width_bucket(d,ARRAY[0,10,20]::og_math_domain[])", " WHERE id=1", int64(2)), true, true},
				{value("width_bucket(dt,ARRAY['2000-01-01','2000-01-10','2000-01-20']::DATE[])", " WHERE id=1", int64(2)), true, true},
				{value("width_bucket(i,ARRAY[]::INT[])", " WHERE id=1", int64(0)), true, true},
				{value("width_bucket(i,ARRAY[0,10,10,20])", " WHERE id=1", int64(3)), true, true},
				{value("width_bucket(i,a)", " WHERE id=2", nil), true, true},
				{value("width_bucket(i,NULL::INT[])", " WHERE id=1", nil), false, false},
				{value("width_bucket(NULL::INT,a)", " WHERE id=1", nil), false, false},
				{value("width_bucket(i,$1::INT[])", " WHERE id=1", nil, nil), false, false},
				{value("width_bucket($1::INT,a)", " WHERE id=1", nil, nil), false, false},
			}
			for _, bad := range []struct{ array, code, message string }{
				{"ARRAY[0,NULL,20]", "22004", "thresholds array must not contain NULLs"},
				{"ARRAY[[0,10],[20,30]]", "2202E", "thresholds must be one-dimensional array"},
			} {
				s := fixture.ImplicitError("SELECT width_bucket(i,"+bad.array+") AS v FROM og_math_threshold WHERE id=1", fixture.Diagnostic{Code: bad.code, Message: bad.message})
				s.SelectRows, s.ErrorStage = true, "execute"
				cases = append(cases, objectGrantProviderCase{s, true, true})
			}
			objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.width_bucket(anycompatible,anycompatiblearray)", "function", "width_bucket", cases, []fixture.Step{
				value("width_bucket(i::NUMERIC,0::NUMERIC,20::NUMERIC,4)", " WHERE id=1", int64(3)),
			})
		})
	})

	t.Run("internal_helpers_not_sql_edges", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			targets := []string{"pg_catalog.dpow(double precision,double precision)", "pg_catalog.numeric_power(numeric,numeric)", "pg_catalog.btint4cmp(integer,integer)", "pg_catalog.bttextcmp(text,text)"}
			defer objectGrantRoutineCleanup(t, p, c, targets)
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
			run(fixture.Exec("CREATE TABLE og_math_internal(x INT,t TEXT)", 0))
			run(fixture.Exec("INSERT INTO og_math_internal VALUES(2,'m')", 1))
			run(fixture.Exec("GRANT SELECT,INSERT ON og_math_internal TO og_object_reader", 0))
			for _, target := range targets {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
			}
			// Physical index support captures the registered routine without
			// borrowing the invoker's scalar EXECUTE privilege.
			run(fixture.Exec("CREATE INDEX og_math_internal_i ON og_math_internal(x)", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select("SELECT power(x::DOUBLE PRECISION,3::DOUBLE PRECISION) AS f,power(x::NUMERIC,3::NUMERIC) AS n,width_bucket(x,ARRAY[0,2,4]) AS i,width_bucket(t,ARRAY['a','m','z']) AS t FROM og_math_internal", []string{"f", "n", "i", "t"}, []string{"FLOAT8", "NUMERIC", "INT4", "INT4"}, [][]any{{float64(8), "8.0000000000000000", int64(2), int64(2)}}))
				fixture.RoleGrantPermissionDenied(run, "SELECT x::DOUBLE PRECISION ^ 3::DOUBLE PRECISION FROM og_math_internal", "function", "dpow", prepared)
				fixture.RoleGrantPermissionDenied(run, "SELECT x::NUMERIC ^ 3::NUMERIC FROM og_math_internal", "function", "numeric_power", prepared)
				fixture.RoleGrantPermissionDenied(run, "SELECT pg_catalog.btint4cmp(x,3) FROM og_math_internal", "function", "btint4cmp", prepared)
				run(fixture.Exec("INSERT INTO og_math_internal VALUES(3,'z')", 1))
			})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.btint4cmp(integer,integer) TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select("SELECT pg_catalog.btint4cmp(x,3) AS v FROM og_math_internal ORDER BY x", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(0)}}))
			})
		})
	})

	t.Run("child_error_precedes_strict_null", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.power(numeric,numeric)"})
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.power(numeric,numeric) FROM PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for _, q := range []string{"SELECT power(NULL::NUMERIC,ln(0::NUMERIC)) AS v", "SELECT power(2::NUMERIC,ln(0::NUMERIC)) AS v"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"})
					s.SelectRows, s.ErrorStage = true, "execute"
					run(s)
				}
			})
		})
	})

	t.Run("stored_writes_current_invoker", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.power(numeric,numeric)"})
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
			for _, q := range []string{
				"CREATE TABLE og_math_default(id INT,v NUMERIC DEFAULT power(2::NUMERIC,3::NUMERIC))",
				"CREATE TABLE og_math_generated(id INT,x NUMERIC,v NUMERIC GENERATED ALWAYS AS (power(x,3::NUMERIC)) STORED)",
				"CREATE TABLE og_math_check(id INT,x NUMERIC CHECK(power(x,3::NUMERIC)=8::NUMERIC))",
				"CREATE TABLE og_math_index(id INT,x NUMERIC)",
				"CREATE INDEX og_math_index_key ON og_math_index(power(x,3::NUMERIC))",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, table := range []string{"og_math_default", "og_math_generated", "og_math_check", "og_math_index"} {
				run(fixture.Exec("GRANT SELECT,INSERT ON "+table+" TO og_object_reader", 0))
			}
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.power(numeric,numeric) FROM PUBLIC", 0))
			for phase, allowed := range []bool{false, true, false} {
				if phase == 1 {
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.power(numeric,numeric) TO og_object_reader", 0))
				} else if phase == 2 {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.power(numeric,numeric) FROM og_object_reader", 0))
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					for _, table := range []string{"og_math_default", "og_math_generated", "og_math_check", "og_math_index"} {
						q := "INSERT INTO " + table + "(id,x) VALUES(1,2)"
						if table == "og_math_default" {
							q = "INSERT INTO " + table + "(id) VALUES(1)"
						}
						if allowed {
							run(fixture.Exec(q, 1))
						} else {
							fixture.RoleGrantPermissionDenied(run, q, "function", "power")
						}
						count := int64(0)
						if phase > 0 {
							count = 1
						}
						run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
					}
					if allowed {
						for _, table := range []string{"og_math_default", "og_math_generated"} {
							run(fixture.Select("SELECT v FROM "+table, []string{"v"}, []string{"NUMERIC"}, [][]any{{"8.0000000000000000"}}))
						}
					}
				})
			}
		})
	})
}
