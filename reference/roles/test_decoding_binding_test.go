package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTestDecodingConsumption(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdTable(s)
		start := tdSlot(s, "td_slot")
		s.Exec("BEGIN")
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
		s.Exec("COMMIT")
		want := tdTxn(tdInsert(1), tdInsert(2))
		before := tdPosition(s)
		a := tdRead(s, "pg_logical_slot_peek_changes", nil, "")
		tdAssertRows(s.T, a, want...)
		b := tdRead(s, "pg_logical_slot_peek_changes", nil, "")
		if !reflect.DeepEqual(a, b) || tdPosition(s) != before {
			s.T.Fatal("peek consumed or changed immutable frontier", a, b, before, tdPosition(s))
		}
		for _, limit := range []any{int64(0), int64(1)} {
			tdAssertRows(s.T, tdRead(s, "pg_logical_slot_peek_changes", limit, ""), want...)
		}
		tdAssertRows(s.T, tdRead(s, "pg_logical_slot_peek_changes", int64(-1), ""))
		for _, lsn := range []string{start} {
			s.Run(fixture.Select("SELECT data FROM pg_logical_slot_peek_changes('td_slot',$1::pg_lsn,NULL,'include-xids','0','skip-empty-xacts','1')", []string{"data"}, []string{"TEXT"}, nil, lsn))
		}
		last := a[len(a)-1].LSN
		rows := make([][]any, len(want))
		for i, v := range want {
			rows[i] = []any{v}
		}
		for _, lsn := range []string{"0/0", last, "FFFFFFFF/FFFFFFFF"} {
			s.Run(fixture.Select("SELECT data FROM pg_logical_slot_peek_changes('td_slot',$1::pg_lsn,NULL,'include-xids','0','skip-empty-xacts','1')", []string{"data"}, []string{"TEXT"}, rows, lsn))
		}
		tdAssertRows(s.T, tdRead(s, "pg_logical_slot_get_changes", int64(1), ""), want...)
		tdGet(s, "")
		s.Run(fixture.Exec("INSERT INTO t VALUES(3,'v')", 1))
		// A successful eager native execution consumes even when Rows is closed
		// before iteration. PostgreSQL materializes the SQL SRF before delivery.
		r, err := s.C.QueryContext(s.P.Ctx, "SELECT * FROM pg_logical_slot_get_changes('td_slot',NULL,NULL,'skip-empty-xacts','1')")
		if err != nil {
			s.T.Fatal(err)
		}
		if err := r.Close(); err != nil {
			s.T.Fatal(err)
		}
		tdGet(s, "")
	})
}

func TestReferenceTestDecodingMetadata(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		for _, sig := range logicalDecodingSignatures {
			logicalDecodingIdentity(s, sig)
			q := "SELECT * FROM " + sig.name + "('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1')"
			s.Run(fixture.Select(q, []string{"lsn", "xid", "data"}, []string{"PG_LSN", "XID", sig.data}, nil))
		}
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		for _, sig := range logicalDecodingSignatures {
			if sig.name == "pg_logical_slot_get_changes" || sig.name == "pg_logical_slot_get_binary_changes" {
				continue
			}
			q := "SELECT lsn IS NOT NULL AS lsn,xid::text<>'0' AS xid,data FROM " + sig.name + "('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1')"
			s.Run(fixture.Select(q, []string{"lsn", "xid", "data"}, []string{"BOOL", "BOOL", sig.data}, [][]any{{true, true, "BEGIN"}, {true, true, tdInsert(1)}, {true, true, "COMMIT"}}))
		}
		old := tdRead(s, "pg_logical_slot_peek_changes", nil, "")
		s.Exec("ALTER TABLE t RENAME COLUMN v TO changed")
		tdGet(s, "", tdTxn(tdInsert(1))...)
		tdAssertRows(s.T, old, tdTxn(tdInsert(1))...)
	})
}

