package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations below are independently predicted from REL_18_6 lookup rules;
// the pinned oracle must resolve them before production changes.
func TestReferenceObjectAddressRoutineResolution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "Odd S")
	fixture.SchemaReferenceCleanup(t, "addr_shadow")
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { objectAddressRun(t, p, b, c, fixture.Exec(q, 0)) }
		for _, q := range []string{
			"CREATE FUNCTION public.addr_recipe_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION public.addr_recipe_f(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION public.addr_recipe_f() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$",
			"CREATE PROCEDURE public.addr_recipe_p(integer) LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE AGGREGATE public.addr_recipe_a(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer)",
			`CREATE SCHEMA "Odd S"`,
			`CREATE DOMAIN "Odd S"."D q" AS integer`,
			`CREATE TABLE "Odd S"."T q"(i integer)`,
			`CREATE FUNCTION "Odd S"."F.q"(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$`,
			`CREATE FUNCTION "Odd S"."F.q"(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$`,
			`CREATE FUNCTION "Odd S"."F.q"("Odd S"."D q",integer[]) RETURNS integer LANGUAGE SQL AS $$SELECT 7$$`,
			`CREATE PROCEDURE "Odd S"."P q"(INOUT i integer) LANGUAGE SQL AS $$SELECT i$$`,
			`CREATE PROCEDURE "Odd S"."P out"(IN i integer,OUT answer integer) LANGUAGE SQL AS $$SELECT i$$`,
			"CREATE SCHEMA addr_shadow",
			"CREATE FUNCTION addr_shadow.addr_recipe_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 9$$",
			"CREATE FUNCTION addr_shadow.addr_recipe_f(text) RETURNS text LANGUAGE SQL AS $$SELECT $1$$",
		} {
			exec(q)
		}
		q := "SELECT proname,prokind::text AS kind FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('addr_recipe_f','addr_recipe_p','addr_recipe_a') ORDER BY proname,pronargs,proargtypes::text"
		objectAddressRun(t, p, b, c, fixture.Select(q, []string{"proname", "kind"}, []string{"NAME", "TEXT"}, [][]any{{"addr_recipe_a", "a"}, {"addr_recipe_f", "f"}, {"addr_recipe_f", "f"}, {"addr_recipe_f", "f"}, {"addr_recipe_p", "p"}}))
		const heldSQL = "SELECT * FROM pg_get_object_address($1,$2::text[],$3::text[])"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		emptyStmt, err := c.PrepareContext(p.Ctx, heldSQL+" LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		defer emptyStmt.Close()
		emptyHeld := fixture.HeldStatement{Executor: c, Statement: emptyStmt, SQL: heldSQL + " LIMIT 0"}
		witness := func(kind string, names, args []string, signature string) int64 {
			t.Helper()
			var id int64
			if err := c.QueryRowContext(p.Ctx, "SELECT "+objectAddressLiteral(signature)+"::regprocedure::oid::bigint").Scan(&id); err != nil || id <= 0 {
				t.Fatal("catalog signature", signature, id, err)
			}
			for _, empty := range []bool{false, true} {
				query := heldSQL
				var row [][]any
				if !empty {
					row = [][]any{{int64(1255), id, int64(0)}}
					if b.PG {
						row = [][]any{{"1255", fmt.Sprint(id), int64(0)}}
					}
				} else {
					query += " LIMIT 0"
				}
				s := fixture.Select(query, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, row, kind, objectAddressArrayText(t, names), objectAddressArrayText(t, args))
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					objectAddressMetadata(t, r, b.PG, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"})
				}
				objectAddressRun(t, p, b, c, s)
				if empty {
					fixture.RunStep(t, p.Ctx, emptyHeld, b.Recorder, s)
				} else {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			scalar := fixture.Select("SELECT pg_get_object_address($1,$2::text[],$3::text[]) AS v", []string{"v"}, []string{"RECORD"}, [][]any{{fmt.Sprintf("(1255,%d,0)", id)}}, kind, objectAddressArrayText(t, names), objectAddressArrayText(t, args))
			scalar.CheckMetadata = func(t *testing.T, r *sql.Rows) { objectAddressMetadata(t, r, b.PG, []string{"v"}, []string{"RECORD"}) }
			objectAddressRun(t, p, b, c, scalar)
			scalarStmt, err := c.PrepareContext(p.Ctx, scalar.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: scalarStmt, SQL: scalar.Query}, b.Recorder, scalar)
			if err := scalarStmt.Close(); err != nil {
				t.Fatal(err)
			}
			return id
		}
		for _, tc := range []struct {
			kind        string
			names, args []string
			signature   string
		}{
			{"function", []string{"public", "addr_recipe_f"}, nil, "public.addr_recipe_f()"},
			{"function", []string{"public", "addr_recipe_f"}, []string{"int"}, "public.addr_recipe_f(integer)"},
			{"function", []string{"public", "addr_recipe_f"}, []string{"bigint"}, "public.addr_recipe_f(bigint)"},
			{"procedure", []string{"public", "addr_recipe_p"}, []string{"integer"}, "public.addr_recipe_p(integer)"},
			{"aggregate", []string{"public", "addr_recipe_a"}, []string{"integer"}, "public.addr_recipe_a(integer)"},
			{"function", []string{"public", "addr_recipe_a"}, []string{"integer"}, "public.addr_recipe_a(integer)"},
			{"aggregate", []string{"pg_catalog", "count"}, []string{"\"any\""}, "pg_catalog.count(\"any\")"},
			{"function", []string{"pg_catalog", "int4larger"}, []string{"integer", "integer"}, "pg_catalog.int4larger(integer,integer)"},
			{"function", []string{"Odd S", "F.q"}, []string{"int4"}, `"Odd S"."F.q"(integer)`},
			{"function", []string{"Odd S", "F.q"}, []string{`"Odd S"."D q"`, "integer[]"}, `"Odd S"."F.q"("Odd S"."D q",integer[])`},
			{"procedure", []string{"Odd S", "P q"}, []string{"integer"}, `"Odd S"."P q"(integer)`},
			{"procedure", []string{"Odd S", "P out"}, []string{"integer"}, `"Odd S"."P out"(integer)`},
		} {
			witness(tc.kind, tc.names, tc.args, tc.signature)
		}
		for _, kind := range []string{"procedure", "aggregate"} {
			name := "addr_recipe_p"
			if kind == "aggregate" {
				name = "addr_recipe_a"
			}
			objectAddressRun(t, p, b, c, fixture.Select("SELECT a.type FROM pg_identify_object_as_address(1255,"+objectAddressLiteral("public."+name+"(integer)")+"::regprocedure,0) a", []string{"type"}, []string{"TEXT"}, [][]any{{kind}}))
		}
		exec("SET search_path TO addr_shadow,public")
		witness("function", []string{"addr_recipe_f"}, []string{"integer"}, "addr_shadow.addr_recipe_f(integer)")
		witness("function", []string{"addr_recipe_f"}, []string{"bigint"}, "public.addr_recipe_f(bigint)")
		witness("function", []string{"public", "addr_recipe_f"}, []string{"integer"}, "public.addr_recipe_f(integer)")
		exec("RESET search_path")
		exec("ALTER FUNCTION public.addr_recipe_f(integer) RENAME TO addr_recipe_renamed")
		s := fixture.ImplicitError(heldSQL, fixture.Diagnostic{Code: "42883", Message: "function public.addr_recipe_f(integer) does not exist"}, "function", "{public,addr_recipe_f}", "{integer}")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		witness("function", []string{"public", "addr_recipe_renamed"}, []string{"integer"}, "public.addr_recipe_renamed(integer)")
		exec("DROP FUNCTION public.addr_recipe_renamed(integer)")
		renamedMissing := fixture.ImplicitError(heldSQL, fixture.Diagnostic{Code: "42883", Message: "function public.addr_recipe_renamed(integer) does not exist"}, "function", "{public,addr_recipe_renamed}", "{integer}")
		renamedMissing.SelectRows, renamedMissing.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, renamedMissing)
		exec("CREATE FUNCTION public.addr_recipe_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
		witness("function", []string{"public", "addr_recipe_f"}, []string{"integer"}, "public.addr_recipe_f(integer)")
		exec("DROP FUNCTION public.addr_recipe_f()")
		s = fixture.ImplicitError(heldSQL, fixture.Diagnostic{Code: "42883", Message: "function public.addr_recipe_f() does not exist"}, "function", "{public,addr_recipe_f}", "{}")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		witness("function", []string{"public", "addr_recipe_f"}, []string{"bigint"}, "public.addr_recipe_f(bigint)")
	})
}

