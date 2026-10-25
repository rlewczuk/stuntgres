package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequenceFeatureSmoke(t *testing.T) {
	t.Run("DefinitionLifecycle", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgs_smoke_reader"})
		fixture.SchemaReferenceCleanup(t, "pgs_smoke_private")
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				for _, q := range []string{"CREATE ROLE pgs_smoke_reader", "CREATE SCHEMA pgs_smoke_private", "CREATE SEQUENCE pgs_smoke_private.pgs_smoke AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE"} {
					pgSequenceExec(t, p, b, c, q)
				}
				oid := fixture.PGDescriptionRelationID(t, p, b, "pgs_smoke")
				row := pgSequenceCustom(oid)
				pgSequenceCheck(t, p, b, c, oid, row)
				for _, tc := range []struct {
					q string
					n int64
				}{{"SELECT nextval('pgs_smoke_private.pgs_smoke')", 15}, {"SELECT setval('pgs_smoke_private.pgs_smoke',42,false)", 42}} {
					fixture.SequenceProbeValue(t, p.Ctx, c, tc.q, tc.n)
					pgSequenceCheck(t, p, b, c, oid, row)
				}
				pgSequenceExec(t, p, b, c, "ALTER SEQUENCE pgs_smoke_private.pgs_smoke RESTART WITH 35")
				pgSequenceCheck(t, p, b, c, oid, row)
				stmt, err := c.PrepareContext(p.Ctx, pgSequenceProbe)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: pgSequenceProbe}
				old, err := c.QueryContext(p.Ctx, pgSequenceProbe, oid)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgs_smoke_private.pgs_smoke START 21 AS BIGINT")
				pgSequenceRetained(t, old, b.PG, row)
				row[1], row[2] = int64(20), int64(21)
				fixture.PgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{row}, oid))
				pgSequenceExec(t, p, b, c, "BEGIN")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				pgSequenceExec(t, p, b, c, "ALTER SEQUENCE pgs_smoke_private.pgs_smoke CACHE 4")
				private := append([]any(nil), row...)
				private[6] = int64(4)
				pgSequenceCheck(t, p, b, c, oid, private)
				pgSequenceExec(t, p, b, c, "ROLLBACK")
				pgSequenceCheck(t, p, b, c, oid, row)
				pgSequenceExec(t, p, b, peer, "SET ROLE pgs_smoke_reader")
				defer peer.ExecContext(p.Ctx, "RESET ROLE")
				pgSequenceAssertNoAccess(t, p, b, peer, "pgs_smoke_private", "pgs_smoke", oid)
				pgSequenceCheck(t, p, b, peer, oid, row)
				pgSequenceExec(t, p, b, peer, "RESET ROLE")
				pgSequenceExec(t, p, b, peer, "DROP SEQUENCE pgs_smoke_private.pgs_smoke")
				fixture.PgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{}, oid))
			})
		}
	})
	t.Run("ActualDumpReplay", pgSequenceControlsScenario)
}
