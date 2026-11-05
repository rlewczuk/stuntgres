package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func tdControls(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("CREATE TABLE td_persistent(id integer PRIMARY KEY,v text)")
	s.Run(fixture.Exec("INSERT INTO td_persistent VALUES(7,'kept')", 1))
	s.Exec("CREATE VIEW td_view AS SELECT id,v FROM td_persistent")
	s.Exec("CREATE FUNCTION td_recipe() RETURNS SETOF td_persistent LANGUAGE SQL AS 'SELECT * FROM td_persistent ORDER BY id'")
	check := func() {
		predefinedSelect(s, "SELECT * FROM td_view", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
		predefinedSelect(s, "SELECT * FROM td_recipe()", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
	}
	check()
	if s.Backend.PG {
		return
	}
	// Only this feature's persistent SQL objects and provider are in the image
	// witness. Logical slots/history intentionally do not survive replacement.
	tdSlot(s, "td_slot")
	s.Run(fixture.Exec("INSERT INTO td_persistent VALUES(8,'transient')", 1))
	old := tdRead(s, "pg_logical_slot_peek_changes", nil, "")
	tdAssertRows(s.T, old, tdTxn("table public.td_persistent: INSERT: id[integer]:8 v[text]:'transient'")...)
	retained, err := s.C.QueryContext(s.P.Ctx, "SELECT lsn,xid,data FROM pg_logical_slot_peek_changes('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1')")
	if err != nil {
		s.T.Fatal(err)
	}
	defer retained.Close()
	descriptors, err := retained.ColumnTypes()
	if err != nil {
		s.T.Fatal(err)
	}
	s.Run(fixture.Exec("DELETE FROM td_persistent WHERE id=8", 1))
	var image bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &image); err != nil {
		s.T.Fatal(err)
	}
	before := tdPosition(s)
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(image.String()+"\nSELECT missing_td_recipe();\n")); err == nil {
		s.T.Fatal("invalid image published")
	}
	check()
	if tdPosition(s) != before {
		s.T.Fatal("failed restore changed slot position", before, tdPosition(s))
	}
	pending := append(tdTxn("table public.td_persistent: INSERT: id[integer]:8 v[text]:'transient'"), tdTxn("table public.td_persistent: DELETE: id[integer]:8")...)
	tdPeek(s, "", pending...)
	ctx, cancel := context.WithCancel(s.P.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, s.P.Native, bytes.NewReader(image.Bytes())); !errors.Is(err, context.Canceled) {
		s.T.Fatal("canceled restore", err)
	}
	check()
	if tdPosition(s) != before {
		s.T.Fatal("canceled restore changed slot position")
	}
	tdPeek(s, "", pending...)
	if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
		s.T.Fatal(err)
	}
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(image.Bytes())); err != nil {
		s.T.Fatal(err)
	}
	check()
	predefinedSelect(s, "SELECT slot_name FROM pg_replication_slots", []string{"slot_name"}, []string{"NAME"}, nil)
	tdAssertRows(s.T, old, tdTxn("table public.td_persistent: INSERT: id[integer]:8 v[text]:'transient'")...)
	if len(descriptors) != 3 {
		s.T.Fatal("retained descriptors", descriptors)
	}
	for i, typ := range []string{"PG_LSN", "XID", "TEXT"} {
		if descriptors[i].DatabaseTypeName() != typ {
			s.T.Fatal("retained descriptor changed", i, descriptors[i])
		}
	}
	var retainedData []tdRow
	for retained.Next() {
		var row tdRow
		if err := retained.Scan(&row.LSN, &row.XID, &row.Data); err != nil {
			s.T.Fatal(err)
		}
		retainedData = append(retainedData, row)
	}
	if err := retained.Err(); err != nil {
		s.T.Fatal(err)
	}
	tdAssertRows(s.T, retainedData, tdTxn("table public.td_persistent: INSERT: id[integer]:8 v[text]:'transient'")...)
	tdSlot(s, "td_slot")
	s.Run(fixture.Exec("INSERT INTO td_persistent VALUES(9,'new')", 1))
	tdGet(s, "", tdTxn("table public.td_persistent: INSERT: id[integer]:9 v[text]:'new'")...)
	s.Run(fixture.Exec("DELETE FROM td_persistent WHERE id=9", 1))
	check()
}

func TestReferenceTestDecodingControls(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) { tdControls(s) })
}

func TestReferenceTestDecodingFeatureSmoke(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		tdDML(s)
		before := tdPosition(s)
		a := tdRead(s, "pg_logical_slot_peek_changes", nil, "")
		tdAssertRows(s.T, a, tdDMLRows()...)
		tdAssertRows(s.T, tdRead(s, "pg_logical_slot_peek_changes", nil, ""), tdDMLRows()...)
		if tdPosition(s) != before {
			s.T.Fatal("smoke peek consumed")
		}
		s.Exec("CREATE ROLE td_reader REPLICATION")
		q := "SELECT data FROM pg_logical_slot_get_changes($1::name,NULL,NULL,'include-xids','0','skip-empty-xacts','1')"
		held := predefinedHeld(s, q)
		s.Exec("SET ROLE td_reader")
		admin := tdPeer(s, db)
		tdExec(s, admin, "ALTER ROLE td_reader NOREPLICATION", 0)
		logicalDecodingHeldFailure(s, held, logicalDecodingDenied(), "td_slot")
		tdExec(s, admin, "ALTER ROLE td_reader REPLICATION", 0)
		want := tdDMLRows()
		rows := make([][]any, len(want))
		for i, v := range want {
			rows[i] = []any{v}
		}
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(q, []string{"data"}, []string{"TEXT"}, rows, "td_slot"))
		s.Exec("RESET ROLE")
		tdGet(s, "")
		slotDrop(s, "td_slot")
		tdRewriteWitness(s)
		slotDrop(s, "td_slot")
		// Real live streaming, then full abort, not a below-threshold ordinary
		// transaction masquerading as streaming. Postabort is oracle-pinned.
		s.Exec("CREATE TABLE td_barrier(v integer)")
		tdSlot(s, "td_slot")
		s.Exec("SET debug_logical_replication_streaming=immediate")
		writer := tdPeer(s, db)
		tdExec(s, writer, "BEGIN", 0)
		xid := tdXID(s, writer)
		tdExec(s, writer, "INSERT INTO t VALUES(3,'v')", 1)
		tdBarrier(s, db)
		tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_peek_changes", nil, tdStream), xid), tdOpen, tdChange, tdClose)
		tdExec(s, writer, "ROLLBACK", 0)
		tdBarrier(s, db)
		// The fresh postabort context's catalog-live check suppresses the
		// aborted top transaction; the earlier live peek proved its history.
		tdAssertRows(s.T, tdForXID(tdRead(s, "pg_logical_slot_get_changes", nil, tdStream), xid))
		slotDrop(s, "td_slot")
		tdControls(s)
	})
}
