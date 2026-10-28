package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTriggerFeatureSmoke(t *testing.T) {
	t.Run("DefinitionBehavior", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				pgTriggerSmokeSetup(t, p, b)
				oid := pgTriggerOID(t, p, b, "pgt_smoke", "pgt_smoke_trigger")
				pgTriggerLive(t, p, b, b.DB, "pgt_smoke", pgTriggerSmokeRow("pgt_smoke_trigger"))
				pgTriggerBoth(t, p, b, fixture.Select("SELECT c.relname,p.proname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.oid=$1::oid", []string{"relname", "proname"}, []string{"NAME", "NAME"}, [][]any{{"pgt_smoke", "pgt_smoke_fn"}}, oid))
				fixture.PgDescriptionRows(t, p, b, fixture.Select("INSERT INTO pgt_smoke VALUES(1,5) RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				stmt, err := b.DB.PrepareContext(p.Ctx, pgTriggerLiveQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgTriggerLiveQuery}
				pgTriggerExec(t, p, b, b.DB, "ALTER TRIGGER pgt_smoke_trigger ON pgt_smoke RENAME TO renamed")
				if pgTriggerOID(t, p, b, "pgt_smoke", "renamed") != oid {
					t.Fatal("rename changed identity")
				}
				fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(pgTriggerLiveQuery, pgTriggerLiveColumns, pgTriggerLiveTypes, pgTriggerSmokeRow("renamed"), "pgt_smoke"))
				pgTriggerBoth(t, p, b, fixture.Select("SELECT count(*) AS n,sum(v) AS total FROM pgt_smoke", []string{"n", "total"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(10)}}))
				pgTriggerExec(t, p, b, b.DB, "DROP TRIGGER renamed ON pgt_smoke")
				fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(pgTriggerLiveQuery, pgTriggerLiveColumns, pgTriggerLiveTypes, [][]any{}, "pgt_smoke"))
				fixture.PgDescriptionRows(t, p, b, fixture.Select("INSERT INTO pgt_smoke VALUES(2,5) RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
			})
		}
	})
	t.Run("ActualControls", pgTriggerControlsScenario)
}
