package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeTypeConstructorOwnerPortability(t *testing.T) {
	for _, tc := range []struct{ name, signature, description string }{
		{"range_two", "public.constructor_owner_span(integer,integer)", "constructor_owner_span(integer,integer)"},
		{"range_three", "public.constructor_owner_span(integer,integer,text)", "constructor_owner_span(integer,integer,text)"},
		{"multi_zero", "public.constructor_owner_many()", "constructor_owner_many()"},
		{"multi_one", "public.constructor_owner_many(public.constructor_owner_span)", "constructor_owner_many(constructor_owner_span)"},
		{"multi_variadic", "public.constructor_owner_many(public.constructor_owner_span[])", "constructor_owner_many(constructor_owner_span[])"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const role = "range_constructor_owner"
			fixture.RoleReferenceCleanup(t, []string{role})
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				for _, q := range []string{
					"CREATE ROLE " + role,
					"CREATE TYPE constructor_owner_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=constructor_owner_many)",
					"CREATE TABLE constructor_owner_saved(k integer,r constructor_owner_span,m constructor_owner_many)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO constructor_owner_saved VALUES(1,'[1,3]','{[1,3],[8,9]}'),(2,'[1,4)','{}')", 2))
				saved := fixture.Select("SELECT k,r::text AS r,m::text AS m FROM constructor_owner_saved ORDER BY k", []string{"k", "r", "m"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3],[8,9]}"}, {int64(2), "[1,4)", "{}"}})
				rangeTypeRemedyRoutes(t, p, b, c, saved)
				call, want := "", ""
				switch tc.name {
				case "range_two":
					call, want = "public.constructor_owner_span(1,3)", "[1,3)"
				case "range_three":
					call, want = "public.constructor_owner_span(1,3,'[]')", "[1,3]"
				case "multi_zero":
					call, want = "public.constructor_owner_many()", "{}"
				case "multi_one":
					call, want = "public.constructor_owner_many(public.constructor_owner_span(1,3,'[]'))", "{[1,3]}"
				case "multi_variadic":
					call, want = "public.constructor_owner_many(VARIADIC ARRAY[public.constructor_owner_span(1,3,'[]'),public.constructor_owner_span(8,9,'[]')])", "{[1,3],[8,9]}"
				}
				callable := fixture.Select("SELECT "+call+"::text AS value", []string{"value"}, []string{"TEXT"}, [][]any{{want}})
				rangeTypeRemedyRoutes(t, p, b, c, callable)
				heldCall, err := c.PrepareContext(p.Ctx, callable.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer heldCall.Close()
				ownerQuery := "SELECT p.proowner=(SELECT oid FROM pg_roles WHERE rolname='" + role + "') AS explicit_owner,p.proowner=(SELECT typowner FROM pg_type WHERE oid='bool'::regtype) AS bootstrap_owner FROM pg_proc p WHERE p.oid='" + tc.signature + "'::regprocedure::oid"
				owner := func(changed bool) {
					run(fixture.Select(ownerQuery, []string{"explicit_owner", "bootstrap_owner"}, []string{"BOOL", "BOOL"}, [][]any{{changed, !changed}}))
				}
				owner(false)
				var originalOwner string
				if err := c.QueryRowContext(p.Ctx, "SELECT r.rolname FROM pg_roles r JOIN pg_proc p ON p.proowner=r.oid WHERE p.oid=$1::regprocedure::oid", tc.signature).Scan(&originalOwner); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("ALTER FUNCTION "+tc.signature+" OWNER TO "+role, 0))
				owner(true)
				rangeTypeRemedyRoutes(t, p, b, c, callable)
				callRows, err := heldCall.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RangeConsumeHeld(t, callRows, callable)
				run(fixture.Select("SELECT count(*) AS n FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('constructor_owner_span','_constructor_owner_span','constructor_owner_many','_constructor_owner_many') AND typowner=(SELECT typowner FROM pg_type WHERE oid='bool'::regtype)", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('constructor_owner_span','constructor_owner_many') AND proowner=(SELECT oid FROM pg_roles WHERE rolname='"+role+"')", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('constructor_owner_span','constructor_owner_many') AND proowner=(SELECT typowner FROM pg_type WHERE oid='bool'::regtype)", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
				dependency := fixture.ImplicitError("DROP ROLE "+role, fixture.Diagnostic{Code: "2BP01", Message: `role "` + role + `" cannot be dropped because some objects depend on it`, Detail: "owner of function " + tc.description})
				dependency.ErrorStage = "execute"
				rangeTypeRemedyRoutes(t, p, b, c, dependency)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.signature+" FROM PUBLIC", 0))
				check := func(backend fixture.Backend, executor fixture.Executor) {
					checkRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s) }
					checkRun(fixture.Select(ownerQuery, []string{"explicit_owner", "bootstrap_owner"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
					rangeTypeRemedyRoutes(t, p, backend, executor, saved)
					rangeTypeRemedyRoutes(t, p, backend, executor, callable)
					rangeTypeRemedyRoutes(t, p, backend, executor, dependency)
					checkRun(fixture.Select("SELECT has_function_privilege('"+role+"',$1::text,'EXECUTE') AS owner_exec,has_function_privilege('pg_monitor',$1::text,'EXECUTE') AS public_exec", []string{"owner_exec", "public_exec"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, tc.signature))
					checkRun(fixture.Select("SELECT unnest(proacl)::text AS acl FROM pg_proc WHERE oid=$1::regprocedure::oid", []string{"acl"}, []string{"TEXT"}, [][]any{{role + "=X/" + role}}, tc.signature))
					resultType := "constructor_owner_span"
					if strings.HasPrefix(tc.name, "multi_") {
						resultType = "constructor_owner_many"
					}
					checkRun(fixture.Select("SELECT prorettype=$2::regtype AS result_type,prolang=(SELECT oid FROM pg_language WHERE lanname='internal') AS internal_language,provolatile='i' AS immutable,proparallel='s' AS parallel_safe,proisstrict AS strict FROM pg_proc WHERE oid=$1::regprocedure::oid", []string{"result_type", "internal_language", "immutable", "parallel_safe", "strict"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, strings.HasPrefix(tc.name, "multi_")}}, tc.signature, resultType))
					checkRun(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_type'::regclass AND d.deptype='i' WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('constructor_owner_span','constructor_owner_many') AND d.refobjid IN ('constructor_owner_span'::regtype,'constructor_owner_many'::regtype)", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
					checkRun(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('constructor_owner_span','constructor_owner_many') AND proowner=(SELECT typowner FROM pg_type WHERE oid='bool'::regtype)", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
				}
				check(b, c)
				if !b.PG {
					held, err := c.QueryContext(p.Ctx, saved.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					var script bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
						t.Fatal("current constructor owner and ACL Dump", err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
						t.Fatal("same-engine constructor owner and ACL Restore", err)
					}
					check(b, c)
					fixture.RangeConsumeHeld(t, held, saved)
					callRows, err := heldCall.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RangeConsumeHeld(t, callRows, callable)
				}
				rangeTypeRemedyReplay(t, p, b, c, []string{role}, check, fixture.Step{}, nil)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.signature+" TO PUBLIC", 0))
				run(fixture.Exec("ALTER FUNCTION "+tc.signature+" OWNER TO "+`"`+strings.ReplaceAll(originalOwner, `"`, `""`)+`"`, 0))
				owner(false)
				run(saved)
				rangeTypeRemedyRoutes(t, p, b, c, callable)
				run(fixture.Exec("DROP ROLE "+role, 0))
			})
		})
	}
}
