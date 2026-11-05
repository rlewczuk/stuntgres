package core_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
)

func TestDatabaseDropObservers(t *testing.T) {
	e, peer, admin := databaseDropOwnerFixture(t)
	enginetest.Run(t, e, "CREATE TABLE drop_observer_backing(v integer)")
	enginetest.Run(t, e, "INSERT INTO drop_observer_backing VALUES(11)")
	enginetest.Run(t, peer, "CREATE TABLE drop_peer_backing(v integer)")
	peerOwner := enginetest.StartSession(t, peer, nil)
	local := statistics.Key{Object: enginetest.RoleTableRef(t, e, "drop_observer_backing"), Category: statistics.Relation}
	shared := statistics.Key{Object: e.Catalog.Domain.Ref(0, ids.ObjectAddress{ClassID: ids.DatabaseClassID}), Category: statistics.Database}
	sharedRelation := statistics.Key{Object: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1260}), Category: statistics.Relation}
	peerKey := statistics.Key{Object: enginetest.RoleTableRef(t, peer, "drop_peer_backing"), Category: statistics.Relation}
	if descriptor, ok := e.CatalogRead().Relation(sharedRelation.Object.Address); !ok || !catalog.SharedRelation(descriptor) {
		t.Fatal("fixture lacks real shared relation identity")
	}
	databaseResetSeedCore(e, map[statistics.Key]statistics.Fact{local: {Calls: 11}, shared: {Database: statistics.DatabaseFact{BlocksRead: 22}}, sharedRelation: {Calls: 33}})
	databaseResetSeedCore(peer, map[statistics.Key]statistics.Fact{peerKey: {Calls: 44}})
	var retained statistics.Reader
	retained.Configure("snapshot", 0)
	localBefore := retained.Read(&e.Statistics, local, time.Now())
	sharedBefore := retained.Read(&e.Statistics, shared, time.Now())
	relationBefore := retained.Read(&e.Statistics, sharedRelation, time.Now())
	resetBefore := e.Statistics.ReadShared(statistics.BGWriter)
	history := e.Cluster.StatementStatistics
	var late statistics.Writer
	unknown := statistics.Key{Object: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 99999}), Category: statistics.Function}
	late.Add(&e.Statistics, local, statistics.Fact{Calls: 100})
	late.Add(&e.Statistics, unknown, statistics.Fact{Calls: 200})
	domain, epoch := e.Catalog.Domain, e.CatalogRead().Epoch()
	databaseAlterEnd(t, admin)
	if _, err := databaseDropOwnerExecute(t.Context(), peer, peerOwner, &parser.DropDatabaseStmt{Name: e.DatabaseName}); err != nil {
		t.Fatal(err)
	}
	late.Flush(&e.Statistics, time.Now(), true)
	for _, key := range []statistics.Key{local, unknown} {
		if _, exists := e.Statistics.ReadEntry(key); exists {
			t.Fatal("late batch resurrected retired local key", key)
		}
	}
	if got := retained.Read(&e.Statistics, local, time.Now()); got != localBefore || got.Calls != 11 {
		t.Fatal("retirement mutated captured statistics reader", got)
	}
	if got := e.Statistics.Read(shared); got.Database != sharedBefore.Database {
		t.Fatal("default retirement reset OID-zero shared facts")
	}
	if got := e.Statistics.Read(sharedRelation); got.Calls != relationBefore.Calls {
		t.Fatal("default retirement reset shared relation partition")
	}
	if got := peer.Statistics.Read(peerKey); got.Calls != 44 || e.Cluster.StatementStatistics != history {
		t.Fatal("retirement lost peer/history authority")
	}
	if got := e.Statistics.ReadShared(statistics.BGWriter); got != resetBefore {
		t.Fatal("retirement reset shared family lineage")
	}
	// Shared-reset callers must still reach the nonselectable default holder.
	enginetest.SessionSQL(t, peerOwner, "SELECT pg_stat_reset_shared('bgwriter')")
	if got := e.Statistics.ReadShared(statistics.BGWriter); got.ResetAt.IsZero() || got.ResetAt == resetBefore.ResetAt {
		t.Fatal("shared reset omitted retained default authority")
	}
	enginetest.SessionSQL(t, peerOwner, "SELECT pg_stat_reset_single_table_counters(1260::oid)")
	if got := e.Statistics.Read(sharedRelation); got.Calls != 0 {
		t.Fatal("shared relation reset omitted retained old-epoch partition")
	}
	if got := retained.Read(&e.Statistics, sharedRelation, time.Now()); got != relationBefore {
		t.Fatal("shared reset mutated old Reader")
	}
	// The manager, not a parallel observer registry, owns terminal domain fences.
	if err := e.Locks.AdmitDomain(domain); err == nil {
		t.Fatal("retired domain readmitted")
	}
	if ticket, err := e.Locks.RegisterSession(domain.Session(enginetest.NextSessionID())); ticket != nil || err == nil {
		if ticket != nil {
			e.Locks.End(ticket)
		}
		t.Fatal("stale persistent ticket resurrected domain")
	}
	if records, err := e.Locks.PGLocks(domain, epoch); err != nil {
		t.Fatal(err)
	} else if len(records) != 0 {
		t.Fatal("retired domain retained lock inquiries", records)
	}
	if core.EngineDatabaseAdmissionCount(e) != 0 || len(e.Statistics.CaptureActivities()) != 1 {
		t.Fatal("default retirement lost peer activity or leaked occupancy")
	}
}
