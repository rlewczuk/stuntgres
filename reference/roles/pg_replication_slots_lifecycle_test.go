package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const slotInventorySelect = "SELECT * FROM pg_catalog.pg_replication_slots"
var slotInventoryNames = []string{"slot_name", "plugin", "slot_type", "datoid", "database", "temporary", "active", "active_pid", "xmin", "catalog_xmin", "restart_lsn", "confirmed_flush_lsn", "wal_status", "safe_wal_size", "two_phase", "two_phase_at", "inactive_since", "conflicting", "invalidation_reason", "failover", "synced"}
var slotInventoryTypes = []string{"NAME", "NAME", "TEXT", "OID", "NAME", "BOOL", "BOOL", "INT4", "XID", "XID", "PG_LSN", "PG_LSN", "TEXT", "INT8", "BOOL", "PG_LSN", "TIMESTAMPTZ", "BOOL", "TEXT", "BOOL", "BOOL"}

func slotInventoryMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, slotInventoryNames) { t.Fatal(names, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 21 { t.Fatal(types, err) }
	for i, typ := range types {
		nullable, known := typ.Nullable()
		if typ.DatabaseTypeName() != slotInventoryTypes[i] || pg && (nullable || known) || !pg && (!nullable || !known) { t.Fatal(i, typ, nullable, known) }
	}
}

func slotInventoryEmpty(s *fixture.RoleMembershipScenario, e fixture.Executor) {
	s.T.Helper()
	step := fixture.Select(slotInventorySelect, slotInventoryNames, slotInventoryTypes, nil)
	step.CheckMetadata = func(t *testing.T, r *sql.Rows) { slotInventoryMetadata(t, r, s.Backend.PG) }
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, e, s.Record, step)
}

func slotInventoryNative(s *fixture.RoleMembershipScenario, executors ...fixture.Executor) {
	s.T.Helper()
	if s.Backend.PG { return }
	for _, e := range executors { slotInventoryEmpty(s, e) }
}

func slotInventoryAbsent(s *fixture.RoleMembershipScenario, e fixture.Executor, name string) {
	s.T.Helper()
	step := fixture.Select(slotInventorySelect + " WHERE slot_name=$1", slotInventoryNames, slotInventoryTypes, nil, name)
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, e, s.Record, step)
}

func slotInventoryPhysical(s *fixture.RoleMembershipScenario, e *sql.Conn, name, restart string, temporary bool, owner *sql.Conn) {
	s.T.Helper()
	if !s.Backend.PG { slotInventoryEmpty(s, e); return }
	var inactive any
	var valid bool
	if err := e.QueryRowContext(s.P.Ctx, "SELECT inactive_since,inactive_since IS NULL OR inactive_since<=clock_timestamp() FROM pg_replication_slots WHERE slot_name=$1", name).Scan(&inactive, &valid); err != nil || !valid { s.T.Fatal(inactive, valid, err) }
	var pid any
	if temporary {
		var n int64
		if err := owner.QueryRowContext(s.P.Ctx, "SELECT pg_backend_pid()").Scan(&n); err != nil { s.T.Fatal(err) }
		pid = n
	}
	var position, status any
	if restart != "" { position, status = restart, "reserved" }
	want := [][]any{{name, nil, "physical", nil, nil, temporary, temporary, pid, nil, nil, position, nil, status, nil, false, nil, fixture.Normalize(inactive), nil, nil, false, false}}
	step := fixture.Select(slotInventorySelect + " WHERE slot_name=$1", slotInventoryNames, slotInventoryTypes, want, name)
	step.Prepared = s.Prepared
	fixture.OidReferenceRows(s.T, s.P.Ctx, s.Backend, e, step)
}

