package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropSlots(t *testing.T) {
	// REL_18_6 dropdb counts active logical slots before FORCE signals anyone;
	// ReplicationSlotsDropDBSlots retires only this database's inactive slots.
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b = "dd_slots_a", "dd_slots_b"
			const retired, peer, activePeer = "dd_slots_retired", "dd_slots_peer", "dd_slots_peer_active"
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					slotCleanup(t, backend.DB, retired, peer)
					admin, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					run := func(s fixture.Step) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, admin, backend.Recorder, s)
					}
					for _, name := range names {
						run(fixture.Exec("CREATE DATABASE "+name, 0))
					}
					held := make(map[string]fixture.HeldStatement)
					drop := func(q string, s fixture.Step) {
						t.Helper()
						var executor fixture.Executor = admin
						if mode == "held" {
							h, ok := held[q]
							if !ok {
								stmt, err := admin.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								t.Cleanup(func() { stmt.Close() })
								h = fixture.HeldStatement{Executor: admin, Statement: stmt, SQL: q}
								held[q] = h
							}
							executor = h
						} else if mode == "fresh" {
							s = fixture.Prepared(s)
						}
						if s.State != "" {
							s.ErrorStage = "execute"
						}
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
					}
					createSlot := func(owner fixture.Executor, record *fixture.Recorder, name string, temporary bool) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, owner, record, fixture.Select("SELECT slot_name,lsn IS NOT NULL AS positioned FROM pg_create_logical_replication_slot($1,'test_decoding',$2)", []string{"slot_name", "positioned"}, []string{"NAME", "BOOL"}, [][]any{{name, true}}, name, temporary))
					}
					bDB, bRaw, bRecord := fixture.DatabaseScenarioPool(t, p, backend.PG, b, "")
					bDB.SetMaxIdleConns(0)
					bOwner, err := bDB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { bOwner.Close() })
					createSlot(bOwner, bRecord, peer, false)
					createSlot(bOwner, bRecord, activePeer, true)
					var peerPID int64
					if err := bOwner.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&peerPID); err != nil {
						t.Fatal(err)
					}
					peerHealthy := func() {
						t.Helper()
						run(fixture.Select("SELECT slot_name,plugin,slot_type,database,temporary,active,active_pid FROM pg_replication_slots WHERE slot_name IN ($1,$2) ORDER BY slot_name", []string{"slot_name", "plugin", "slot_type", "database", "temporary", "active", "active_pid"}, []string{"NAME", "NAME", "TEXT", "NAME", "BOOL", "BOOL", "INT4"}, [][]any{{peer, "test_decoding", "logical", b, false, false, nil}, {activePeer, "test_decoding", "logical", b, true, true, peerPID}}, peer, activePeer))
						fixture.RunStep(t, p.Ctx, bOwner, bRecord, fixture.Select("SELECT pg_backend_pid() AS pid", []string{"pid"}, []string{"INT4"}, [][]any{{peerPID}}))
					}
					aDB, aRaw, aRecord := fixture.DatabaseScenarioPool(t, p, backend.PG, a, "")
					aDB.SetMaxIdleConns(0)
					creator, err := aDB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { creator.Close() })
					createSlot(creator, aRecord, retired, false)
					if err := creator.Close(); err != nil {
						t.Fatal(err)
					}
					if err := aDB.Close(); err != nil {
						t.Fatal(err)
					}
					if err := aRaw.Close(); err != nil {
						t.Fatal(err)
					}
					fixture.DatabaseReferenceOwners(t, p, admin, backend.Recorder, a, 0)
					run(fixture.Select("SELECT slot_name,temporary,active,active_pid FROM pg_replication_slots WHERE slot_name=$1", []string{"slot_name", "temporary", "active", "active_pid"}, []string{"NAME", "BOOL", "BOOL", "INT4"}, [][]any{{retired, false, false, nil}}, retired))
					q := "DROP DATABASE " + a
					drop(q, fixture.Exec(q, 0))
					run(fixture.Select("SELECT slot_name FROM pg_replication_slots WHERE slot_name=$1", []string{"slot_name"}, []string{"NAME"}, nil, retired))
					peerHealthy()
					run(fixture.Exec("CREATE DATABASE "+a, 0))
					activeDB, activeRaw, activeRecord := fixture.DatabaseScenarioPool(t, p, backend.PG, a, "")
					activeDB.SetMaxIdleConns(0)
					owners := make([]*sql.Conn, 2)
					pids := make([]int64, 2)
					slots := []string{"dd_slots_active_one", "dd_slots_active_two"}
					for i := range owners {
						owners[i], err = activeDB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						owner := owners[i]
						t.Cleanup(func() { owner.Close() })
						createSlot(owner, activeRecord, slots[i], true)
						if err := owner.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pids[i]); err != nil {
							t.Fatal(err)
						}
					}
					for _, count := range []int{2, 1} {
						fixture.DatabaseReferenceOwners(t, p, admin, backend.Recorder, a, int64(count))
						for _, suffix := range []string{"", " WITH (FORCE)"} {
							q := "DROP DATABASE " + a + suffix
							want := fixture.ControlsReferenceDiagnostic("55006", "database \""+a+"\" is used by an active logical replication slot")
							want.Detail = "There is 1 active slot."
							if count == 2 {
								want.Detail = "There are 2 active slots."
							}
							for range 2 {
								drop(q, fixture.ErrorDiagnostic(q, want))
								for i := range count {
									fixture.RunStep(t, p.Ctx, owners[i], activeRecord, fixture.Select("SELECT pg_backend_pid() AS pid", []string{"pid"}, []string{"INT4"}, [][]any{{pids[i]}}))
									run(fixture.Select("SELECT slot_name,temporary,active,active_pid FROM pg_replication_slots WHERE slot_name=$1", []string{"slot_name", "temporary", "active", "active_pid"}, []string{"NAME", "BOOL", "BOOL", "INT4"}, [][]any{{slots[i], true, true, pids[i]}}, slots[i]))
								}
								peerHealthy()
							}
						}
						if err := owners[count-1].Close(); err != nil {
							t.Fatal(err)
						}
					}
					if err := activeDB.Close(); err != nil {
						t.Fatal(err)
					}
					if err := activeRaw.Close(); err != nil {
						t.Fatal(err)
					}
					fixture.DatabaseReferenceOwners(t, p, admin, backend.Recorder, a, 0)
					run(fixture.Select("SELECT slot_name FROM pg_replication_slots WHERE database=$1", []string{"slot_name"}, []string{"NAME"}, nil, a))
					q = "DROP DATABASE " + a + " WITH (FORCE)"
					drop(q, fixture.Exec(q, 0))
					run(fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, nil, a))
					peerHealthy()
					if err := bOwner.Close(); err != nil {
						t.Fatal(err)
					}
					if err := bDB.Close(); err != nil {
						t.Fatal(err)
					}
					if err := bRaw.Close(); err != nil {
						t.Fatal(err)
					}
					fixture.DatabaseReferenceOwners(t, p, admin, backend.Recorder, b, 0)
				})
			}
		})
	}
}
