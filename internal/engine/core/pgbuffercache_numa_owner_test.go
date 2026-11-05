package core

import (
	"strings"
	"testing"
)

func TestPgBuffercacheNUMAOwnerBoundary(t *testing.T) {
	e, err := NewEngine(nil, nil, "numa_owner", Startup{})
	if err != nil { t.Fatal(err) }
	foreign, err := NewEngine(nil, nil, "numa_foreign", Startup{})
	if err != nil { t.Fatal(err) }
	cluster := e.Cluster
	e.Mu.Lock()
	defer e.Mu.Unlock()
	defer func() { e.Cluster = cluster }()
	if cluster.buffercache != nil { t.Fatal("construction allocated topology or occupancy") }
	for _, owner := range []*DatabaseCluster{nil, foreign.Cluster} {
		e.Cluster = owner
		capacity, err := e.logicalBufferNUMACapacityLocked()
		if err == nil || capacity != 0 || !strings.Contains(err.Error(), "registered database runtime") {
			t.Fatal("private/foreign runtime acquired successful topology", capacity, err)
		}
		if cluster.buffercache != nil || foreign.Cluster.buffercache != nil { t.Fatal("rejected owner allocated a cache") }
	}
	e.Cluster = cluster
	database := e.Catalog.Current().MustDatabase(e.Catalog.DatabaseAddress())
	delete(cluster.Targets, database.Name())
	defer func() { cluster.Targets[database.Name()] = e }()
	if capacity, err := e.logicalBufferNUMACapacityLocked(); err == nil || capacity != 0 { t.Fatal("unattached runtime acquired topology", capacity, err) }
	if cluster.buffercache != nil { t.Fatal("unattached runtime allocated a cache") }
	cluster.Targets[database.Name()] = e
	capacity, err := e.logicalBufferNUMACapacityLocked()
	if err != nil || capacity <= 0 || cluster.buffercache == nil { t.Fatal("registered lazy capacity", capacity, err) }
	cache := cluster.buffercache
	for range 3 {
		got, err := e.logicalBufferNUMACapacityLocked()
		if err != nil || got != capacity || cluster.buffercache != cache || cache.byKey != nil || cache.bySlot != nil || cache.cursor != 0 || cache.nextFree != 0 {
			t.Fatal("capacity observation activated sparse occupancy", got, err, cache)
		}
	}
}
