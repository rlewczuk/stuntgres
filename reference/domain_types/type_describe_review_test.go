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

// Review1 expectations derive independently from REL_18_6 and were confirmed
// against the pinned oracle before production remedies; native output is never a golden.
func TestReferenceTypeDescribeTempVisibility(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "DROP TABLE IF EXISTS pg_temp.td_temp_type,pg_temp.text"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("CREATE TEMP TABLE td_temp_type(v integer)", 0))
		typeDescribeValue(run, "format_type('pg_temp.td_temp_type'::regtype::oid,NULL)", "TEXT", "td_temp_type")
		run(fixture.Exec("CREATE TEMP TABLE text(v integer)", 0))
		query := "SELECT pg_catalog.format_type('pg_temp.td_temp_type'::regtype::oid,NULL) AS temporary,pg_catalog.format_type(25,NULL) AS builtin"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, path := range []struct{ sql, builtin string }{
			{"public", "pg_catalog.text"},
			{"pg_catalog,pg_temp", "text"},
			{"pg_temp,pg_catalog", "pg_catalog.text"},
			{"public", "pg_catalog.text"},
		} {
			run(fixture.Exec("SET search_path TO "+path.sql, 0))
			s := fixture.Select(query, []string{"temporary", "builtin"}, []string{"TEXT", "TEXT"}, [][]any{{"td_temp_type", path.builtin}})
			typeDescribeBoth(run, s)
			fixture.RegtypeRunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Native eager rows own the old spelling even after session path changes.
		// lib/pq's streamed cursor is instead fully consumed before another command.
		if !b.PG {
			rows, err := c.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			run(fixture.Exec("SET search_path TO pg_catalog,pg_temp", 0))
			fixture.RegtypeAssertRowsWithMetadata(t, rows, [][]any{{"td_temp_type", "pg_catalog.text"}}, false, func(t *testing.T, rows *sql.Rows) {
				fixture.RegtypeFamilyMetadata(t, rows, false, []string{"TEXT", "TEXT"})
			})
		}
	})
}

func typeDescribeComposedExpressions() []string {
	expressions := []string{}
	for _, literal := range []string{"'x'", "NULL"} {
		expressions = append(expressions, literal+"::pg_catalog.unknown::pg_catalog.unknown")
		for _, target := range []string{`"any"`, "anyelement", "anynonarray", "anycompatible", "anycompatiblenonarray"} {
			expressions = append(expressions, literal+"::"+target+"::pg_catalog.unknown", literal+"::pg_catalog.unknown::"+target+"::pg_catalog.unknown")
		}
		for _, source := range []string{"text", "varchar", "bpchar", "name"} {
			expressions = append(expressions, literal+"::"+source+"::pg_catalog.unknown::pg_catalog.unknown")
		}
	}
	return expressions
}

