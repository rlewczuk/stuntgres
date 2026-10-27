package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Review3 expectations follow the released coercion rules, independently of
// native output: exiting logical UNKNOWN through explicit TEXT means OID 25.
// The inner string output/input operation must still run. BPCHAR sources retain
// their padded bytes; casts to UNKNOWN remain within selected inquiry arguments.
func TestReferenceTypeDescribeTextExit(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, query := range []string{
			"CREATE DOMAIN public.td_exit_text AS text CONSTRAINT td_exit_text_check CHECK(VALUE<>'bad')",
			"CREATE TABLE public.td_exit_inputs(id integer,v text,d public.td_exit_text,p character(3))",
			"CREATE TABLE public.td_exit_recipes(id integer,constant_type regtype DEFAULT pg_typeof('x'::text::pg_catalog.unknown::text),null_type regtype DEFAULT pg_typeof(NULL::text::pg_catalog.unknown::text),unknown_type regtype DEFAULT pg_typeof('x'::text::pg_catalog.unknown),v text DEFAULT 'x',CONSTRAINT td_exit_recipe_check CHECK(pg_typeof(v::pg_catalog.unknown::text)::oid=25::oid))",
		} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("INSERT INTO public.td_exit_inputs VALUES(1,'x','x','x'),(2,NULL,NULL,NULL)", 2))
		run(fixture.Exec("INSERT INTO public.td_exit_recipes(id) VALUES(1)", 1))

		for _, source := range []string{
			"'x'::text", "NULL::text", "'x'::public.td_exit_text", "NULL::public.td_exit_text", "'x'::character(3)",
		} {
			typeDescribeBoth(run, fixture.Select(
				"SELECT pg_typeof("+source+"::pg_catalog.unknown::text) AS concrete,pg_typeof("+source+"::pg_catalog.unknown::text)::oid::bigint AS concrete_oid,pg_typeof("+source+"::pg_catalog.unknown) AS logical,pg_typeof("+source+"::pg_catalog.unknown)::oid::bigint AS logical_oid",
				[]string{"concrete", "concrete_oid", "logical", "logical_oid"},
				[]string{"REGTYPE", "INT8", "REGTYPE", "INT8"},
				[][]any{{"text", int64(25), "unknown", int64(705)}},
			))
		}
		for _, literal := range []string{"'x'", "NULL"} {
			for _, target := range []string{"pg_catalog.unknown", `"any"`, "anyelement", "anynonarray", "anycompatible", "anycompatiblenonarray"} {
				source := literal + "::" + target + "::pg_catalog.unknown"
				typeDescribeBoth(run, fixture.Select(
					"SELECT pg_typeof("+source+"::text) AS concrete,pg_typeof("+source+"::text)::oid::bigint AS concrete_oid,pg_typeof("+source+") AS logical,pg_typeof("+source+")::oid::bigint AS logical_oid",
					[]string{"concrete", "concrete_oid", "logical", "logical_oid"}, []string{"REGTYPE", "INT8", "REGTYPE", "INT8"},
					[][]any{{"text", int64(25), "unknown", int64(705)}},
				))
			}
		}
		query := "SELECT pg_typeof($1::text::pg_catalog.unknown::text) AS concrete,pg_typeof($1::text::pg_catalog.unknown::text)::oid::bigint AS concrete_oid,pg_typeof($1::text::pg_catalog.unknown) AS logical,pg_typeof($1::text::pg_catalog.unknown)::oid::bigint AS logical_oid"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, value := range []any{"first", nil, "second"} {
			s := fixture.Select(query,
				[]string{"concrete", "concrete_oid", "logical", "logical_oid"},
				[]string{"REGTYPE", "INT8", "REGTYPE", "INT8"},
				[][]any{{"text", int64(25), "unknown", int64(705)}}, value)
			typeDescribeBoth(run, s)
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
		}

		run(fixture.Exec("CREATE VIEW public.td_exit_constants AS SELECT pg_typeof('x'::text::pg_catalog.unknown::text) AS concrete,pg_typeof(NULL::text::pg_catalog.unknown::text) AS missing,pg_typeof('x'::text::pg_catalog.unknown) AS logical,pg_typeof('x'::character(3)::pg_catalog.unknown::text) AS padded", 0))
		run(fixture.Exec("CREATE VIEW public.td_exit_sources AS SELECT id,pg_typeof(v::pg_catalog.unknown::text) AS concrete,pg_typeof(d::pg_catalog.unknown::text) AS nominal_exit,pg_typeof(p::pg_catalog.unknown::text) AS padded_exit,pg_typeof(v::pg_catalog.unknown) AS logical,pg_typeof(d::pg_catalog.unknown) AS nominal_logical,pg_typeof(p::pg_catalog.unknown) AS padded_logical,v AS value,p AS padded FROM public.td_exit_inputs", 0))
		for _, pretty := range []bool{false, true} {
			for _, name := range []string{"td_exit_constants", "td_exit_sources"} {
				fixture.ViewCopySQL(t, p, b, fmt.Sprintf("public.%s_%t", name, pretty), "SELECT pg_get_viewdef('public."+name+"'::regclass,$1::boolean) AS definition", pretty)
			}
		}
		proof := func(run func(fixture.Step)) {
			for _, suffix := range []string{"", "_false", "_true"} {
				typeDescribeBoth(run, fixture.Select(
					"SELECT concrete,missing,logical,padded,concrete::oid::bigint AS a,missing::oid::bigint AS b,logical::oid::bigint AS c,padded::oid::bigint AS d FROM public.td_exit_constants"+suffix,
					[]string{"concrete", "missing", "logical", "padded", "a", "b", "c", "d"},
					[]string{"REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "INT8", "INT8", "INT8", "INT8"},
					[][]any{{"text", "text", "unknown", "text", int64(25), int64(25), int64(705), int64(25)}},
				))
				typeDescribeBoth(run, fixture.Select(
					"SELECT id,concrete,nominal_exit,padded_exit,logical,nominal_logical,padded_logical,value,padded FROM public.td_exit_sources"+suffix+" ORDER BY id",
					[]string{"id", "concrete", "nominal_exit", "padded_exit", "logical", "nominal_logical", "padded_logical", "value", "padded"},
					[]string{"INT4", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "REGTYPE", "TEXT", "BPCHAR"},
					[][]any{{int64(1), "text", "text", "text", "unknown", "unknown", "unknown", "x", "x  "}, {int64(2), "text", "text", "text", "unknown", "unknown", "unknown", nil, nil}},
				))
				typeDescribeBoth(run, fixture.Select(
					"SELECT id,concrete::oid::bigint AS a,nominal_exit::oid::bigint AS b,padded_exit::oid::bigint AS c,logical::oid::bigint AS d,nominal_logical::oid::bigint AS e,padded_logical::oid::bigint AS f FROM public.td_exit_sources"+suffix+" ORDER BY id",
					[]string{"id", "a", "b", "c", "d", "e", "f"}, []string{"INT4", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"},
					[][]any{{int64(1), int64(25), int64(25), int64(25), int64(705), int64(705), int64(705)}, {int64(2), int64(25), int64(25), int64(25), int64(705), int64(705), int64(705)}},
				))
			}
			typeDescribeBoth(run, fixture.Select(
				"SELECT id,constant_type,null_type,unknown_type,v,constant_type::oid::bigint AS a,null_type::oid::bigint AS b,unknown_type::oid::bigint AS c FROM public.td_exit_recipes ORDER BY id",
				[]string{"id", "constant_type", "null_type", "unknown_type", "v", "a", "b", "c"},
				[]string{"INT4", "REGTYPE", "REGTYPE", "REGTYPE", "TEXT", "INT8", "INT8", "INT8"},
				[][]any{{int64(1), "text", "text", "unknown", "x", int64(25), int64(25), int64(705)}},
			))
			// Re-evaluate the stored defaults and CHECK, not just restored rows.
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("INSERT INTO public.td_exit_recipes(id) VALUES(2) RETURNING constant_type::oid::bigint AS a,null_type::oid::bigint AS b,unknown_type::oid::bigint AS c,v", []string{"a", "b", "c", "v"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{int64(25), int64(25), int64(705), "x"}})
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Exec("DELETE FROM public.td_exit_recipes WHERE id=2", 1))
			}
		}
		// The domain relationship uses an actual source, including its typed NULL.
		// Keep it out of typeDescribeValue, whose query intentionally has no FROM.
		proofNominal := func(run func(fixture.Step)) {
			typeDescribeBoth(run, fixture.Select("SELECT id,pg_typeof(d)::oid='public.td_exit_text'::regtype::oid AS nominal FROM public.td_exit_inputs ORDER BY id", []string{"id", "nominal"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}}))
		}
		proof(run)
		proofNominal(run)

		run(fixture.Exec("CREATE SEQUENCE public.td_exit_effect START 1", 0))
		for i, tail := range []string{"::text", ""} {
			typeDescribeValue(run, "pg_typeof(nextval('public.td_exit_effect')::text::pg_catalog.unknown"+tail+")::oid::bigint", "INT8", []int64{25, 705}[i])
			typeDescribeValue(run, "currval('public.td_exit_effect')", "INT8", int64(2*(i+1)))
		}
		// A stored volatile producer must also survive presentation exactly once.
		run(fixture.Exec("CREATE VIEW public.td_exit_effect_view AS SELECT pg_typeof(nextval('public.td_exit_effect')::text::pg_catalog.unknown::text) AS identity", 0))
		for i, pretty := range []bool{false, true} {
			name := fmt.Sprintf("public.td_exit_effect_%t", pretty)
			fixture.ViewCopySQL(t, p, b, name, "SELECT pg_get_viewdef('public.td_exit_effect_view'::regclass,$1::boolean) AS definition", pretty)
			typeDescribeBoth(run, fixture.Select("SELECT identity::oid::bigint AS v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{int64(25)}}))
			typeDescribeValue(run, "currval('public.td_exit_effect')", "INT8", int64(6+2*i))
		}
		for _, tail := range []string{"::text", ""} {
			typeDescribeFailure(run, "SELECT pg_typeof((1/0)::text::pg_catalog.unknown"+tail+")", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), false)
			d := fixture.ControlsReferenceDiagnostic("23514", `value for domain td_exit_text violates check constraint "td_exit_text_check"`)
			d.Schema, d.DataType, d.Constraint = "public", "td_exit_text", "td_exit_text_check"
			typeDescribeFailure(run, "SELECT pg_typeof('bad'::public.td_exit_text::pg_catalog.unknown"+tail+")", d, false)
		}
		for _, source := range []string{"1", "id"} {
			query := "SELECT pg_typeof(" + source + "::pg_catalog.unknown::text)"
			if source == "id" {
				query += " FROM public.td_exit_inputs"
			}
			d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type integer to unknown")
			d.Position = fixture.Position(18 + len(source))
			typeDescribeFailure(run, query, d, true)
		}
		typeDescribeValue(run, "pg_typeof(1)::oid::bigint", "INT8", int64(23))
		typeDescribeValue(run, "pg_typeof(NULL::integer)::oid::bigint", "INT8", int64(23))
		for _, selected := range []bool{false, true} {
			func() {
				if selected {
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if _, err := c.ExecContext(ctx, "DROP CAST IF EXISTS (xid AS real)"); err != nil {
							t.Error(err)
						}
					}()
					run(fixture.Exec("CREATE CAST (xid AS real) WITHOUT FUNCTION", 0))
					run(fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"method", "context"}, []string{"TEXT", "TEXT"}, [][]any{{"b", "e"}}))
				}
				for _, source := range []string{"'x'::text::pg_catalog.unknown", "NULL::text::pg_catalog.unknown"} {
					for _, tail := range []string{"::text", ""} {
						name := "unknown"
						if tail != "" {
							name = "text"
						}
						for _, prefix := range []string{"", "/* é */ "} {
							d := fixture.ControlsReferenceDiagnostic("42883", "function pg_typeof("+name+", integer) does not exist")
							d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
							d.Position = "8"
							if prefix != "" {
								d.Position = "16"
							}
							typeDescribeFailure(run, "SELECT "+prefix+"pg_typeof("+source+tail+",1)", d, true)
						}
					}
				}
			}()
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		oldID := typeDescribeOID(t, p.Ctx, c, "public.td_exit_text")
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		proof(run)
		proofNominal(run)
		target := fixture.NewFixture(t, false, true)
		targetConn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		targetRun := func(s fixture.Step) { fixture.RegtypeRunStep(t, target.Ctx, targetConn, target.MemoryRecord, s) }
		for i := range 8 {
			targetRun(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE td_exit_displacement_%d(v integer)", i), 0))
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		if newID := typeDescribeOID(t, target.Ctx, targetConn, "public.td_exit_text"); newID == oldID {
			t.Fatal("foreign replay did not displace domain identity", oldID)
		}
		proof(targetRun)
		proofNominal(targetRun)
	})
}
