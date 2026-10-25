package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangeTypeAliasMaximumRetainedEndpointCapacity(t *testing.T) {
	for _, mode := range []string{"endpoint_only", "with_ordinary"} {
		t.Run(mode, func(t *testing.T) {
			ordinary := mode == "with_ordinary"
			ctx := context.Background()
			view, r, _ := rangeTypeUnitFamily(t, sqltype.Regclass, false)
			next := catalog.CloneState(view.State())
			address := func(class, id ids.OID) ids.ObjectAddress { return ids.ObjectAddress{ClassID: class, ObjectID: id} }
			lower := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 30000), ObjectNamespace: address(ids.NamespaceClassID, ids.PublicNamespaceOID), ObjectRevision: 1, ObjectName: "lower_endpoint", RelationKind: catalog.TableRelation}
			upper := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, ^ids.OID(0)), ObjectNamespace: lower.Namespace(), ObjectRevision: 1, ObjectName: "maximum_endpoint", RelationKind: catalog.TableRelation}
			unrelated := &catalog.Namespace{ObjectAddress: address(ids.NamespaceClassID, 30001), ObjectRevision: 1, ObjectName: "unrelated_import"}
			for _, d := range []catalog.Descriptor{lower, upper} {
				next.SetDescriptor(d.Address(), d)
			}
			if ordinary {
				next.SetDescriptor(unrelated.Address(), unrelated)
			}
			value := rangeTypeUnitInput(t, r, "[30000,4294967295]")
			rows := map[ids.ObjectAddress]*storage.Relation{lower.Address(): {Versions: []storage.RowVersion{{Values: []sqltype.Value{value}}}}}
			constraints, err := stored.RangeIdentityConstraints(ctx, next, rows)
			if err != nil {
				t.Fatal(err)
			}

			target := New()
			target.Mu.Lock()
			_, err = target.Catalog.AllocateOIDAtLeastLocked(29999)
			target.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			op, done := roleOperation(t, target, "postgres")
			defer done()
			roleExecute(t, target, op, "CREATE TEMP TABLE capacity_live(k integer)")
			roleExecute(t, target, op, "INSERT INTO capacity_live VALUES(7)")
			retained, err := control.TemporarySet(target.CatalogRead(), next)
			if err != nil {
				t.Fatal(err)
			}
			// These are real session-owned TEMP namespaces created by the ordinary DDL
			// path. The source endpoint and unrelated import collide across catalog
			// classes exactly as the production global reservation policy requires.
			if retained[address(ids.NamespaceClassID, 30000)] == nil || retained[unrelated.Address()] == nil {
				t.Fatal("missing actual TEMP prefix collision", retained)
			}
			before := target.Catalog.State
			target.Mu.Lock()
			_, err = target.Catalog.AllocateOIDAtLeastLocked(30010)
			if err != nil {
				target.Mu.Unlock()
				t.Fatal(err)
			}
			mapping, err := control.TemporaryCollisionMapping(ctx, next, retained, constraints, target.Catalog.AllocateOIDAtLeastLocked)
			target.Mu.Unlock()
			if err != nil {
				t.Fatal("feasible endpoint plus ordinary collision must not exhaust speculative alternatives", err)
			}
			if mapping[upper.Address()] != (ids.ObjectAddress{}) {
				t.Fatal("noncolliding maximum real endpoint must retain its identity", mapping)
			}
			a, b := mapping[lower.Address()], mapping[unrelated.Address()]
			if a.ObjectID != 30011 || (ordinary && b.ObjectID != 30012) || (!ordinary && b != (ids.ObjectAddress{})) {
				t.Fatal("complete demand must stop at its first feasible assignment", mapping)
			}
			for _, destination := range mapping {
				for retainedAddress := range retained {
					if destination.ObjectID == retainedAddress.ObjectID {
						t.Fatal("mapped onto retained TEMP identity", destination)
					}
				}
			}
			if target.Catalog.State != before || next.Get(lower.Address()) != lower || next.Get(upper.Address()) != upper || (ordinary && next.Get(unrelated.Address()) != unrelated) {
				t.Fatal("planning mutated source or target generations")
			}
			if data := tuples(roleExecute(t, target, op, "SELECT k FROM capacity_live")); len(data) != 1 || data[0][0] != int64(7) {
				t.Fatal("retained TEMP data changed", data)
			}
			// Positive planning may not leave future ordinary allocation artificially
			// exhausted just because a retainable endpoint happens to equal MAXOID.
			expectedNext := ids.OID(30012)
			if ordinary {
				expectedNext = 30013
			}
			target.Mu.Lock()
			fresh, err := target.Catalog.AllocateOIDLocked()
			target.Mu.Unlock()
			if err != nil || fresh != expectedNext {
				t.Fatal("speculative alternatives consumed future capacity", fresh, expectedNext, err)
			}
			roleExecute(t, target, op, "CREATE TABLE capacity_after_plan(k integer)")
			rangeTypeUnitOutput(t, value, "[30000,4294967295]")
		})
	}
}