func TestReferenceTypeDescribeComposedUnknown(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		expressions := typeDescribeComposedExpressions()
		columns, types, values, selections := []string{}, []string{}, []any{}, []string{}
		for i, expression := range expressions {
			name := fmt.Sprintf("u%d", i)
			selections = append(selections, "pg_typeof("+expression+") AS "+name)
			columns, types, values = append(columns, name), append(types, "REGTYPE"), append(values, "unknown")
			typeDescribeValue(run, "pg_typeof("+expression+")::oid::bigint", "INT8", int64(705))
		}
		for _, tc := range []struct {
			expression string
			oid        int64
		}{{"'x'::text", 25}, {"NULL::text", 25}, {"1::integer", 23}, {"NULL::integer", 23}} {
			typeDescribeValue(run, "pg_typeof("+tc.expression+")::oid::bigint", "INT8", tc.oid)
		}
		run(fixture.Exec("CREATE DOMAIN public.td_composed_text AS text", 0))
		run(fixture.Exec("CREATE TABLE public.td_composed_inputs(id integer,t text,d public.td_composed_text)", 0))
		run(fixture.Exec("INSERT INTO public.td_composed_inputs VALUES(1,'x','x'),(2,NULL,NULL)", 2))
		run(fixture.Exec("CREATE VIEW public.td_composed_sources AS SELECT id,pg_typeof(t::pg_catalog.unknown::pg_catalog.unknown) AS textual,pg_typeof(d::pg_catalog.unknown::pg_catalog.unknown) AS nominal,pg_typeof(t) AS concrete FROM public.td_composed_inputs", 0))
		run(fixture.Exec("CREATE VIEW public.td_composed AS SELECT "+strings.Join(selections, ",")+",pg_typeof('x'::text) AS concrete,pg_typeof(1) AS integral", 0))
		columns, types, values = append(columns, "concrete", "integral"), append(types, "REGTYPE", "REGTYPE"), append(values, "text", "integer")
		// Both stored AST presentation routes must recreate the same logical input,
		// whether they elide a proven no-op or retain an executable StringIO cast.
		for _, pretty := range []bool{false, true} {
			suffix := fmt.Sprintf("%t", pretty)
			fixture.ViewCopySQL(t, p, b, "public.td_composed_"+suffix, "SELECT pg_get_viewdef('public.td_composed'::regclass,$1::boolean) AS definition", pretty)
			fixture.ViewCopySQL(t, p, b, "public.td_composed_sources_"+suffix, "SELECT pg_get_viewdef('public.td_composed_sources'::regclass,$1::boolean) AS definition", pretty)
		}
		proof := func(run func(fixture.Step)) {
			for _, suffix := range []string{"", "_false", "_true"} {
				relation := "public.td_composed" + suffix
				typeDescribeBoth(run, fixture.Select("SELECT * FROM "+relation, columns, types, [][]any{values}))
				for i := range expressions {
					typeDescribeBoth(run, fixture.Select(fmt.Sprintf("SELECT u%d::oid::bigint AS v FROM %s", i, relation), []string{"v"}, []string{"INT8"}, [][]any{{int64(705)}}))
				}
				typeDescribeBoth(run, fixture.Select("SELECT id,textual,nominal,concrete FROM public.td_composed_sources"+suffix+" ORDER BY id", []string{"id", "textual", "nominal", "concrete"}, []string{"INT4", "REGTYPE", "REGTYPE", "REGTYPE"}, [][]any{{int64(1), "unknown", "unknown", "text"}, {int64(2), "unknown", "unknown", "text"}}))
				typeDescribeBoth(run, fixture.Select("SELECT id,textual::oid::bigint AS a,nominal::oid::bigint AS b FROM public.td_composed_sources"+suffix+" ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(705), int64(705)}, {int64(2), int64(705), int64(705)}}))
			}
		}
		proof(run)
		query := "SELECT pg_typeof($1::text::pg_catalog.unknown::pg_catalog.unknown)::oid::bigint AS logical,pg_typeof($1::text)::oid::bigint AS concrete"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, value := range []any{"first", nil, "second"} {
			s := fixture.Select(query, []string{"logical", "concrete"}, []string{"INT8", "INT8"}, [][]any{{int64(705), int64(25)}}, value)
			typeDescribeBoth(run, s)
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
		}
		run(fixture.Exec("CREATE SEQUENCE public.td_composed_effect START 1", 0))
		typeDescribeValue(run, "pg_typeof(nextval('public.td_composed_effect')::text::pg_catalog.unknown::pg_catalog.unknown)::oid::bigint", "INT8", int64(705))
		typeDescribeValue(run, "currval('public.td_composed_effect')", "INT8", int64(2))
		typeDescribeFailure(run, "SELECT pg_typeof((1/0)::text::pg_catalog.unknown::pg_catalog.unknown)", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), false)
		for _, source := range []string{"1", "id"} {
			query := "SELECT pg_typeof(" + source + "::pg_catalog.unknown::pg_catalog.unknown)"
			if source == "id" {
				query += " FROM public.td_composed_inputs"
			}
			d := fixture.ControlsReferenceDiagnostic("42846", "cannot cast type integer to unknown")
			d.Position = fixture.Position(18 + len(source))
			typeDescribeFailure(run, query, d, true)
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		oldID := typeDescribeOID(t, p.Ctx, c, "public.td_composed_text")
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		proof(run)
		target := fixture.NewFixture(t, false, true)
		targetConn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		targetRun := func(s fixture.Step) { fixture.RegtypeRunStep(t, target.Ctx, targetConn, target.MemoryRecord, s) }
		for i := range 8 {
			targetRun(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE td_composed_displacement_%d(v integer)", i), 0))
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		if newID := typeDescribeOID(t, target.Ctx, targetConn, "public.td_composed_text"); newID == oldID {
			t.Fatal("foreign replay did not displace domain identity", oldID)
		}
		proof(targetRun)
	})
}

func TestReferenceTypeDescribeUnknownArity(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprintf("has_casts_%t", selected), func(t *testing.T) {
			typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
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
				expressions := append([]string{"'x'", "NULL", "'x'::unknown", "NULL::unknown", "'x'::text::unknown", "NULL::text::unknown"}, typeDescribeComposedExpressions()...)
				for _, literal := range []string{"'x'", "NULL"} {
					for _, target := range []string{`"any"`, "anyelement", "anynonarray", "anycompatible", "anycompatiblenonarray"} {
						expressions = append(expressions, literal+"::"+target)
					}
				}
				for _, expression := range expressions {
					d := fixture.ControlsReferenceDiagnostic("42883", "function pg_typeof(unknown, integer) does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = "8"
					typeDescribeFailure(run, "SELECT pg_typeof("+expression+",1)", d, true)
				}
				for _, tc := range []struct{ expression, name string }{{"'x'::text", "text"}, {"NULL::text", "text"}, {"1", "integer"}} {
					d := fixture.ControlsReferenceDiagnostic("42883", "function pg_typeof("+tc.name+", integer) does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = "8"
					typeDescribeFailure(run, "SELECT pg_typeof("+tc.expression+",1)", d, true)
				}
				d := fixture.ControlsReferenceDiagnostic("42883", "function pg_typeof(unknown, integer) does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				d.Position = "16"
				typeDescribeFailure(run, "SELECT /* é */ pg_typeof('x'::text::unknown::unknown,1)", d, true)
			})
		})
	}
}

func TestReferenceTypeDescribeCopyWhere(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE public.td_copy(v integer)", 0))
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		transport, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer transport.Close()
		for _, tc := range []struct {
			predicate string
			count     int64
			rows      [][]any
		}{
			{"format_type(pg_typeof(v),NULL)='integer'", 3, [][]any{{int64(1)}, {int64(2)}, {nil}}},
			{"format_type(pg_typeof(v),NULL)='integer' AND v>1", 1, [][]any{{int64(2)}}},
		} {
			result, err := fixture.CopyFrom(p.Ctx, transport, b.PG, "COPY public.td_copy(v) FROM STDIN WHERE "+tc.predicate, strings.NewReader("1\n\\N\n2\n"))
			want := stuntgres.CopyResult{Count: tc.count, CommandTag: fmt.Sprintf("COPY %d", tc.count)}
			if err != nil || result != want {
				t.Fatalf("COPY predicate result=%+v want=%+v error=%v", result, want, err)
			}
			typeDescribeBoth(run, fixture.Select("SELECT v FROM public.td_copy ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, tc.rows))
			run(fixture.Exec("DELETE FROM public.td_copy", tc.count))
		}
		run(fixture.Exec("INSERT INTO public.td_copy VALUES(9)", 1))
		result, err := fixture.CopyFrom(p.Ctx, transport, b.PG, "COPY public.td_copy(v) FROM STDIN WHERE format_type(pg_typeof(v),(10/(v-2))::integer)='integer'", strings.NewReader("1\n2\n"))
		if result != (stuntgres.CopyResult{}) {
			t.Fatal("failed COPY published result", result)
		}
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		d.Context = "COPY td_copy, line 2: \"2\""
		fixture.AssertDiagnostic(t, err, &d)
		typeDescribeBoth(run, fixture.Select("SELECT v FROM public.td_copy ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
	})
}
