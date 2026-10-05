//go:build reference

package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceCatalogCore(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE public.t (id integer)", 0))
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
		native := referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: tc.state, Message: tc.message,
		}
		// The existing INSERT target binder owns this occurrence position.
		// Current missing-relation/SELECT/CREATE producers have no native cursor.
		if strings.HasPrefix(tc.query, "INSERT INTO public.t (missing_column)") {
			native.Position = tc.pos
		}
		postgres := native
		postgres.Position = tc.pos
		step := referenceStep{
			query: tc.query, prepared: tc.prepared, state: tc.state,
			diagnostic: &native, postgresDiagnostic: &postgres, errorStage: tc.stage,
		}
		p.step(t, step)
	}
	p.step(t, referenceExec("INSERT INTO public.t VALUES (1)", 1))
	p.step(t, referenceSelect("SELECT id FROM public.t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.step(t, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.step(t, referencePrepared(referenceExec("INSERT INTO public.t VALUES ($1)", 1, 1)))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM public.t WHERE id = $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, 1)))
	p.step(t, referencePrepared(referenceSelect("SELECT id FROM t WHERE id = $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}, 1)))
}
