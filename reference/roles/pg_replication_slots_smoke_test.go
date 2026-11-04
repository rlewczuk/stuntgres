package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGReplicationSlotsFeatureSmoke(t *testing.T) {
	slotCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		peer, err := db.Conn(s.P.Ctx)
		if err != nil { s.T.Fatal(err) }
		defer peer.Close()
		run := func(e fixture.Executor, step fixture.Step) { fixture.RunStep(s.T, s.P.Ctx, e, s.Record, step) }
		exec := func(e fixture.Executor, q string) { run(e, fixture.Exec(q, 0)) }
		// Global ACL restoration must precede any revoke, including fatal exits.
		s.T.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"RESET ROLE", "GRANT SELECT ON pg_catalog.pg_replication_slots TO PUBLIC", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO PUBLIC"} {
				if _, err := s.C.ExecContext(ctx, q); err != nil { s.T.Error(err) }
			}
			if s.Backend.PG {
				for _, q := range []string{
					"UPDATE pg_catalog.pg_class SET relacl='{stuntgres=arwdDxtm/stuntgres,=r/stuntgres}'::aclitem[] WHERE oid='pg_catalog.pg_replication_slots'::regclass",
					"UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid=3781",
				} {
					if _, err := s.C.ExecContext(ctx, q); err != nil { s.T.Error(err) }
				}
			}
		})
		if s.Backend.PG { slotCleanup(s.T, db, "slots_smoke_physical", "slots_smoke_logical") }
		s.Exec("CREATE TABLE slots_smoke_sentinel(n integer)")
		s.Run(fixture.Exec("INSERT INTO slots_smoke_sentinel VALUES(7)", 1))
		slotInventoryEmpty(s, s.C)
		q := slotInventorySelect + " LIMIT 0"
		stmt, err := peer.PrepareContext(s.P.Ctx, q)
		if err != nil { s.T.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: q}
		readHeld := func() {
			step := fixture.Select(q, slotInventoryNames, slotInventoryTypes, nil)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { slotInventoryMetadata(t, rows, s.Backend.PG) }
			run(held, step)
		}
		old, err := peer.QueryContext(s.P.Ctx, q)
		if err != nil { s.T.Fatal(err) }
		slotInventoryMetadata(s.T, old, s.Backend.PG)
		descriptors, err := old.ColumnTypes()
		if err != nil { s.T.Fatal(err) }
		if err := old.Close(); err != nil { s.T.Fatal(err) }
		exec(peer, "SET ROLE rsl_actor")
		readHeld()
		slotCreate(s, "slots_smoke_physical", false, false, false)
		slotInventoryPhysical(s, peer, "slots_smoke_physical", "", false, s.C)
		if s.Backend.PG { slotDrop(s, "slots_smoke_physical") }
		slotInventoryAbsent(s, peer, "slots_smoke_physical")
		start := slotCreate(s, "slots_smoke_logical", true, false, false)
		slotInventoryLogical(s, peer, "slots_smoke_logical", "test_decoding", start, false, false, false, s.C)
		if s.Backend.PG { slotDrop(s, "slots_smoke_logical") }
		slotInventoryEmpty(s, peer)
		deny := func(object string) {
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + object})
			step.SelectRows, step.ErrorStage = true, "execute"
			run(held, step)
		}
		exec(s.C, "REVOKE SELECT ON pg_catalog.pg_replication_slots FROM PUBLIC")
		deny("view pg_replication_slots")
		exec(s.C, "GRANT SELECT ON pg_catalog.pg_replication_slots TO PUBLIC")
		exec(s.C, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM PUBLIC")
		deny("function pg_get_replication_slots")
		exec(s.C, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO PUBLIC")
		readHeld()
		exec(peer, "RESET ROLE")
		if !s.Backend.PG {
			s.Exec("CREATE VIEW slots_smoke_view AS SELECT slot_name AS slot,restart_lsn AS retained FROM pg_catalog.pg_replication_slots")
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil { s.T.Fatal(err) }
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil { s.T.Fatal(err) }
			slotInventoryEmpty(s, peer)
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil { s.T.Fatal(err) }
			predefinedSelect(s, "SELECT * FROM slots_smoke_view", []string{"slot", "retained"}, []string{"NAME", "PG_LSN"}, nil)
			readHeld()
		}
		for i, descriptor := range descriptors {
			if descriptor.Name() != slotInventoryNames[i] || descriptor.DatabaseTypeName() != slotInventoryTypes[i] { s.T.Fatal("held descriptor changed", i) }
		}
		predefinedSelect(s, "SELECT n FROM slots_smoke_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}
