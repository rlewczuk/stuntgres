package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// VACUUM's maintenance-only path admits granted peer TEMP namespaces without
// relaxing the ordinary data-access boundary.
func TestReferenceVacuumF7ForeignTempSearchPath(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			for _, phase := range []string{"denied_table", "allowed_table", "denied_namespace", "fallback"} {
				t.Run(phase, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"vacuum_path_actor"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							owner := vacuumConn(t, p, b)
							run := func(s fixture.Step) { vacuumRun(t, p, b, owner, s) }
							run(fixture.Exec("CREATE ROLE vacuum_path_actor", 0))
							run(fixture.Exec("GRANT USAGE ON SCHEMA public TO vacuum_path_actor", 0))
							run(fixture.Exec("CREATE TEMP TABLE path_foreign(id INT)", 0))
							run(fixture.Exec("INSERT INTO path_foreign VALUES(7)", 1))
							var ns string
							if err := owner.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='path_foreign'::regclass").Scan(&ns); err != nil {
								t.Fatal(err)
							}
							schema := `"` + strings.ReplaceAll(ns, `"`, `""`) + `"`
							qualified := schema + ".path_foreign"
							run(fixture.Exec("GRANT USAGE ON SCHEMA "+schema+" TO vacuum_path_actor", 0))
							defer func() {
								ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
								defer cancel()
								for _, q := range []string{"REVOKE USAGE ON SCHEMA " + schema + " FROM vacuum_path_actor", "DROP TABLE IF EXISTS path_foreign"} {
									if _, err := owner.ExecContext(ctx, q); err != nil {
										t.Error("TEMP path cleanup", err)
									}
								}
							}()
							if phase != "denied_table" {
								run(fixture.Exec("GRANT MAINTAIN ON "+qualified+" TO vacuum_path_actor", 0))
							}
							if phase == "denied_namespace" || phase == "fallback" {
								run(fixture.Exec("REVOKE USAGE ON SCHEMA "+schema+" FROM vacuum_path_actor", 0))
							}
							if phase == "fallback" {
								run(fixture.Exec("CREATE TABLE public.path_foreign(id INT)", 0))
								run(fixture.Exec("INSERT INTO public.path_foreign VALUES(11)", 1))
								run(fixture.Exec("GRANT MAINTAIN ON public.path_foreign TO vacuum_path_actor", 0))
							}
							var notices []fixture.Diagnostic
							_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
							fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("SET ROLE vacuum_path_actor", 0))
							fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("SET search_path="+schema+",public", 0))
							q := "VACUUM path_foreign"
							step := fixture.Exec(q, 0)
							if phase == "denied_namespace" {
								step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: `relation "path_foreign" does not exist`})
							}
							notices = nil
							vacuumPhaseExecute(t, p, worker, rec, held, step)
							var want []fixture.Diagnostic
							if phase == "denied_table" {
								want = []fixture.Diagnostic{vacuumPhaseWarning("01000", `permission denied to vacuum "path_foreign", skipping it`)}
							}
							vacuumPhaseNotices(t, notices, want)
							vacuumCount(t, p.Ctx, owner, qualified, 0)
							if phase == "fallback" {
								vacuumCount(t, p.Ctx, owner, "public.path_foreign", 1)
								vacuumRows(t, p, b, owner, "public.path_foreign", 11)
							}
							// A qualified actual namespace never uses search-path omission.
							notices = nil
							qualifiedStep := fixture.Exec("VACUUM "+qualified, 0)
							if phase == "denied_namespace" || phase == "fallback" {
								qualifiedStep = fixture.ImplicitError("VACUUM "+qualified, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + ns})
							}
							vacuumPhaseExecute(t, p, worker, rec, held, qualifiedStep)
							qualifiedWant := want
							if phase == "denied_namespace" || phase == "fallback" {
								qualifiedWant = nil
							}
							vacuumPhaseNotices(t, notices, qualifiedWant)
							if !b.PG && phase == "allowed_table" {
								denied := fixture.ImplicitError("SELECT id FROM "+qualified, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + ns, Position: fixture.Position(len("SELECT id FROM ") + 1)})
								denied.SelectRows = true
								fixture.RunStep(t, p.Ctx, worker, rec, denied)
							}
							fixture.RunStep(t, p.Ctx, worker, rec, fixture.Exec("RESET ROLE", 0))
							vacuumRows(t, p, b, owner, qualified, 7)
							vacuumProgressEmpty(t, p, b, worker)
						})
					}
				})
			}
		})
	}
}
