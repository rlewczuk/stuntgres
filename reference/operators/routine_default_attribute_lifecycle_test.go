package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDefaultAttributeLifecycle(t *testing.T) {
	for _, kind := range []string{"table", "type"} {
		t.Run(kind+"_rename", func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "rda_moved")
			parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				create, rename := "CREATE TABLE public.rda_life_t(a integer,b integer)", "ALTER TABLE"
				if kind == "type" {
					create, rename = "CREATE TYPE public.rda_life_t AS (a integer,b integer)", "ALTER TYPE"
				}
				userOperatorExec(t, p, b, c, create,
					"CREATE FUNCTION public.rda_life_f(x integer DEFAULT (NULL::public.rda_life_t).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'")
				var rel, typ, proc int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'public.rda_life_t'::regclass::oid::bigint,'public.rda_life_t'::regtype::oid::bigint,'public.rda_life_f(integer)'::regprocedure::oid::bigint").Scan(&rel, &typ, &proc); err != nil {
					t.Fatal(err)
				}
				column := rename + " public.rda_life_t RENAME COLUMN a TO renamed_a"
				if kind == "type" {
					column = rename + " public.rda_life_t RENAME ATTRIBUTE a TO renamed_a"
				}
				userOperatorExec(t, p, b, c, column, rename+" public.rda_life_t RENAME TO renamed_t", "CREATE SCHEMA rda_moved", rename+" public.renamed_t SET SCHEMA rda_moved", "CREATE TABLE public.rda_life_t(a integer,b integer)")
				q := "SELECT 'rda_moved.renamed_t'::regclass::oid::bigint AS r,'rda_moved.renamed_t'::regtype::oid::bigint AS t,'public.rda_life_f(integer)'::regprocedure::oid::bigint AS p"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"r", "t", "p"}, []string{"INT8", "INT8", "INT8"}, [][]any{{rel, typ, proc}}))
				defaultAttributeEdges(t, p, b, c, "public.rda_life_f(integer)", "rda_moved.renamed_t", []int64{1}, []int64{1})
				defaultAttributeEdges(t, p, b, c, "public.rda_life_f(integer)", "public.rda_life_t", nil, nil)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT attnum::integer AS a FROM pg_attribute WHERE attrelid='rda_moved.renamed_t'::regclass AND attname='renamed_a'", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
				defaultAttributeLabels(t, p, b, c, "public.rda_life_f(integer)", "rda_moved", "renamed_t", "renamed_a")
				defaultAttributeCalls(t, p, b, c, "public.rda_life_f")
			})
		})
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_drop_prepared_%t", kind, prepared), func(t *testing.T) {
				parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					create := "CREATE TABLE public.rda_drop_t(a integer,b integer)"
					if kind == "type" {
						create = "CREATE TYPE public.rda_drop_t AS (a integer,b integer)"
					}
					userOperatorExec(t, p, b, c, create, "CREATE TABLE public.rda_survivor(v integer)", "CREATE FUNCTION public.rda_drop_f(x integer DEFAULT (NULL::public.rda_drop_t).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.rda_survivor VALUES(41)", 1))
					if kind == "table" {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.rda_drop_t VALUES(7,8)", 1))
					}
					q := "DROP " + kind + " public.rda_drop_t RESTRICT"
					var err error
					if prepared {
						stmt, e := c.PrepareContext(p.Ctx, q)
						if e != nil {
							t.Fatal("DROP prepare unexpectedly rejected", e)
						}
						_, err = stmt.ExecContext(p.Ctx)
						stmt.Close()
					} else {
						_, err = c.ExecContext(p.Ctx, q)
					}
					got := fixture.ExtractDiagnostic(t, err)
					want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop "+kind+" rda_drop_t because other objects depend on it")
					// Complete diagnostic observed on pinned PostgreSQL18.6.
					want.Detail = "function rda_drop_f(integer) depends on " + kind + " rda_drop_t"
					want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					if got != want {
						t.Errorf("DROP diagnostic got=%#v want=%#v", got, want)
					}
					defaultAttributeCalls(t, p, b, c, "public.rda_drop_f")
					defaultAttributeEdges(t, p, b, c, "public.rda_drop_f(integer)", "public.rda_drop_t", []int64{1}, []int64{1})
					if kind == "table" {
						defaultAttributeStoredValue(t, p, b, c, "SELECT a AS v FROM public.rda_drop_t", "INT4", int64(7), true)
					}
					userOperatorExec(t, p, b, c, "DROP "+kind+" public.rda_drop_t CASCADE")
					defaultAttributeStoredValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rda_drop_f'", "INT8", int64(0), false)
					defaultAttributeStoredValue(t, p, b, c, "SELECT v FROM public.rda_survivor", "INT4", int64(41), true)
				})
			})
		}
	}
}