func TestReferenceObjectAddressRoutineDiagnostics(t *testing.T) {
	for _, group := range []string{"identity", "argument_boundary"} {
		t.Run(group, func(t *testing.T) {
			objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{"CREATE FUNCTION public.addr_recipe_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "CREATE PROCEDURE public.addr_recipe_p(integer) LANGUAGE SQL AS $$SELECT $1$$"} {
					objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
				}
				var cases []objectAddressParameterCase
				add := func(name, kind, names, args, code, message string) {
					cases = append(cases, objectAddressParameterCase{name, []any{kind, names, args}, fixture.Diagnostic{Code: code, Message: message}})
				}
				if group == "identity" {
					for _, tc := range []struct{ kind, name, args, code, message string }{
						{"table", "addr_absent_table", "{}", "42P01", `relation "public.addr_absent_table" does not exist`},
						{"function", "addr_absent_function", "{integer}", "42883", "function public.addr_absent_function(integer) does not exist"},
						{"procedure", "addr_absent_procedure", "{integer}", "42883", "procedure public.addr_absent_procedure(integer) does not exist"},
						{"aggregate", "addr_absent_aggregate", "{integer}", "42883", "aggregate public.addr_absent_aggregate(integer) does not exist"},
						{"aggregate", "addr_absent_aggregate", "{}", "42883", "aggregate public.addr_absent_aggregate(*) does not exist"},
						{"procedure", "addr_recipe_f", "{integer}", "42809", "public.addr_recipe_f(integer) is not a procedure"},
						{"function", "addr_recipe_p", "{integer}", "42809", "public.addr_recipe_p(integer) is not a function"},
						{"aggregate", "addr_recipe_f", "{integer}", "42809", "function public.addr_recipe_f(integer) is not an aggregate"},
						{"function", "addr_recipe_f", "{text}", "42883", "function public.addr_recipe_f(text) does not exist"},
					} {
						add(tc.kind+tc.name+tc.args, tc.kind, "{public,"+tc.name+"}", tc.args, tc.code, tc.message)
					}
					add("schema", "function", "{addr_absent_schema,f}", "{integer}", "3F000", `schema "addr_absent_schema" does not exist`)
					add("type", "function", "{public,addr_recipe_f}", "{public.addr_absent_type}", "42704", `type "public.addr_absent_type" does not exist`)
					add("qualification", "function", "{a,b,c,d}", "{}", "42601", "improper qualified name (too many dotted names): a.b.c.d")
					add("database", "function", "{addr_other_database,public,f}", "{}", "0A000", "cross-database references are not implemented: addr_other_database.public.f")
				} else {
					args100 := strings.TrimSuffix(strings.Repeat("integer,", 100), ",")
					add("hundred", "function", "{public,addr_absent_function}", "{"+args100+"}", "42883", "function public.addr_absent_function("+strings.ReplaceAll(args100, ",", ", ")+") does not exist")
					for _, kind := range []string{"function", "procedure", "aggregate"} {
						noun := "functions"
						if kind == "procedure" {
							noun = "procedures"
						}
						add(kind+"101", kind, "{public,addr_absent}", "{"+args100+",integer}", "54023", noun+" cannot have more than 100 arguments")
					}
					add("count_before_type", "function", "{public,addr_absent}", "{public.addr_absent_type,"+args100+"}", "54023", "functions cannot have more than 100 arguments")
					add("count_before_schema", "function", "{addr_absent_schema,f}", "{"+args100+",integer}", "54023", "functions cannot have more than 100 arguments")
					add("null_before_syntax", "function", "{public,addr_absent}", "{NULL,NONE,"+args100+"}", "22023", "name or argument lists may not contain nulls")
					cases = append(cases, objectAddressParameterCase{"syntax_before_null", []any{"function", "{public,addr_absent}", "{NONE,NULL," + args100 + "}"}, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NONE"`, Position: "1", Context: `invalid type name "NONE"`}})
				}
				const q = "SELECT pg_get_object_address($1,$2::text[],$3::text[]) AS v"
				for _, route := range []string{"direct", "fresh", "held"} {
					t.Run(route, func(t *testing.T) {
						var executor fixture.Executor = c
						if route == "held" {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						run := func(t *testing.T, s fixture.Step) {
							if route == "fresh" {
								s = fixture.Prepared(s)
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
						}
						for _, tc := range cases {
							t.Run(tc.name, func(t *testing.T) {
								s := fixture.ImplicitError(q, tc.want, tc.args...)
								s.SelectRows, s.ErrorStage = true, "execute"
								run(t, s)
								run(t, fixture.Select(q, []string{"v"}, []string{"RECORD"}, [][]any{{"(1247,23,0)"}}, "type", "{integer}", "{}"))
							})
						}
					})
				}
			})
		})
	}
}