func TestRangeTypeAliasCompleteCapacityLimits(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(fmt.Sprint("exhausted_", exhausted), func(t *testing.T) {
			ctx := context.Background()
			view, r, _ := rangeTypeUnitFamily(t, sqltype.Regclass, false)
			next := catalog.CloneState(view.State())
			lower := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 30000}, ObjectRevision: 1, ObjectName: "lower", RelationKind: catalog.TableRelation}
			upper := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ^ids.OID(0)}, ObjectRevision: 1, ObjectName: "upper", RelationKind: catalog.TableRelation}
			ordinary := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30001}, ObjectRevision: 1, ObjectName: "ordinary"}
			for _, d := range []catalog.Descriptor{lower, upper, ordinary} {
				next.SetDescriptor(d.Address(), d)
			}
			value := rangeTypeUnitInput(t, r, "[30000,4294967295]")
			constraints, err := stored.RangeIdentityConstraints(ctx, next, map[ids.ObjectAddress]*storage.Relation{lower.Address(): {Versions: []storage.RowVersion{{Values: []sqltype.Value{value}}}}})
			if err != nil {
				t.Fatal(err)
			}
			target := New()
			target.Mu.Lock()
			_, err = target.Catalog.AllocateOIDAtLeastLocked(29999)
			target.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			op, done := roleOperation(t, target, "postgres")
			defer done()
			roleExecute(t, target, op, "CREATE TEMP TABLE finite_capacity(k integer)")
			retained, err := control.TemporarySet(target.CatalogRead(), next)
			if err != nil {
				t.Fatal(err)
			}
			before := target.Catalog.State
			target.Mu.Lock()
			floor := ^ids.OID(0) - 2
			if exhausted {
				floor = ^ids.OID(0)
			}
			_, err = target.Catalog.AllocateOIDAtLeastLocked(floor)
			if err != nil {
				target.Mu.Unlock()
				t.Fatal(err)
			}
			mapping, err := control.TemporaryCollisionMapping(ctx, next, retained, constraints, target.Catalog.AllocateOIDAtLeastLocked)
			target.Mu.Unlock()
			var diagnostic interface{ SQLState() string }
			if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22000" || mapping != nil {
				t.Fatal("two fresh demands cannot share one remaining unreserved output", mapping, err)
			}
			if target.Catalog.State != before || next.Get(lower.Address()) != lower {
				t.Fatal("capacity failure published a generation")
			}
			target.Mu.Lock()
			_, err = target.Catalog.AllocateOIDAtLeastLocked(1)
			target.Mu.Unlock()
			if !errors.Is(err, catalog.ErrObjectIdentityExhausted) {
				t.Fatal("failed search rewound/revived exhausted frontier", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			target.Mu.Lock()
			_, err = control.TemporaryCollisionMapping(canceled, next, retained, constraints, target.Catalog.AllocateOIDAtLeastLocked)
			target.Mu.Unlock()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("capacity search lost cancellation", err)
			}
		})
	}
}

func TestRangeTypeAliasSparseCapacity(t *testing.T) {
	// Owning mapping-API fixture: this intentionally positioned retained identity
	// is not a claim about a public SQL TEMP allocator sequence.
	ctx := context.Background()
	view, r, _ := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	next := catalog.CloneState(view.State())
	upper := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 4000000100}, ObjectRevision: 1, ObjectName: "sparse_upper", RelationKind: catalog.TableRelation}
	next.SetDescriptor(upper.Address(), upper)
	value := rangeTypeUnitInput(t, r, "[4000000000,4000000100]")
	constraints, err := stored.RangeIdentityConstraints(ctx, next, map[ids.ObjectAddress]*storage.Relation{upper.Address(): {Versions: []storage.RowVersion{{Values: []sqltype.Value{value}}}}})
	if err != nil {
		t.Fatal(err)
	}
	temp := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 4000000100}, ObjectRevision: 1, ObjectName: "pg_temp_sparse"}
	retained := map[ids.ObjectAddress]catalog.Descriptor{temp.Address(): temp}
	target := New()
	target.Mu.Lock()
	_, err = target.Catalog.AllocateOIDAtLeastLocked(30010)
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	mapping, err := control.TemporaryCollisionMapping(ctx, next, retained, constraints, target.Catalog.AllocateOIDAtLeastLocked)
	fresh, nextErr := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil || mapping[upper.Address()].ObjectID != 4000000001 || nextErr != nil || fresh != 4000000002 {
		t.Fatal("required sparse interval must use one real floor-aware output then stop", mapping, err, fresh, nextErr)
	}
	if next.Get(upper.Address()) != upper || retained[temp.Address()] != temp {
		t.Fatal("sparse planning mutated immutable identities")
	}
}
