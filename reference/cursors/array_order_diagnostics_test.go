package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayOrderErrorBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s := fixture.ImplicitError(q, d, args...)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared && analysis {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func TestReferenceArrayOrderTypingDiagnostics(t *testing.T) {
	t.Run("signatures", func(t *testing.T) {
		arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE ao_diag(id int)", "CREATE TABLE ao_diag_empty(id int)", "INSERT INTO ao_diag VALUES(1)", "CREATE DOMAIN ao_domain AS int", "CREATE DOMAIN ao_other_domain AS int", "CREATE TYPE ao_e AS ENUM ('a')", "CREATE TYPE ao_f AS ENUM ('a')"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, count))
			}
			const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct{ name, q, message, position string }{
				{"bool", "SELECT ARRAY[1] = ARRAY[TRUE]", "operator does not exist: integer[] = boolean[]", "17"},
				{"cross_width", "SELECT ARRAY[1] = ARRAY[1::bigint]", "operator does not exist: integer[] = bigint[]", "17"},
				{"canonical_ne", "SELECT ARRAY[1] != ARRAY[TRUE]", "operator does not exist: integer[] <> boolean[]", "17"},
				{"null_signature", "SELECT NULL::int[] = NULL::bigint[]", "operator does not exist: integer[] = bigint[]", "20"},
				{"unicode", "SELECT /*é*/ARRAY[1] = ARRAY[TRUE]", "operator does not exist: integer[] = boolean[]", "22"},
				{"domain_element", "SELECT ARRAY[1::ao_domain] = ARRAY[1]", "operator does not exist: ao_domain[] = integer[]", "28"},
				{"distinct_domain", "SELECT ARRAY[1::ao_domain] = ARRAY[1::ao_other_domain]", "operator does not exist: ao_domain[] = ao_other_domain[]", "28"},
				{"enum_identity", "SELECT ARRAY['a'::ao_e] = ARRAY['a'::ao_f]", "operator does not exist: ao_e[] = ao_f[]", "25"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, source := range []string{"ao_diag", "ao_diag_empty"} {
						t.Run(source, func(t *testing.T) {
							arrayOrderErrorBoth(t, p, b, c, tc.q+" FROM "+source, fixture.Diagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: hint}, true)
						})
					}
				})
			}
			for _, tc := range []struct {
				q string
				d fixture.Diagnostic
			}{
				{"SELECT 'oops' = '{1}'::int[]", fixture.Diagnostic{Code: "22P02", Message: `malformed array literal: "oops"`, Detail: `Array value must start with "{" or dimension information.`, Position: "8"}},
				{"SELECT ARRAY[1] OPERATOR(ao_missing.=) ARRAY[1]", fixture.Diagnostic{Code: "3F000", Message: `schema "ao_missing" does not exist`, Position: "17"}},
			} {
				arrayOrderErrorBoth(t, p, b, c, tc.q, tc.d, true)
			}
			arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT ARRAY[1] OPERATOR(pg_catalog.=) ARRAY[1]", []any{true}))
		})
	})
	t.Run("hidden_signature_names", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "ao_types")
		arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE SCHEMA ao_types",
				"CREATE DOMAIN public.ao_hidden_domain AS int",
				"CREATE DOMAIN ao_types.ao_hidden_domain AS int",
				"CREATE TYPE public.ao_hidden_enum AS ENUM ('a')",
				"CREATE TYPE ao_types.ao_hidden_enum AS ENUM ('a')",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			for _, tc := range []struct{ q, message string }{
				{"SELECT ARRAY[1::ao_types.ao_hidden_domain] = ARRAY[1]", "operator does not exist: ao_types.ao_hidden_domain[] = integer[]"},
				{"SELECT ARRAY['a'::ao_types.ao_hidden_enum] = ARRAY['a'::public.ao_hidden_enum]", "operator does not exist: ao_types.ao_hidden_enum[] = ao_hidden_enum[]"},
			} {
				d := fixture.Diagnostic{Code: "42883", Message: tc.message, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(strings.Index(tc.q, "=") + 1)}
				arrayOrderErrorBoth(t, p, b, c, tc.q, d, true)
			}
		})
	})
	t.Run("provider_demand", func(t *testing.T) {
		arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, a := range []string{"'{}'::json[]", "'{NULL}'::json[]", "ARRAY['{}'::json]"} {
				t.Run(a, func(t *testing.T) {
					for _, op := range []string{"=", "<>"} {
						arrayOrderErrorBoth(t, p, b, c, "SELECT "+a+op+a, fixture.Diagnostic{Code: "42883", Message: "could not identify an equality operator for type json"}, false)
					}
					for _, op := range []string{"<", "<=", ">", ">="} {
						arrayOrderErrorBoth(t, p, b, c, "SELECT "+a+op+a, fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type json"}, false)
					}
				})
			}
			arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT '{}'::json[]='{NULL}'::json[],'{}'::json[]<>'{NULL}'::json[],'[0:0]={NULL}'::json[]='{NULL}'::json[]", []any{false, true, false}))
			for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
				arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT NULL::json[]"+op+"'{}'::json[]", []any{nil}))
			}
			arrayOrderErrorBoth(t, p, b, c, "SELECT '{}'::json[]<'{NULL}'::json[]", fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type json"}, false)
			for _, a := range []string{"'{}'::aclitem[]", "'{NULL}'::aclitem[]", "ARRAY['=r/postgres'::aclitem]"} {
				arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT "+a+"="+a, []any{true}))
				arrayOrderErrorBoth(t, p, b, c, "SELECT "+a+"<"+a, fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type aclitem"}, false)
			}
		})
	})
	t.Run("jsonpath_provider_demand", func(t *testing.T) {
		arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, a := range []string{"'{}'::jsonpath[]", "'{NULL}'::jsonpath[]", "ARRAY['$'::jsonpath]"} {
				arrayOrderErrorBoth(t, p, b, c, "SELECT "+a+"="+a, fixture.Diagnostic{Code: "42883", Message: "could not identify an equality operator for type jsonpath"}, false)
				arrayOrderErrorBoth(t, p, b, c, "SELECT "+a+"<"+a, fixture.Diagnostic{Code: "42883", Message: "could not identify a comparison function for type jsonpath"}, false)
			}
			arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT '{}'::jsonpath[]='{NULL}'::jsonpath[],NULL::jsonpath[]='{}'::jsonpath[],NULL::jsonpath[]<'{}'::jsonpath[]", []any{false, nil, nil}))
		})
	})
	t.Run("collation_demand", func(t *testing.T) {
		arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ao_collation(id int,a text[] COLLATE \"C\",b text[] COLLATE \"POSIX\")", 0))
			run(fixture.Exec("INSERT INTO ao_collation VALUES(1,'{}','{a}'),(2,'{}','{}'),(3,'{NULL}','{NULL}'),(4,'{NULL}','{a}'),(5,'{a}','{a}')", 5))
			for _, tc := range []struct {
				id     int
				eq, lt bool
			}{{1, false, true}, {2, true, false}, {3, true, false}, {4, false, false}} {
				arrayCompareBoth(t, p, b, c, fixture.Select("SELECT a=b AS eq,a<b AS lt FROM ao_collation WHERE id=$1", []string{"eq", "lt"}, []string{"BOOL", "BOOL"}, [][]any{{tc.eq, tc.lt}}, tc.id))
			}
			d := fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
			for _, op := range []string{"=", "<"} {
				arrayOrderErrorBoth(t, p, b, c, "SELECT a"+op+"b FROM ao_collation WHERE id=5", d, false)
			}
			arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT a COLLATE \"C\"=b COLLATE \"C\" FROM ao_collation WHERE id=5", []any{true}))
		})
	})
	t.Run("authority", arrayOrderAuthority)
}

func arrayOrderAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ao_reader"})
	fixture.SchemaReferenceCleanup(t, "ao_denied")
	arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{"CREATE ROLE ao_reader", "CREATE TABLE ao_acl(id int,a int[],b int[])", "GRANT SELECT(id) ON ao_acl TO ao_reader"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE SCHEMA ao_denied", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ao_reader", 0))
		}
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec("INSERT INTO ao_acl VALUES(1,'{1}','{1}')", 1))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION ao_reader", 0))
			arrayOrderErrorBoth(t, p, b, c, "SELECT ARRAY[1] OPERATOR(ao_denied.=) ARRAY[1]", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema ao_denied", Position: "17"}, true)
			for _, q := range []string{"SELECT a=b FROM ao_acl", "SELECT a=b FROM ao_acl WHERE FALSE", "SELECT NULL::int[]=a FROM ao_acl", "SELECT 1=ANY(ARRAY[1,id]) OR a=b FROM ao_acl"} {
				arrayOrderErrorBoth(t, p, b, c, q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ao_acl"}, false)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT SELECT(a,b) ON ao_acl TO ao_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ao_reader", 0))
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT a=b FROM ao_acl", []any{true}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
