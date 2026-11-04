package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGHBAFileRulesFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hba_smoke_actor"})
	p, f := fixture.NewHBAFileScenario(t, hbaCanonicalSet())
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			want := hbaCanonicalRows(f, b)
			hbaRead(t, p, b, b.DB, hbaSelect, want, false)
			stmt, err := b.DB.PrepareContext(p.Ctx, hbaSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: hbaSelect}
			old, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all trust\n", "trust"))
			next := [][]any{hbaLocal(1, f.Path(b, ""), 1, "trust")}
			hbaRead(t, p, b, held, hbaSelect, next, true)
			hbaSQLError(t, p, b, b.DB, "SELECT missing FROM pg_catalog.pg_hba_file_rules", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}, false, "execute")
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
			run(admin, "CREATE ROLE hba_smoke_actor")
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				if _, err := actor.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
				for _, q := range []string{"REVOKE SELECT ON pg_catalog.pg_hba_file_rules FROM hba_smoke_actor", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() FROM hba_smoke_actor"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			run(actor, "SET SESSION AUTHORIZATION hba_smoke_actor")
			f.Install(t, p.Ctx, b, fixture.HBARuleSet{})
			hbaDenied(t, p, b, actor, hbaSelect, false)
			run(admin, "GRANT SELECT ON pg_catalog.pg_hba_file_rules TO hba_smoke_actor")
			hbaDenied(t, p, b, actor, hbaSelect, true)
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_hba_file_rules() TO hba_smoke_actor")
			hbaRead(t, p, b, actor, hbaSelect, nil, true)
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all trust\n", "trust"))
			hbaRead(t, p, b, actor, hbaSelect, next, true)
			run(actor, "RESET SESSION AUTHORIZATION")
			// Bag comparison: the mixed include result has no ORDER BY contract.
			hbaMetadata(t, old, b.PG)
			var retained [][]any
			for old.Next() {
				values, targets := make([]any, 11), make([]any, 11)
				for i := range values {
					targets[i] = &values[i]
				}
				if err := old.Scan(targets...); err != nil {
					t.Fatal(err)
				}
				for i, v := range values {
					fixture.AssertRawValue(t, b.PG, hbaTypes[i], v)
					values[i] = fixture.Normalize(v)
				}
				retained = append(retained, values)
			}
			if err := old.Err(); err != nil {
				t.Fatal(err)
			}
			if _, matches := fixture.CompareRows(retained, want, true); !matches {
				t.Fatal(retained, want)
			}
			if !b.PG {
				hbaMutableLifecycle(t, p)
			}
		})
	}
}
