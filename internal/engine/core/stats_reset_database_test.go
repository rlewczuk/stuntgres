package core_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"testing"
	"testing/synctest"
	"time"
)

type databaseResetFixture struct {
	e, peer                                               *core.Engine
	s, ps                                                 *session.Session
	a, b, f, g, db, sharedDB, shared, otherShared, peerDB statistics.Key
}

func databaseResetFixtureOf(t *testing.T) databaseResetFixture {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, s, "CREATE TABLE database_reset_a(v integer)", "CREATE TABLE database_reset_b(v integer)", "CREATE FUNCTION database_reset_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "CREATE FUNCTION database_reset_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 9; END$$", "CREATE DATABASE database_reset_peer")
	peer, err := e.SelectDatabase(context.Background(), "database_reset_peer")
	if err != nil {
		t.Fatal(err)
	}
	ps := enginetest.StartSession(t, peer, nil)
	key := func(target *core.Engine, address ids.ObjectAddress, category statistics.Category, epoch ids.CatalogEpoch) statistics.Key {
		return statistics.Key{Object: target.Catalog.Domain.Ref(epoch, address), Category: category}
	}
	return databaseResetFixture{e: e, peer: peer, s: s, ps: ps, a: statistics.Key{Object: enginetest.RoleTableRef(t, e, "database_reset_a"), Category: statistics.Relation}, b: statistics.Key{Object: enginetest.RoleTableRef(t, e, "database_reset_b"), Category: statistics.Relation}, f: statsFunctionKey(t, e, "database_reset_f"), g: statsFunctionKey(t, e, "database_reset_g"), db: key(e, e.Catalog.DatabaseAddress(), statistics.Database, 0), peerDB: key(peer, peer.Catalog.DatabaseAddress(), statistics.Database, 0), sharedDB: key(e, ids.ObjectAddress{ClassID: ids.DatabaseClassID}, statistics.Database, 0), shared: key(e, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1260}, statistics.Relation, e.CatalogRead().Epoch()), otherShared: key(e, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1262}, statistics.Relation, e.CatalogRead().Epoch())}
}
func databaseResetSeedCore(target *core.Engine, values map[statistics.Key]statistics.Fact) {
	var writer statistics.Writer
	for key, fact := range values {
		writer.Add(&target.Statistics, key, fact)
	}
	writer.Flush(&target.Statistics, time.Now(), true)
}
func databaseResetCall(t *testing.T, s *session.Session, name string, key statistics.Key) {
	t.Helper()
	resetStatsExec(t, s, fmt.Sprintf("SELECT %s(%d::oid)", name, key.Object.Address.ObjectID))
}
func TestStatsResetDatabaseRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := databaseResetFixtureOf(t)
		statsFunctionExec(t, x.s, "INSERT INTO database_reset_a VALUES(1),(2)", "ANALYZE database_reset_a", "BEGIN", "INSERT INTO database_reset_b VALUES(1)", "ROLLBACK")
		// The genuine local writer is drained before exact immediate observations.
		statsFunctionRetire(t, x.s)
		x.s = enginetest.StartSession(t, x.e, nil)
		if x.e.Statistics.Read(x.a).Inserts != 2 || x.e.Statistics.Read(x.a).AnalyzeCount != 1 || x.e.Statistics.Read(x.db).Database.Inserts < 3 || x.e.Statistics.Read(x.db).Database.Rollbacks == 0 {
			t.Fatal("genuine counters absent")
		}
		a := x.e.Statistics.Read(x.a)
		db := x.e.Statistics.Read(x.db).Database
		generation := x.e.Statistics.Generation()
		ast, d := enginetest.PreparedDescription(t, x.e, x.s, "SELECT pg_stat_reset_single_table_counters('database_reset_a'::regclass::oid)")
		if x.e.Statistics.Read(x.a) != a {
			t.Fatal("Prepare executed reset")
		}
		result, err := enginetest.PreparedExecute(x.e, x.s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		if err := resetStatsConsumeVoid(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		if got := x.e.Statistics.Read(x.a); got.Inserts != 0 || got.Live != 0 || got.Dead != 0 || got.Modifications != 0 || got.InsertsSince != 0 || got.AnalyzeCount != 0 || !got.LastAnalyze.IsZero() {
			t.Fatal("relation reset", got)
		}
		if x.e.Statistics.Read(x.db).Database.Inserts != db.Inserts || x.e.Statistics.Read(x.db).Database.ResetAt.IsZero() || x.e.Statistics.Generation() != generation {
			t.Fatal("single timestamp/generation")
		}
		// This independent relation has never received a unary reset. Publish
		// genuine maintenance plus later mutation history before database reset.
		statsFunctionExec(t, x.s, "CREATE TABLE database_reset_maintenance(v integer)", "INSERT INTO database_reset_maintenance VALUES(1),(2),(3)")
		statsFunctionRetire(t, x.s)
		x.s = enginetest.StartSession(t, x.e, nil)
		statsFunctionExec(t, x.s, "VACUUM database_reset_maintenance", "ANALYZE database_reset_maintenance", "INSERT INTO database_reset_maintenance VALUES(4),(5)", "UPDATE database_reset_maintenance SET v=11 WHERE v=1", "DELETE FROM database_reset_maintenance WHERE v=2")
		statsFunctionRetire(t, x.s)
		x.s = enginetest.StartSession(t, x.e, nil)
		maintenanceKey := statistics.Key{Object: enginetest.RoleTableRef(t, x.e, "database_reset_maintenance"), Category: statistics.Relation}
		positive := x.e.Statistics.Read(maintenanceKey)
		if positive.Inserts != 5 || positive.Updates != 1 || positive.Deletes != 1 || positive.Live != 4 || positive.Dead != 2 || positive.Modifications != 4 || positive.InsertsSince != 2 || positive.VacuumCount != 1 || positive.AnalyzeCount != 1 || positive.LastVacuum.IsZero() || positive.LastAnalyze.IsZero() {
			t.Fatal("genuine database-wide relation baseline", positive)
		}
		// Physical logical maintenance is independent of published counters:
		// UPDATE replaces a version without adding a dead estimate; DELETE adds one.
		x.e.Mu.Lock()
		physicalStore := x.e.Storage.Relations[maintenanceKey.Object.Address]
		var physicalMaintenance storage.Maintenance
		if physicalStore != nil {
			physicalMaintenance = physicalStore.Maintenance
		}
		x.e.Mu.Unlock()
		if physicalStore == nil || physicalMaintenance.Dead != 1 {
			t.Fatal("independent physical dead sample", physicalMaintenance)
		}
		optimizer := enginetest.AnalyzeFacts(x.e, maintenanceKey.Object.Address)
		if optimizer == nil {
			t.Fatal("actual optimizer root absent")
		}
		columnKey := storage.StatisticsKey{Attribute: 1}
		column := optimizer.Columns[columnKey]
		if column == nil || column.Rows != 3 || column.DistinctCount != 3 {
			t.Fatal("actual pre-DML optimizer column absent", column)
		}
		columnValue := *column
		columnValue.MCV = slices.Clone(column.MCV)
		columnValue.Histogram = slices.Clone(column.Histogram)
		optimizerBacking := reflect.ValueOf(optimizer.Columns).UnsafePointer()
		mcvBacking := reflect.ValueOf(column.MCV).Pointer()
		histogramBacking := reflect.ValueOf(column.Histogram).Pointer()
		physicalRows := func() {
			t.Helper()
			physical := enginetest.SessionSQL(t, x.s, "SELECT v FROM database_reset_maintenance ORDER BY v")
			rows := enginetest.Tuples(physical)
			if err := physical.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 4 {
				t.Fatal("database reset changed physical rows", rows)
			}
			for i, want := range []int64{3, 4, 5, 11} {
				if len(rows[i]) != 1 || rows[i][0] != want {
					t.Fatal("database reset changed physical rows", rows)
				}
			}
		}
		physicalRows()
		statsFunctionExec(t, x.s, "BEGIN")
		resetStatsExec(t, x.s, "SELECT pg_stat_reset()")
		if got := x.e.Statistics.Read(x.db).Database; got != (statistics.DatabaseFact{ResetAt: got.ResetAt}) || got.ResetAt.IsZero() {
			t.Fatal("immediate database vector", got)
		}
		if got := x.e.Statistics.Read(maintenanceKey); got != (statistics.Fact{ResetAt: positive.ResetAt, Generation: positive.Generation}) {
			t.Fatal("database reset retained genuine relation maintenance/autovacuum history", got)
		}
		x.e.Mu.Lock()
		physicalUnchanged := x.e.Storage.Relations[maintenanceKey.Object.Address] == physicalStore && physicalStore.Maintenance == physicalMaintenance
		x.e.Mu.Unlock()
		if !physicalUnchanged {
			t.Fatal("published reset changed physical maintenance authority")
		}
		if current := enginetest.AnalyzeFacts(x.e, maintenanceKey.Object.Address); current != optimizer || reflect.ValueOf(current.Columns).UnsafePointer() != optimizerBacking || current.Columns[columnKey] != column || reflect.ValueOf(column.MCV).Pointer() != mcvBacking || reflect.ValueOf(column.Histogram).Pointer() != histogramBacking || !reflect.DeepEqual(*column, columnValue) {
			t.Fatal("database reset changed retained optimizer root/column/backing")
		}
		statsFunctionExec(t, x.s, "ROLLBACK", "ANALYZE database_reset_a")
		if x.e.Statistics.Read(x.a).AnalyzeCount != 1 || x.e.Statistics.Read(x.a).Live != 2 {
			t.Fatal("new maintenance did not follow reset")
		}
		physicalRows()
		statsFunctionExec(t, x.s, "ANALYZE database_reset_maintenance")
		if got := x.e.Statistics.Read(maintenanceKey); got.AnalyzeCount != 1 || got.LastAnalyze.IsZero() || got.Live != 4 || got.Inserts != 0 || got.Updates != 0 || got.Deletes != 0 || got.Dead != 1 || got.Modifications != 0 || got.InsertsSince != 0 || got.VacuumCount != 0 || !got.LastVacuum.IsZero() {
			t.Fatal("new genuine maintenance after database reset", got)
		}
		if next := enginetest.AnalyzeFacts(x.e, maintenanceKey.Object.Address); next == nil || next.Columns[columnKey] == nil || next.Columns[columnKey].Rows != 4 {
			t.Fatal("new ANALYZE did not collect actual four rows")
		}
		statsFunctionExec(t, x.s, "BEGIN", "CREATE TABLE database_reset_private(v integer)", "ANALYZE database_reset_private")
		var private statistics.Key
		for address, descriptor := range x.s.Transactions.Catalog.Descriptors {
			if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == "database_reset_private" {
				private = statistics.Key{Object: x.e.Catalog.Domain.Ref(x.s.Transactions.Ref.Epoch(), address), Category: statistics.Relation}
			}
		}
		if private.Object.Domain() == nil || x.e.Statistics.Read(private).AnalyzeCount != 1 {
			t.Fatal("actual provisional maintenance absent")
		}
		databaseResetCall(t, x.s, "pg_stat_reset_single_table_counters", private)
		if x.e.Statistics.Read(private).AnalyzeCount != 0 {
			t.Fatal("reset required committed object visibility")
		}
		statsFunctionExec(t, x.s, "ROLLBACK")
		var current statistics.Reader
		current.Configure("snapshot", 1)
		current.Read(&x.e.Statistics, private, time.Now())
		if statsLifetimeSnapshotContains(&current, private) {
			t.Fatal("creation abort resurrected zero tracked entry")
		}
	})
}
func TestStatsResetDatabaseFunctions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := databaseResetFixtureOf(t)
		statsFunctionExec(t, x.s, "CREATE OR REPLACE FUNCTION database_reset_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.003); RETURN 7; END$$", "CREATE OR REPLACE FUNCTION database_reset_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.005); RETURN 9; END$$", "CREATE FUNCTION database_reset_untracked() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 11; END$$", "SET track_functions=none", "SELECT database_reset_untracked()")
		statsFunctionExec(t, x.s, "SET track_functions=pl", "SELECT database_reset_f()", "SELECT database_reset_f()", "SELECT database_reset_g()", "SELECT database_reset_g()", "SELECT database_reset_g()")
		statsFunctionRetire(t, x.s)
		f, g := x.e.Statistics.Read(x.f), x.e.Statistics.Read(x.g)
		if f.Calls != 2 || f.TotalTime != 6*time.Millisecond || f.SelfTime != 6*time.Millisecond || g.Calls != 3 || g.TotalTime != 15*time.Millisecond || g.SelfTime != 15*time.Millisecond {
			t.Fatal("genuine PL collector", f, g)
		}
		observer := enginetest.StartSession(t, x.e, nil)
		untracked := statsFunctionKey(t, x.e, "database_reset_untracked")
		databaseResetCall(t, observer, "pg_stat_reset_single_function_counters", untracked)
		var fresh statistics.Reader
		fresh.Configure("snapshot", 1)
		fresh.Read(&x.e.Statistics, untracked, time.Now())
		if statsLifetimeSnapshotContains(&fresh, untracked) {
			t.Fatal("reset allocated untracked function entry")
		}
		databaseResetCall(t, observer, "pg_stat_reset_single_function_counters", x.f)
		if got := x.e.Statistics.Read(x.f); got.Calls != 0 || got.TotalTime != 0 || got.SelfTime != 0 || x.e.Statistics.Read(x.g) != g {
			t.Fatal("function isolation", got)
		}
		statsFunctionExec(t, observer, "BEGIN", "DROP FUNCTION database_reset_g()")
		databaseResetCall(t, observer, "pg_stat_reset_single_function_counters", x.g)
		if got := x.e.Statistics.Read(x.g); got.Calls != 0 || got.TotalTime != 0 {
			t.Fatal("staged DROP lost original reset authority")
		}
		statsFunctionExec(t, observer, "ROLLBACK")
		if _, exists := x.e.CatalogRead().Procedure(x.g.Object.Address); !exists || x.e.Statistics.Read(x.g).Calls != 0 {
			t.Fatal("rollback restored reset history")
		}
		statsFunctionExec(t, observer, "SET track_functions=pl", "SELECT database_reset_f()")
		statsFunctionRetire(t, observer)
		if got := x.e.Statistics.Read(x.f); got.Calls != 1 || got.TotalTime != 3*time.Millisecond || got.SelfTime != 3*time.Millisecond {
			t.Fatal("later genuine call", got)
		}
		resetter := enginetest.StartSession(t, x.e, nil)
		resetStatsExec(t, resetter, "SELECT pg_stat_reset()")
		for _, key := range []statistics.Key{x.f, x.g} {
			if got := x.e.Statistics.Read(key); got.Calls != 0 || got.TotalTime != 0 || got.SelfTime != 0 {
				t.Fatal("database reset retained real function history", got)
			}
		}
	})
}
func TestStatsResetDatabaseClusterIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := databaseResetFixtureOf(t)
		peerShared := x.shared
		peerShared.Object = x.peer.Catalog.Domain.Ref(x.peer.CatalogRead().Epoch(), x.shared.Object.Address)
		foreign := x.shared
		foreign.Object = new(ids.Domain).Ref(x.e.CatalogRead().Epoch(), foreign.Object.Address)
		databaseResetSeedCore(x.e, map[statistics.Key]statistics.Fact{x.a: {Inserts: 11}, x.shared: {Inserts: 23}, x.otherShared: {Inserts: 29}, foreign: {Inserts: 31}, x.db: {Database: statistics.DatabaseFact{Inserts: 11, ResetAt: time.Unix(100, 0)}}, x.sharedDB: {Database: statistics.DatabaseFact{Inserts: 13, ResetAt: time.Unix(100, 0)}}})
		databaseResetSeedCore(x.peer, map[statistics.Key]statistics.Fact{peerShared: {Inserts: 23}, x.peerDB: {Database: statistics.DatabaseFact{Inserts: 17}}})
		sharedDB := x.e.Statistics.Read(x.sharedDB)
		resetStatsExec(t, x.s, "SELECT pg_stat_reset()")
		if x.e.Statistics.Read(x.a).Inserts != 0 || x.e.Statistics.Read(x.shared).Inserts != 23 || x.peer.Statistics.Read(peerShared).Inserts != 23 || x.e.Statistics.Read(x.sharedDB) != sharedDB || x.peer.Statistics.Read(x.peerDB).Database.Inserts != 17 {
			t.Fatal("database partition isolation")
		}
		local := x.e.Statistics.Read(x.db).Database
		databaseResetCall(t, x.s, "pg_stat_reset_single_table_counters", x.shared)
		if x.e.Statistics.Read(x.shared).Inserts != 0 || x.peer.Statistics.Read(peerShared).Inserts != 0 || x.e.Statistics.Read(x.otherShared).Inserts != 29 || x.e.Statistics.Read(foreign).Inserts != 31 || x.e.Statistics.Read(x.db).Database.ResetAt != local.ResetAt || x.e.Statistics.Read(x.sharedDB).Database.Inserts != 13 {
			t.Fatal("shared qualification/timestamp")
		}
	})
}
func TestStatsResetDatabaseProviderSmoke(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := databaseResetFixtureOf(t)
		io, wal, slot := x.a, x.a, x.a
		io.Category, wal.Category, slot.Category = statistics.IO, statistics.WAL, statistics.ReplicationSlot
		databaseResetSeedCore(x.e, map[statistics.Key]statistics.Fact{x.a: {Inserts: 5}, x.b: {Inserts: 7}, x.f: {Calls: 2}, x.g: {Calls: 3}, x.db: {Database: statistics.DatabaseFact{Inserts: 11}}, x.sharedDB: {Database: statistics.DatabaseFact{Inserts: 13}}, x.shared: {Inserts: 19}, io: {Operations: 23}, wal: {Operations: 29}, slot: {SpillTxns: 31}})
		databaseResetSeedCore(x.peer, map[statistics.Key]statistics.Fact{x.peerDB: {Database: statistics.DatabaseFact{Inserts: 17}}})
		statements := x.e.Cluster.StatementStatistics
		sk := statistics.StatementKey{UserID: 1, DatabaseID: 2, QueryID: 123, TopLevel: true}
		token := statements.Begin(sk, sk, "SELECT sentinel", time.Now())
		statements.RecordExecution(token, statistics.StatementRecord{Key: sk, Query: "SELECT sentinel", At: time.Now(), Rows: 37})
		statements.Release(token)
		var pending statistics.Writer
		pending.Add(&x.e.Statistics, x.a, statistics.Fact{Inserts: 2})
		var reader statistics.Reader
		reader.Configure("snapshot", 1)
		retained := reader.Read(&x.e.Statistics, x.a, time.Now())
		generation := x.e.Statistics.Generation()
		retirement := reflect.ValueOf(&x.e.Statistics).Elem().FieldByName("retired").UnsafePointer()
		ast, d := enginetest.PreparedDescription(t, x.e, x.s, "SELECT pg_stat_reset_single_table_counters('database_reset_a'::regclass::oid)")
		result, err := enginetest.PreparedExecute(x.e, x.s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		if err := resetStatsConsumeVoid(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		if x.e.Statistics.Read(x.a).Inserts != 0 || x.e.Statistics.Read(x.b).Inserts != 7 {
			t.Fatal("prepared table reset")
		}
		databaseResetCall(t, x.s, "pg_stat_reset_single_function_counters", x.f)
		if x.e.Statistics.Read(x.f).Calls != 0 || x.e.Statistics.Read(x.g).Calls != 3 {
			t.Fatal("function reset")
		}
		databaseResetCall(t, x.s, "pg_stat_reset_single_table_counters", x.shared)
		if x.e.Statistics.Read(x.shared).Inserts != 0 || x.e.Statistics.Read(x.sharedDB).Database.ResetAt.IsZero() {
			t.Fatal("shared reset")
		}
		resetStatsExec(t, x.s, "SELECT pg_stat_reset()")
		if x.e.Statistics.Read(x.db).Database.Inserts != 0 || x.e.Statistics.Read(x.b).Inserts != 0 || x.e.Statistics.Read(x.g).Calls != 0 || x.peer.Statistics.Read(x.peerDB).Database.Inserts != 17 || x.e.Statistics.Read(x.sharedDB).Database.Inserts != 13 || x.e.Statistics.Read(io).Operations != 23 || x.e.Statistics.Read(wal).Operations != 29 || x.e.Statistics.Read(slot).SpillTxns != 31 {
			t.Fatal("database reset isolation")
		}
		found := false
		for _, entry := range statements.Snapshot() {
			if entry.Key == sk {
				found = entry.Rows == 37
			}
		}
		if !found {
			t.Fatal("statement accumulator erased")
		}
		if x.e.Statistics.Generation() != generation || reflect.ValueOf(&x.e.Statistics).Elem().FieldByName("retired").UnsafePointer() != retirement || reader.Read(&x.e.Statistics, x.a, time.Now()) != retained {
			t.Fatal("lifecycle/capture changed")
		}
		pending.Flush(&x.e.Statistics, time.Now(), true)
		if x.e.Statistics.Read(x.a).Inserts != 2 {
			t.Fatal("pending two lost")
		}
	})
}
