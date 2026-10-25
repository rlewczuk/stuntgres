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
				var baseline bytes.Buffer
				if !b.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
						t.Fatal(err)
					}
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
				if !b.PG {
					statement, err := c.PrepareContext(p.Ctx, saved.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					held, err := statement.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					var rejected bytes.Buffer
					err = stuntgres.Dump(p.Ctx, p.Native, &rejected)
					if err == nil || !strings.Contains(err.Error(), "dump cannot serialize altered generated constructor authority") || rejected.Len() != 0 {
						t.Fatal("independently owned constructor must reject before output", err, rejected.String())
					}
					owner(true)
					run(saved)
					rangeTypeRemedyRoutes(t, p, b, c, dependency)
					fixture.RangeConsumeHeld(t, held, saved)
					again, err := statement.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RangeConsumeHeld(t, again, saved)
				}
				run(fixture.Exec("ALTER FUNCTION "+tc.signature+" OWNER TO "+`"`+strings.ReplaceAll(originalOwner, `"`, `""`)+`"`, 0))
				owner(false)
				run(saved)
				if !b.PG {
					var restored bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &restored); err != nil || !bytes.Equal(baseline.Bytes(), restored.Bytes()) {
						t.Fatal("failed dump changed descriptors or baseline portability", err)
					}
					target := fixture.NewFixture(t, false, true)
					if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(restored.Bytes())); err != nil {
						t.Fatal("restored original constructor authority baseline", err)
					}
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, saved)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, callable)
				}
				run(fixture.Exec("DROP ROLE "+role, 0))
			})
		})
	}
}