func TestReferenceTestDecodingPreparedAuthority(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		tdTable(s)
		start := tdSlot(s, "td_slot")
		tdSlot(s, "td_other")
		s.Exec("CREATE ROLE td_reader REPLICATION")
		s.Exec("BEGIN")
		originalXID := tdXID(s, s.C)
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		s.Exec("COMMIT")
		q := "SELECT data FROM pg_logical_slot_get_changes($1::name,$2::pg_lsn,$3::integer,VARIADIC $4::text[])"
		held := predefinedHeld(s, q)
		args := func(slot any, limit any, options any) []any { return []any{slot, nil, limit, options} }
		// SQL array text is evaluated afresh on each Execute, not retained scratch.
		options := "{include-xids,0,skip-empty-xacts,1}"
		s.Exec("SET ROLE td_reader")
		admin := tdPeer(s, db)
		tdExec(s, admin, "ALTER ROLE td_reader NOREPLICATION", 0)
		d := logicalDecodingDenied()
		d.Severity = "ERROR"
		d.SeverityUnlocalized = "ERROR"
		step := fixture.ErrorDiagnostic(q, d, args("td_slot", nil, options)...)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		tdExec(s, admin, "ALTER ROLE td_reader REPLICATION", 0)
		s.Exec("RESET ROLE")
		s.Exec("SET output_plugin_libraries='pgoutput'")
		s.Exec("SET ROLE td_reader")
		before := tdPosition(s)
		denied := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `library "test_decoding" may not be used as an output plugin`, Hint: `If it is safe for all REPLICATION users to use this library as an output plugin, add it to "output_plugin_libraries" and reload the server configuration.`}
		step = fixture.ErrorDiagnostic(q, denied, args("td_slot", nil, options)...)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		if tdPosition(s) != before {
			s.T.Fatal("allowlist denial consumed slot")
		}
		s.Exec("RESET ROLE")
		s.Exec("RESET output_plugin_libraries")
		s.Exec("SET ROLE td_reader")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(q, []string{"data"}, []string{"TEXT"}, nil, "td_slot", start, int64(1), options))
		read := func(slot string, limit any, want ...string) {
			rows := make([][]any, len(want))
			for i, v := range want {
				rows[i] = []any{v}
			}
			fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(q, []string{"data"}, []string{"TEXT"}, rows, args(slot, limit, options)...))
		}
		read("td_other", int64(1), tdTxn(tdInsert(1))...)
		read("td_other", nil)
		d = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "slot name must not be null"}
		step = fixture.ErrorDiagnostic(q, d, args(nil, nil, options)...)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		d.Message = "options array must not be null"
		step = fixture.ErrorDiagnostic(q, d, args("td_slot", nil, nil)...)
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		options = "{include-xids,1,skip-empty-xacts,1}"
		read("td_slot", nil, "BEGIN "+originalXID, tdInsert(1), "COMMIT "+originalXID)
		options = "{include-xids,0,skip-empty-xacts,1}"
		s.Exec("RESET ROLE")
		slotDrop(s, "td_slot")
		tdSlot(s, "td_slot")
		s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
		s.Exec("SET ROLE td_reader")
		read("td_slot", nil, tdTxn(tdInsert(2))...)
		s.Exec("RESET ROLE")
	})
}

func TestReferenceTestDecodingCancellation(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		decoder := s.C
		if s.Backend.PG {
			// Reserve the cold decoder before creating t, so even pool reuse
			// cannot leave its relation descriptor warm.
			decoder = tdPeer(s, db)
		}
		tdTable(s)
		tdSlot(s, "td_slot")
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,'v')", 1))
		before := tdPosition(s)
		q := "SELECT data FROM pg_logical_slot_get_changes('td_slot',NULL,NULL,'include-xids','0','skip-empty-xacts','1')"
		stmt, err := decoder.PrepareContext(s.P.Ctx, q)
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
		if s.Backend.PG {
			tdCancelAdmitted(s, db, decoder, stmt, q)
		} else {
			// Native pre-admission cancellation is an explicit adapter phase;
			// deterministic admitted native barriers live with the owner tests.
			ctx, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			var r *sql.Rows
			if s.Prepared {
				r, err = stmt.QueryContext(ctx)
			} else {
				r, err = decoder.QueryContext(ctx, q)
			}
			if r != nil {
				r.Close()
				s.T.Fatal("pre-canceled decode exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				s.T.Fatal("pre-canceled decode", err)
			}
		}
		if after := tdPosition(s); after != before {
			s.T.Fatal("cancel confirmed", before, after)
		}
		predefinedSelect(s, "SELECT active,active_pid IS NULL AS released FROM pg_replication_slots WHERE slot_name='td_slot'", []string{"active", "released"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}})
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "v"}})
		var recovery fixture.Executor = decoder
		if s.Prepared {
			recovery = fixture.HeldStatement{Executor: decoder, Statement: stmt, SQL: q}
		}
		fixture.RunStep(s.T, s.P.Ctx, recovery, s.Record, fixture.Select(q, []string{"data"}, []string{"TEXT"}, [][]any{{"BEGIN"}, {tdInsert(1)}, {"COMMIT"}}))
		fixture.RunStep(s.T, s.P.Ctx, recovery, s.Record, fixture.Select(q, []string{"data"}, []string{"TEXT"}, nil))
		if after := tdPosition(s); after == before {
			s.T.Fatal("recovered get did not confirm pending transaction", before)
		}
		slotDrop(s, "td_slot")
		predefinedSelect(s, "SELECT slot_name::text FROM pg_replication_slots WHERE slot_name='td_slot'", []string{"slot_name"}, []string{"TEXT"}, nil)
	})
}
