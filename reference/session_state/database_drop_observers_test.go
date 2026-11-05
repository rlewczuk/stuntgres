package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatabaseDropObservers(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b, slot = "dd_observer_target", "dd_observer_peer", "dd_observer_slot"
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					admin, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					run := func(x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, x, r, s)
					}
					for _, name := range names {
						run(admin, backend.Recorder, fixture.Exec("CREATE DATABASE "+name, 0))
					}
					peer, peerRaw, pr := fixture.DatabaseScenarioPool(t, p, backend.PG, b, "")
					run(peer, pr, fixture.Exec("CREATE EXTENSION pg_buffercache", 0))
					run(peer, pr, fixture.Exec("CREATE TABLE observer_value(v integer)", 0))
					run(peer, pr, fixture.Exec("INSERT INTO observer_value VALUES(22)", 1))
					target, targetRaw, tr := fixture.DatabaseScenarioPool(t, p, backend.PG, a, "")
					run(target, tr, fixture.Exec("CREATE TABLE observer_value(v integer)", 0))
					run(target, tr, fixture.Exec("INSERT INTO observer_value VALUES(11)", 1))
					run(target, tr, fixture.Select("SELECT v FROM observer_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					run(target, tr, fixture.Select("SELECT slot_name,lsn IS NOT NULL AS positioned FROM pg_create_logical_replication_slot($1,'test_decoding')", []string{"slot_name", "positioned"}, []string{"NAME", "BOOL"}, [][]any{{slot, true}}, slot))
					var oid string
					if err := admin.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_database WHERE datname=$1", a).Scan(&oid); err != nil {
						t.Fatal(err)
					}
					retained, err := peer.QueryContext(p.Ctx, "SELECT datname FROM pg_stat_database WHERE datid=$1::oid", oid)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { retained.Close() })
					// Shared reset timestamps are independent authority, not counters to zero
					// as a side effect of removing one database's cumulative partition.
					var before string
					if err := peer.QueryRowContext(p.Ctx, "SELECT stats_reset::text FROM pg_stat_bgwriter").Scan(&before); err != nil {
						t.Fatal(err)
					}
					for _, db := range []*sql.DB{target, targetRaw} {
						if err := db.Close(); err != nil {
							t.Fatal(err)
						}
					}
					fixture.DatabaseReferenceOwners(t, p, admin, backend.Recorder, a, 0)
					query := "DROP DATABASE " + a
					var dropper fixture.Executor = admin
					if mode == "held" {
						statement, err := admin.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						dropper = fixture.HeldStatement{Executor: admin, Statement: statement, SQL: query}
					}
					step := fixture.Exec(query, 0)
					if mode == "fresh" {
						step = fixture.Prepared(step)
					}
					run(dropper, backend.Recorder, step)
					for _, inquiry := range []string{
						"SELECT count(*) FROM pg_database WHERE oid=$1::oid",
						"SELECT count(*) FROM pg_stat_database WHERE datid=$1::oid",
						"SELECT count(*) FROM pg_stat_activity WHERE datid=$1::oid",
						"SELECT count(*) FROM pg_locks WHERE database=$1::oid",
						"SELECT count(*) FROM pg_buffercache WHERE reldatabase=$1::oid",
					} {
						run(peer, pr, fixture.Select(inquiry, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
					}
					run(peer, pr, fixture.Select("SELECT slot_name FROM pg_replication_slots WHERE slot_name=$1", []string{"slot_name"}, []string{"NAME"}, nil, slot))
					run(peer, pr, fixture.Select("SELECT slot_name FROM pg_stat_replication_slots WHERE slot_name=$1", []string{"slot_name"}, []string{"TEXT"}, nil, slot))
					var after string
					if err := peer.QueryRowContext(p.Ctx, "SELECT stats_reset::text FROM pg_stat_bgwriter").Scan(&after); err != nil || before != after {
						t.Fatal("DROP reset shared authority", before, after, err)
					}
					if !retained.Next() {
						t.Fatal("retained statistics membership lost", retained.Err())
					}
					var old string
					if err := retained.Scan(&old); err != nil || old != a {
						t.Fatal("retained membership changed", old, err)
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal("retained membership tail", retained.Err())
					}
					retained.Close()
					run(peer, pr, fixture.Select("SELECT v FROM observer_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
					run(peer, pr, fixture.Select("SELECT count(*) FROM pg_stat_database WHERE datid=0", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					for _, query := range []string{"SELECT pg_stat_reset_shared('bgwriter')", "SELECT pg_stat_reset_slru('Xact')"} {
						run(peer, pr, fixture.Select(query, []string{func() string {
							if query == "SELECT pg_stat_reset_slru('Xact')" {
								return "pg_stat_reset_slru"
							}
							return "pg_stat_reset_shared"
						}()}, []string{"VOID"}, [][]any{{""}}))
					}
					for _, db := range []*sql.DB{peer, peerRaw} {
						if err := db.Close(); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}
