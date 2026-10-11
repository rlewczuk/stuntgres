//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCatalogCore(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE public.t (id integer)", 0))
	type c struct {
		query, state, message, stage, pos string
		prepared                          bool
	}
	cases := []c{
		{"SELECT id FROM public.missing_table", "42P01", `relation "public.missing_table" does not exist`, "execute", "16", false},
		{"SELECT id FROM public.missing_table", "42P01", `relation "public.missing_table" does not exist`, "prepare", "16", true},
		{"INSERT INTO public.missing_table VALUES (1)", "42P01", `relation "public.missing_table" does not exist`, "execute", "13", false},
		{"INSERT INTO public.missing_table VALUES (1)", "42P01", `relation "public.missing_table" does not exist`, "prepare", "13", true},
		{"CREATE TABLE missing_schema.t (id integer)", "3F000", `schema "missing_schema" does not exist`, "execute", "14", false},
		{"CREATE TABLE missing_schema.t (id integer)", "3F000", `schema "missing_schema" does not exist`, "execute", "14", true},
		{"SELECT id FROM missing_schema.t", "42P01", `relation "missing_schema.t" does not exist`, "execute", "16", false},
		{"SELECT id FROM missing_schema.t", "42P01", `relation "missing_schema.t" does not exist`, "prepare", "16", true},
		{"INSERT INTO missing_schema.t VALUES (1)", "42P01", `relation "missing_schema.t" does not exist`, "execute", "13", false},
		{"INSERT INTO missing_schema.t VALUES (1)", "42P01", `relation "missing_schema.t" does not exist`, "prepare", "13", true},
		{"CREATE TABLE public.t (id integer)", "42P07", `relation "t" already exists`, "execute", "", false},
		{"CREATE TABLE public.t (id integer)", "42P07", `relation "t" already exists`, "execute", "", true},
		{"CREATE TABLE t (id integer)", "42P07", `relation "t" already exists`, "execute", "", false},
		{"CREATE TABLE t (id integer)", "42P07", `relation "t" already exists`, "execute", "", true},
		{"SELECT missing_column FROM public.t", "42703", `column "missing_column" does not exist`, "execute", "8", false},
		{"SELECT missing_column FROM public.t", "42703", `column "missing_column" does not exist`, "prepare", "8", true},
		{"INSERT INTO public.t (missing_column) VALUES (1)", "42703", `column "missing_column" of relation "t" does not exist`, "execute", "23", false},
		{"INSERT INTO public.t (missing_column) VALUES (1)", "42703", `column "missing_column" of relation "t" does not exist`, "prepare", "23", true},
	}
	for _, tc := range cases {
		native := fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: tc.state, Message: tc.message, Position: tc.pos,
		}
		step := fixture.Step{
			Query: tc.query, Prepared: tc.prepared, State: tc.state,
			Diagnostic: &native, ErrorStage: tc.stage,
		}
		p.Step(t, step)
	}
	p.Step(t, fixture.Exec("INSERT INTO public.t VALUES (1)", 1))
	p.Step(t, fixture.Select("SELECT id FROM public.t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.Step(t, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO public.t VALUES ($1)", 1, 1)))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT id FROM public.t WHERE id = $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, 1)))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT id FROM t WHERE id = $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, 1)))
}
