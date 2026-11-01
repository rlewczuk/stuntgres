package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGMatviewsDatabaseBoundary(t *testing.T) {
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			databases = append(databases, "pgmv_other_database")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DATABASE pgmv_other_database", 0))
			other, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "pgmv_other_database", "")
			fixture.RunStep(t, p.Ctx, other, recorder, fixture.Exec("CREATE MATERIALIZED VIEW public.pgmv_database_only AS SELECT 9 AS n", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE MATERIALIZED VIEW public.pgmv_main_only AS SELECT 9 AS n", 1))
			for _, probe := range []struct {
				db fixture.Executor
				recorder *fixture.Recorder
				name string
				present bool
			}{{b.DB, b.Recorder, "pgmv_database_only", false}, {other, recorder, "pgmv_database_only", true}, {b.DB, b.Recorder, "pgmv_main_only", true}, {other, recorder, "pgmv_main_only", false}} {
				var rows [][]any
				if probe.present {
					rows = [][]any{fixture.PGMatviewRow("public", probe.name, fixture.PGTableViewsOwner(b), false, true, " SELECT 9 AS n;")}
				}
				s := fixture.PGTableViewSelect(b, pgMatviewProbe, fixture.PGMatviewColumns, fixture.PGMatviewTypes, rows, "public", probe.name)
				fixture.RunStep(t, p.Ctx, probe.db, probe.recorder, s)
				fixture.RunStep(t, p.Ctx, probe.db, probe.recorder, fixture.Prepared(s))
			}
		})
	}
}