func slotInventoryLogical(s *fixture.RoleMembershipScenario, e *sql.Conn, name, plugin, confirmed string, temporary, twoPhase, failover bool, owner *sql.Conn) {
	s.T.Helper()
	if !s.Backend.PG { slotInventoryEmpty(s, e); return }
	var pid any
	if temporary {
		var n int64
		if err := owner.QueryRowContext(s.P.Ctx, "SELECT pg_backend_pid()").Scan(&n); err != nil { s.T.Fatal(err) }
		pid = n
	}
	q := "SELECT slot_name,plugin,slot_type,datoid=(SELECT oid FROM pg_database WHERE datname=current_database()) AS database_oid,database=current_database() AS database_name,temporary,active,active_pid,xmin IS NULL OR xmin::text<>'0' AS horizon,catalog_xmin IS NOT NULL AS catalog_horizon,restart_lsn IS NOT NULL AND restart_lsn<=confirmed_flush_lsn AS retained,confirmed_flush_lsn::text,wal_status,safe_wal_size,two_phase,(NOT two_phase AND two_phase_at IS NULL) OR (two_phase AND two_phase_at IS NOT NULL AND two_phase_at<=confirmed_flush_lsn) AS phase_position,inactive_since IS NULL OR inactive_since<=clock_timestamp() AS inactive_time,conflicting,invalidation_reason,failover,synced FROM pg_replication_slots WHERE slot_name=$1"
	step := fixture.Select(q, []string{"slot_name", "plugin", "slot_type", "database_oid", "database_name", "temporary", "active", "active_pid", "horizon", "catalog_horizon", "retained", "confirmed_flush_lsn", "wal_status", "safe_wal_size", "two_phase", "phase_position", "inactive_time", "conflicting", "invalidation_reason", "failover", "synced"}, []string{"NAME", "NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT8", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{name, plugin, "logical", true, true, temporary, temporary, pid, true, true, true, confirmed, "reserved", nil, twoPhase, true, true, false, nil, failover, false}}, name)
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, e, s.Record, step)
}

func TestReferencePGReplicationSlotsLifecycle(t *testing.T) {
	slotCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		peer, err := db.Conn(s.P.Ctx)
		if err != nil { s.T.Fatal(err) }
		defer peer.Close()
		if s.Backend.PG { slotCleanup(s.T, db, "slots_meta_physical", "slots_meta_logical", "slots_meta_temporary") }
		slotInventoryEmpty(s, s.C)
		predefinedSelect(s, "SHOW max_slot_wal_keep_size", []string{"max_slot_wal_keep_size"}, []string{"TEXT"}, [][]any{{"-1"}})
		slotCreate(s, "slots_meta_physical", false, false, false)
		slotInventoryPhysical(s, s.C, "slots_meta_physical", "", false, s.C)
		s.Exec("SET ROLE rsl_actor")
		slotInventoryPhysical(s, s.C, "slots_meta_physical", "", false, s.C)
		s.Exec("RESET ROLE")
		if s.Backend.PG { slotDrop(s, "slots_meta_physical") }
		slotInventoryAbsent(s, peer, "slots_meta_physical")
		query := "SELECT * FROM pg_create_logical_replication_slot('slots_meta_logical','test_decoding',false,true,true)"
		var logicalStart string
		if s.Backend.PG {
			var name string
			if err := s.C.QueryRowContext(s.P.Ctx, query).Scan(&name, &logicalStart); err != nil || name != "slots_meta_logical" { s.T.Fatal(name, logicalStart, err) }
		} else { slotFailure(s, query, slotPluginMissing("test_decoding")) }
		slotInventoryLogical(s, s.C, "slots_meta_logical", "test_decoding", logicalStart, false, true, true, s.C)
		fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("SET ROLE rsl_actor", 0))
		slotInventoryLogical(s, peer, "slots_meta_logical", "test_decoding", logicalStart, false, true, true, s.C)
		fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("RESET ROLE", 0))
		if s.Backend.PG { slotDrop(s, "slots_meta_logical") }
		slotInventoryAbsent(s, peer, "slots_meta_logical")
		for _, logical := range []bool{false, true} {
			start := slotCreate(s, "slots_meta_temporary", logical, false, true)
			if logical { slotInventoryLogical(s, peer, "slots_meta_temporary", "test_decoding", start, true, false, false, s.C) } else { slotInventoryPhysical(s, peer, "slots_meta_temporary", "", true, s.C) }
			// An error on the owner retires its temporary slots; peer reads do not.
			slotFailure(s, "SELECT pg_drop_replication_slot('slots_meta_missing')", logicalDecodingMissing("slots_meta_missing"))
			slotInventoryAbsent(s, peer, "slots_meta_temporary")
			slotInventoryNative(s, s.C, peer)
		}
		slotInventoryEmpty(s, peer)
	})
}
