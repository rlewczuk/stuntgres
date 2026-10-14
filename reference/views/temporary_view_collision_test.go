package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewImportCollisions(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP TABLE collision_base(v INT)", nil)
		s.Exec(s.C, "INSERT INTO collision_base VALUES(11)", 1)
		s.Utility("CREATE VIEW collision_v AS SELECT v FROM collision_base", nil)
		s.Utility("CREATE VIEW collision_w AS SELECT v FROM collision_v", nil)
		s.Exec(s.Peer, "CREATE TEMP VIEW collision_peer AS SELECT 22 AS v", 0)
		temporaryViewNotices(t, n, "collision_v", "collision_w")
		fixture.TemporaryTableRows(s, s.C, "collision_w", 11)
		if s.Postgres {
			return
		}
		var oid, rule int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='collision_v'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		source := fixture.NewFixture(t, false, true)
		// Offset the second real TEMP namespace allocation so the imported
		// relation/rule inventory genuinely overlaps the retained identities.
		fixture.RunStep(t, s.P.Ctx, source.Memory, source.MemoryRecord, fixture.Exec("CREATE SCHEMA collision_padding", 0))
		for i := range 12 {
			for _, step := range []fixture.Step{fixture.Exec(fmt.Sprintf("CREATE TABLE imported_%d(v INT)", i), 0), fixture.Exec(fmt.Sprintf("INSERT INTO imported_%d VALUES(%d)", i, i), 1), fixture.Exec(fmt.Sprintf("CREATE VIEW imported_view_%d AS SELECT v FROM imported_%d", i, i), 0)} {
				fixture.RunStep(t, s.P.Ctx, source.Memory, source.MemoryRecord, step)
			}
		}
		var overlap int64
		if err := source.Memory.QueryRowContext(s.P.Ctx, "SELECT count(*) FROM pg_catalog.pg_class WHERE oid=$1 OR oid=$2", oid, rule).Scan(&overlap); err != nil {
			t.Fatal(err)
		}
		if overlap == 0 {
			if err := source.Memory.QueryRowContext(s.P.Ctx, "SELECT count(*) FROM pg_catalog.pg_rewrite WHERE oid=$1 OR oid=$2", oid, rule).Scan(&overlap); err != nil {
				t.Fatal(err)
			}
		}
		if overlap == 0 {
			t.Fatal("fixture did not exercise an actual imported relation/rule identity collision")
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, source.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var afterOID, afterRule int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='collision_v'").Scan(&afterOID); err != nil {
			t.Fatal(err)
		}
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class=$1", afterOID).Scan(&afterRule); err != nil {
			t.Fatal(err)
		}
		if afterOID != oid || afterRule != rule {
			t.Fatal("TEMP identities reminted", oid, afterOID, rule, afterRule)
		}
		fixture.TemporaryTableRows(s, s.C, "collision_w", 11)
		fixture.TemporaryTableRows(s, s.Peer, "collision_peer", 22)
		fixture.TemporaryTableRows(s, s.C, "imported_view_11", 11)
		s.Exec(s.C, "INSERT INTO collision_base VALUES(12)", 1)
		fixture.TemporaryTableRows(s, s.C, "collision_w", 11, 12)
		second := fixture.ViewDump(t, s.P)
		replayed := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(s.P.Ctx, replayed.Native, bytes.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, s.P.Ctx, replayed.Memory, replayed.MemoryRecord, fixture.Select("SELECT v FROM imported_view_11", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		fixture.TemporaryTableRows(s, s.C, "collision_w", 11, 12)
	})
}
