package control

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func rangeIdentityAddress(id ids.OID) ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: id}
}
func rangeIdentityNamespace(id ids.OID) *catalog.Namespace {
	return &catalog.Namespace{ObjectAddress: rangeIdentityAddress(id), ObjectRevision: 1, ObjectName: "identity"}
}
func rangeIdentityEdge(a, b ids.OID) catalog.AliasOrderConstraint {
	point := func(id ids.OID) catalog.AliasOrderEndpoint {
		return catalog.AliasOrderEndpoint{Object: rangeIdentityAddress(id), OID: id, Domain: sqltype.Regnamespace}
	}
	return catalog.AliasOrderConstraint{Lower: point(a), Upper: point(b)}
}
func rangeIdentityAllocator(frontier ids.OID) func(ids.OID) (ids.OID, error) {
	return func(floor ids.OID) (ids.OID, error) {
		if frontier == ^ids.OID(0) {
			return 0, catalog.ErrObjectIdentityExhausted
		}
		frontier++
		if frontier < floor {
			frontier = floor
		}
		return frontier, nil
	}
}

func TestRangeTypeTemporaryEndpointComponents(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		next := catalog.NewState(1)
		old := []ids.OID{30000, 30010, 30020, 30100}
		if reverse {
			old = []ids.OID{30100, 30020, 30010, 30000}
		}
		for _, id := range old {
			d := rangeIdentityNamespace(id)
			next.SetDescriptor(d.Address(), d)
		}
		temporary := map[ids.ObjectAddress]catalog.Descriptor{}
		for _, id := range []ids.OID{30000, 30015} {
			d := rangeIdentityNamespace(id)
			temporary[d.Address()] = d
		}
		mapping, err := TemporaryCollisionMapping(context.Background(), next, temporary, []catalog.AliasOrderConstraint{rangeIdentityEdge(30000, 30010), rangeIdentityEdge(30010, 30020)}, rangeIdentityAllocator(30010))
		if err != nil {
			t.Fatal(err)
		}
		if len(mapping) != 2 || mapping[rangeIdentityAddress(30000)].ObjectID != 30011 || mapping[rangeIdentityAddress(30010)].ObjectID != 30012 {
			t.Fatal("affected component allocation", mapping)
		}
		if mapping[rangeIdentityAddress(30020)] != (ids.ObjectAddress{}) || mapping[rangeIdentityAddress(30100)] != (ids.ObjectAddress{}) {
			t.Fatal("legal originals/unrelated identities must remain", mapping)
		}
		for _, destination := range mapping {
			if temporary[destination] != nil {
				t.Fatal("TEMP became an allocation or source anchor", mapping)
			}
		}
	}
}

func TestRangeTypeTemporaryUnrelatedCollision(t *testing.T) {
	next := catalog.NewState(1)
	for _, id := range []ids.OID{30000, 30010} {
		d := rangeIdentityNamespace(id)
		next.SetDescriptor(d.Address(), d)
	}
	edge := rangeIdentityEdge(30010, 30011)
	edge.Upper.Object = ids.ObjectAddress{}
	temp := rangeIdentityNamespace(30000)
	mapping, err := TemporaryCollisionMapping(context.Background(), next, map[ids.ObjectAddress]catalog.Descriptor{temp.Address(): temp}, []catalog.AliasOrderConstraint{edge}, rangeIdentityAllocator(30020))
	if err != nil || len(mapping) != 1 || mapping[rangeIdentityAddress(30010)] != (ids.ObjectAddress{}) {
		t.Fatal("unrelated collision activated numeric component", mapping, err)
	}
}

func TestRangeTypeTemporaryPredecessorBacktracking(t *testing.T) {
	// This is the mapping API's structural contract, not a claim that a public
	// TEMP lifecycle exposes this particular allocator frontier/reservation set.
	next := catalog.NewState(1)
	for _, id := range []ids.OID{30000, 30100} {
		d := rangeIdentityNamespace(id)
		next.SetDescriptor(d.Address(), d)
	}
	temporary := make(map[ids.ObjectAddress]catalog.Descriptor)
	for id := ids.OID(30001); id <= 30100; id++ {
		d := rangeIdentityNamespace(id)
		temporary[d.Address()] = d
	}
	edge := rangeIdentityEdge(30100, 30101)
	edge.Upper.Object = ids.ObjectAddress{}
	mapping, err := TemporaryCollisionMapping(context.Background(), next, temporary, []catalog.AliasOrderConstraint{rangeIdentityEdge(30000, 30100), edge}, rangeIdentityAllocator(29990))
	if err != nil || mapping[rangeIdentityAddress(30000)].ObjectID != 29991 || mapping[rangeIdentityAddress(30100)].ObjectID != 29992 {
		t.Fatal("retain-first must backtrack to an earlier predecessor", mapping, err)
	}
}

func TestRangeTypeTemporaryUncollidedFastPath(t *testing.T) {
	next := catalog.NewState(1)
	d := rangeIdentityNamespace(30000)
	next.SetDescriptor(d.Address(), d)
	temp := rangeIdentityNamespace(30001)
	mapping, err := TemporaryCollisionMapping(context.Background(), next, map[ids.ObjectAddress]catalog.Descriptor{temp.Address(): temp}, nil, func(ids.OID) (ids.OID, error) { t.Fatal("no collision allocated an identity"); return 0, nil })
	if err != nil || mapping != nil {
		t.Fatal("uncollided generation fast path", mapping, err)
	}
}

func TestRangeTypeTemporarySelectiveWithoutOrder(t *testing.T) {
	next := catalog.NewState(1)
	for _, id := range []ids.OID{30000, 30010} {
		d := rangeIdentityNamespace(id)
		next.SetDescriptor(d.Address(), d)
	}
	temp := rangeIdentityNamespace(30000)
	mapping, err := TemporaryCollisionMapping(context.Background(), next, map[ids.ObjectAddress]catalog.Descriptor{temp.Address(): temp}, nil, rangeIdentityAllocator(30010))
	if err != nil || len(mapping) != 1 || mapping[rangeIdentityAddress(30000)].ObjectID != 30011 || mapping[rangeIdentityAddress(30010)] != (ids.ObjectAddress{}) {
		t.Fatal("non-range import lost selective predecessor inversion", mapping, err)
	}
}

func TestRangeTypeTemporaryIntervalCapacityAndCancellation(t *testing.T) {
	next := catalog.NewState(1)
	temporary := make(map[ids.ObjectAddress]catalog.Descriptor)
	for _, id := range []ids.OID{30000, 30001} {
		d := rangeIdentityNamespace(id)
		next.SetDescriptor(d.Address(), d)
		temporary[d.Address()] = d
	}
	var edges []catalog.AliasOrderConstraint
	for _, id := range []ids.OID{30000, 30001} {
		e := rangeIdentityEdge(id, 30003)
		e.Upper.Object = ids.ObjectAddress{}
		edges = append(edges, e)
	}
	_, err := TemporaryCollisionMapping(context.Background(), next, temporary, edges, rangeIdentityAllocator(30001))
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22000" {
		t.Fatal("two groups cannot share the only legal slot", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = TemporaryCollisionMapping(ctx, next, temporary, edges, rangeIdentityAllocator(30001))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("planning cancellation", err)
	}
}
