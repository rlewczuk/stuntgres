package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func routineDropBuiltinFamilyMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, family string) {
	t.Helper()
	body := "SELECT public." + family + "()"
	q := "SELECT rd_family_caller() AS v"
	native := fixture.ControlsReferenceDiagnostic("42883", "function public."+family+"() does not exist")
	native.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	native.Position = "8"
	native.Context = "SQL function \"rd_family_caller\" statement 1"
	pg := native
	pg.Position = ""
	pg.InternalPosition = "8"
	pg.InternalQuery = body
	pg.Context = "SQL function \"rd_family_caller\" during inlining"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, native)
		s.PostgresDiagnostic = &pg
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceRoutineDropBuiltinFamilyQuotedBody(t *testing.T) {
	for _, family := range []string{"num_nulls", "num_nonnulls"} {
		t.Run(family, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, _ := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					routineExec(t, p, b, c,
						"CREATE FUNCTION public."+family+"() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$",
						"CREATE FUNCTION public.rd_family_caller() RETURNS integer LANGUAGE SQL AS $$SELECT public."+family+"()$$")
					q := "SELECT rd_family_caller() AS v"
					held, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					routineValue(t, p, b, c, q, "v", "INT4", int64(7))
					routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
					routineExec(t, p, b, c, "DROP FUNCTION public."+family+"() RESTRICT")
					routineDropNotices(t, &notices)
					routineDropBuiltinFamilyMissing(t, p, b, c, family)
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal("builtin-family dangling caller replay", err)
						}
						routineDropBuiltinFamilyMissing(t, p, b, c, family)
						if p.Postgres != nil {
							pg, err := p.Postgres.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer pg.Close()
							if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
							if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
								t.Fatal("exact builtin-family emitted replay", err)
							}
							pb := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
							routineDropBuiltinFamilyMissing(t, p, pb, pg, family)
							routineExec(t, p, pb, pg, "CREATE FUNCTION public."+family+"() RETURNS integer LANGUAGE SQL AS $$SELECT 17$$", "SET check_function_bodies = true")
							routineValue(t, p, pb, pg, q, "v", "INT4", int64(17))
							if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
						}
					}
					routineExec(t, p, b, c, "CREATE FUNCTION public."+family+"() RETURNS integer LANGUAGE SQL AS $$SELECT 17$$")
					routineValue(t, p, b, c, q, "v", "INT4", int64(17))
					routineDropHeld(t, p, b, c, held, q, "INT4", int64(17))
				})
			}
		})
	}
}
