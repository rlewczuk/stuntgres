package core

import (
	"strings"
	"testing"
)

func TestPgBuffercachePrivateOwnerBoundary(t *testing.T) {
	e, err := NewEngine(nil, nil, "buffer_owner", Startup{})
	if err != nil {
		t.Fatal(err)
	}
	cluster := e.Cluster
	e.Mu.Lock()
	defer e.Mu.Unlock()
	defer func() { e.Cluster = cluster }()
	if cluster.buffercache != nil {
		t.Fatal("construction allocated a live cache")
	}

	// A private template destination deliberately has no cluster. Publication
	// must not manufacture state; source capture cannot lend it a live lease.
	e.Cluster = nil
	e.reconcileLogicalBuffersLocked()
	if key, eligible := e.logicalBufferReadKeyLocked(nil, e.Catalog.Current(), e.Catalog.DatabaseAddress()); eligible || key != (logicalBufferKey{}) {
		t.Fatal("private staging acquired a source cache identity")
	}
	if snapshot, err := e.snapshotLogicalBuffersLocked(); err == nil || snapshot.capacity != 0 || snapshot.rows != nil || !strings.Contains(err.Error(), "registered database runtime") {
		t.Fatal("private snapshot fabricated a successful observation", snapshot, err)
	}
	if cluster.buffercache != nil {
		t.Fatal("private staging allocated shared occupancy")
	}

	// An old operational bridge cannot activate while its runtime has no owner.
	// Open must reject that state before consulting its catalog/registration.
	bridge := &logicalBufferRead{engine: e}
	e.Mu.Unlock()
	bridge.Open()
	bridge.Close()
	e.Mu.Lock()
	if bridge.opened || bridge.entry != nil || bridge.cache != nil {
		t.Fatal("absent owner retained a bridge", bridge)
	}

	e.Cluster = cluster
	database := e.Catalog.Current().MustDatabase(e.Catalog.DatabaseAddress())
	delete(cluster.Targets, database.Name())
	defer func() { cluster.Targets[database.Name()] = e }()
	if snapshot, err := e.snapshotLogicalBuffersLocked(); err == nil || snapshot.capacity != 0 || snapshot.rows != nil {
		t.Fatal("unregistered live pointer fabricated an observation", snapshot, err)
	}
	if cluster.buffercache != nil {
		t.Fatal("unregistered runtime allocated cache state")
	}
	cluster.Targets[database.Name()] = e
	if snapshot, err := e.snapshotLogicalBuffersLocked(); err != nil || snapshot.capacity <= 0 || snapshot.rows != nil || cluster.buffercache == nil {
		t.Fatal("registered runtime failed cold lazy capture", snapshot, err)
	}
}
