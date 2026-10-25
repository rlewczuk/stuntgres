package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecyclePrepared(t *testing.T) {
	for _, tc := range []struct{ name, create, input, output string }{
		{"enum", "CREATE TYPE tl_src.t AS ENUM ('z','a')", "z", "z"},
		{"domain", "CREATE DOMAIN tl_src.t AS integer", "7", "7"},
		{"composite", "CREATE TYPE tl_src.t AS(n integer,note text)", "(7,x)", "(7,x)"},
		{"cube", "CREATE EXTENSION cube WITH SCHEMA tl_src", "(1,2)", "(1, 2)"},
		{"query_int", "CREATE EXTENSION intarray WITH SCHEMA tl_src", "1", "1"},
		{"citext", "CREATE EXTENSION citext WITH SCHEMA tl_src", "MiXeD", "MiXeD"},
		{"earth", "CREATE EXTENSION earthdistance WITH SCHEMA tl_src", "(6378168,0,0)", "(6378168, 0, 0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "SET search_path TO tl_src,public")
				if tc.name == "earth" {
					domainIdentityDDL(t, p, c, r, "CREATE EXTENSION cube WITH SCHEMA tl_src")
				}
				domainIdentityDDL(t, p, c, r, tc.create)
				name := "t"
				if tc.name == "cube" || tc.name == "query_int" || tc.name == "citext" || tc.name == "earth" {
					name = tc.name
				}
				domainIdentityDDL(t, p, c, r, "CREATE TABLE public.tl_prepared(id integer,v tl_src."+name+")")
				insertQ := "INSERT INTO public.tl_prepared VALUES($1,$2)"
				insert := typeLifecycleHeld(t, p, c, insertQ)
				selectQ := "SELECT id,v::text AS v FROM public.tl_prepared ORDER BY id"
				selectStmt := typeLifecycleHeld(t, p, c, selectQ)
				castQ := "SELECT ($1::tl_src." + name + ")::text AS v"
				cast := typeLifecycleHeld(t, p, c, castQ)
				fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insertQ, 1, int64(1), tc.input))
				fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insertQ, 1, int64(2), nil))
				fixture.RunStep(t, p.Ctx, cast, r, fixture.Select(castQ, []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, tc.input))
				oid := domainAlterOID(t, p, c, "SELECT 'tl_src."+name+"'::regtype::oid")
				domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src."+name+" RENAME TO moved", "ALTER TYPE tl_src.moved SET SCHEMA tl_dst")
				domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.moved'::regtype::oid", oid)
				fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insertQ, 1, int64(3), tc.input))
				fixture.RunStep(t, p.Ctx, selectStmt, r, fixture.Select(selectQ, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), tc.output}, {int64(2), nil}, {int64(3), tc.output}}))
				// Unchanged logical casts retain type identity across rename/move.
				fixture.RunStep(t, p.Ctx, cast, r, fixture.Select(castQ, []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, tc.input))
				fixture.RunStep(t, p.Ctx, cast, r, fixture.Select(castQ, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil))
				// A real effective-path change retires captures and reanalyzes the
				// original SQL, with original character position, not the new name.
				domainIdentityDDL(t, p, c, r, "SET search_path TO tl_dst,public")
				want := fixture.ControlsReferenceDiagnostic("42704", `type "tl_src.`+name+`" does not exist`)
				want.Position = "13"
				s := fixture.ErrorDiagnostic(castQ, want, tc.input)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, cast, r, s)
				domainIdentityDDL(t, p, c, r, "SET search_path TO tl_src,public")
				fixture.RunStep(t, p.Ctx, cast, r, s)
				run(fixture.Prepared(fixture.Select("SELECT ($1::tl_dst.moved)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, tc.input)))
				// A new source lookup must not resurrect an obsolete spelling.
				fresh := fixture.ErrorDiagnostic(castQ, want, tc.input)
				fresh.SelectRows = true
				fresh.Prepared = true
				fresh.ErrorStage = "prepare"
				run(fresh)
				if tc.name == "enum" {
					bad := fixture.ControlsReferenceDiagnostic("22P02", `invalid input value for enum tl_dst.moved: "bad"`)
					bad.Position = "8"
					run(fixture.ErrorDiagnostic("SELECT 'bad'::tl_dst.moved", bad))
					bad.Position = ""
					bad.Context = "unnamed portal parameter $1 = '...'"
					s := fixture.Prepared(fixture.ErrorDiagnostic("SELECT $1::tl_dst.moved", bad, "bad"))
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
				}
				// Native eager rows own the old provider graph even after removal.
				if !b.PG {
					rows, err := c.QueryContext(p.Ctx, "SELECT v::text AS v FROM public.tl_prepared WHERE id=1")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					if tc.name == "enum" || tc.name == "domain" || tc.name == "composite" {
						domainIdentityDDL(t, p, c, r, "DROP TYPE tl_dst.moved CASCADE")
					} else {
						ext := tc.name
						if ext == "query_int" {
							ext = "intarray"
						}
						if ext == "earth" {
							ext = "earthdistance"
						}
						domainIdentityDDL(t, p, c, r, "DROP EXTENSION "+ext+" CASCADE")
					}
					if len(columns) != 1 || columns[0] != "v" || !rows.Next() {
						t.Fatal("retained rows/columns lost")
					}
					var v string
					if err := rows.Scan(&v); err != nil {
						t.Fatal(err)
					}
					if v != tc.output {
						t.Fatalf("retained=%q", v)
					}
					if rows.Next() {
						t.Fatal("extra retained row")
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
	t.Run("drop_recreate_parameter_and_result_identity", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			domainIdentityDDL(t, p, c, r, "CREATE DOMAIN public.tl_d AS integer")
			q := "SELECT ($1::public.tl_d[])::text AS v"
			held := typeLifecycleHeld(t, p, c, q)
			s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"{7,NULL}"}}, "{7,NULL}")
			fixture.RunStep(t, p.Ctx, held, r, s)
			oid := domainAlterOID(t, p, c, "SELECT 'public.tl_d'::regtype::oid")
			array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='public.tl_d'::regtype::oid")
			domainIdentityDDL(t, p, c, r, "DROP TYPE public.tl_d", "CREATE DOMAIN public.tl_d AS integer")
			if got := domainAlterOID(t, p, c, "SELECT 'public.tl_d'::regtype::oid"); got == oid {
				t.Fatal("recreation reused logical identity")
			}
			want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type")
			if b.PG {
				want = fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for type %d", array))
				want.Context = "unnamed portal parameter $1"
			}
			failed := fixture.ErrorDiagnostic(q, want, "{8}")
			failed.SelectRows = true
			failed.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, r, failed)
		})
	})
	for _, tc := range []struct{ name, create, value string }{
		{"enum", "CREATE TYPE public.tl_result AS ENUM ('z')", "z"},
		{"composite", "CREATE TYPE public.tl_result AS(n integer,note text)", "(7,x)"},
	} {
		t.Run(tc.name+"_result_recreation", func(t *testing.T) {
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				domainIdentityDDL(t, p, c, r, tc.create, "CREATE TABLE public.tl_result_holder(keep integer,v public.tl_result)")
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.tl_result_holder VALUES(11,$1)", 1, tc.value))
				q := "SELECT v FROM public.tl_result_holder"
				held := typeLifecycleHeld(t, p, c, q)
				initial := fixture.Select(q, []string{"v"}, []string{""}, [][]any{{tc.value}})
				initial.CheckRaw = func(t *testing.T, column, row int, v any) bool {
					if b.PG {
						if _, ok := v.([]byte); !ok {
							t.Fatalf("raw custom cell=%T, want []byte", v)
						}
					} else if _, ok := v.(string); !ok {
						t.Fatalf("raw custom cell=%T, want string", v)
					}
					return true
				}
				fixture.RunStep(t, p.Ctx, held, r, initial)
				domainIdentityDDL(t, p, c, r, "DROP TYPE public.tl_result CASCADE", tc.create, "ALTER TABLE public.tl_result_holder ADD COLUMN v public.tl_result")
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, r, s)
				// The failed old consumer must not destroy retained holder data.
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT keep,v::text AS v FROM public.tl_result_holder", []string{"keep", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), nil}}))
			})
		})
	}
}
