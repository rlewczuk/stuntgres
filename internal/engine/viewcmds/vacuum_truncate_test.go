package viewcmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func vacuumTruncateStatistics(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "autocommit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE truncate_stats(id INT)", "INSERT INTO truncate_stats VALUES(1),(2),(3)", "DELETE FROM truncate_stats WHERE id=1", "VACUUM truncate_stats"} {
				enginetest.SessionSQL(t, s, q)
			}
			ref, old := vacuumStore(t, e, "truncate_stats")
			before := old.Maintenance
			if explicit {
				enginetest.TransactionBegin(t, s)
			}
			enginetest.SessionSQL(t, s, "TRUNCATE truncate_stats")
			if !explicit {
				empty := e.Storage.Relations[ref.Address]
				m := empty.Maintenance
				if len(empty.Versions) != 0 || m.Live != 0 || m.Dead != 0 || m.InsertsSince != 0 || m.Scanned || m.FrozenXID < 3 || m.VacuumCount != before.VacuumCount || m.LastVacuum != before.LastVacuum {
					t.Fatal("empty truncate did not reset actual current facts", m)
				}
			}
			enginetest.SessionSQL(t, s, "INSERT INTO truncate_stats VALUES(9)")
			if explicit {
				if e.Storage.Relations[ref.Address] != old || old.Maintenance != before {
					t.Fatal("private truncate changed published maintenance")
				}
				enginetest.SessionSQL(t, s, "COMMIT")
			}
			nextRef, fresh := vacuumStore(t, e, "truncate_stats")
			m := fresh.Maintenance
			if nextRef != ref || fresh == old || len(fresh.Versions) != 1 {
				t.Fatal("truncate storage/object lifetime")
			}
			if m.VacuumCount != before.VacuumCount || m.LastVacuum != before.LastVacuum || m.Updates != before.Updates || m.Deletes != before.Deletes {
				t.Fatal("truncate reset cumulative history", before, m)
			}
			if m.Inserts != before.Inserts+1 || m.Modifications != before.Modifications+1 || m.InsertsSince != 1 || m.Live != 1 || m.Dead != 0 {
				t.Fatal("post-truncate deltas not actual once", before, m)
			}
			if m.Scanned {
				t.Fatal("new truncated root retained stale scan estimate")
			}
			result := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT reltuples=-1::real AS unknown FROM pg_class WHERE oid='truncate_stats'::regclass"))
			if len(result) != 1 || result[0][0] != true {
				t.Fatal("committed-table truncate estimate not unknown", result)
			}
			publisher := fresh.Versions[0].InsertFullXID
			if publisher < 3 || m.FrozenXID < 3 || m.FrozenXID > publisher || fresh.Versions[0].Frozen || fresh.Versions[0].ImportFrozen {
				t.Fatal("truncate/post-insert horizon not genuine", publisher, m)
			}
			if old.Maintenance != before {
				t.Fatal("truncate mutated predecessor maintenance")
			}
			stable := fresh.Maintenance
			enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "TRUNCATE truncate_stats")
			enginetest.SessionSQL(t, s, "ROLLBACK")
			if e.Storage.Relations[ref.Address] != fresh || fresh.Maintenance != stable {
				t.Fatal("rolled-back truncate changed published maintenance")
			}
			enginetest.TransactionBegin(t, s)
			for _, q := range []string{"SAVEPOINT boundary", "TRUNCATE truncate_stats", "ROLLBACK TO boundary", "COMMIT"} {
				enginetest.SessionSQL(t, s, q)
			}
			if e.Storage.Relations[ref.Address] != fresh || fresh.Maintenance != stable {
				t.Fatal("rollback-to truncate changed published maintenance")
			}
			enginetest.SessionSQL(t, s, "VACUUM truncate_stats")
			if fresh.Maintenance.VacuumCount != before.VacuumCount+1 || !fresh.Maintenance.Scanned || fresh.Maintenance.Tuples != 1 {
				t.Fatal("post-truncate VACUUM completion lost cumulative history", fresh.Maintenance)
			}
			enginetest.SessionSQL(t, s, "DROP TABLE truncate_stats")
			enginetest.SessionSQL(t, s, "CREATE TABLE truncate_stats(id INT)")
			recreatedRef, recreated := vacuumStore(t, e, "truncate_stats")
			if recreatedRef == ref || recreated.Maintenance.VacuumCount != 0 || !recreated.Maintenance.LastVacuum.IsZero() || recreated.Maintenance.Scanned {
				t.Fatal("same-name new table inherited truncate/vacuum history")
			}
		})
	}
}
