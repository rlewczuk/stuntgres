package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangeTypeAliasNumericAnchorAtomicity(t *testing.T) {
	view, r, _ := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	next := catalog.CloneState(view.State())
	target := New()
	original := target.catalog.State
	target.mu.Lock()
	allocate := func() ids.OID {
		id, err := target.catalog.AllocateOIDLocked()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := allocate(), allocate()
	lo, numeric := allocate(), allocate()
	target.mu.Unlock()
	if numeric != lo+1 {
		t.Fatal("real retained TEMP prefix must expose the adjacent nonrelation identity", lo, numeric)
	}
	namespace := func(id ids.OID, name string) *catalog.Namespace {
		return &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: id}, ObjectRevision: 1, ObjectName: name}
	}
	a, b := namespace(first, "pg_temp_1"), namespace(second, "pg_toast_temp_1")
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: lo}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, ObjectName: "lo", RelationKind: catalog.TableRelation}
	next.SetDescriptor(relation.Address(), relation)
	temp := *relation
	temp.ObjectNamespace = a.Address()
	temp.ObjectName = "occupied"
	rowType, err := sqltype.Composite(sqltype.ID(numeric), "occupied", "pg_temp_1.occupied", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := &catalog.Type{SQLType: rowType, ObjectNamespace: a.Address(), ObjectRevision: 1, OwningRelation: temp.Address()}
	retained := map[ids.ObjectAddress]catalog.Descriptor{a.Address(): a, b.Address(): b, temp.Address(): &temp, row.Address(): row}
	for _, upper := range []ids.OID{numeric, ^ids.OID(0)} {
		source := rangeTypeUnitInput(t, r, fmt.Sprintf("[%d,%d]", lo, upper))
		rows := map[ids.ObjectAddress]*storage.Relation{relation.Address(): {Versions: []storage.RowVersion{{Values: []sqltype.Value{source}}}}}
		constraints, err := stored.RangeIdentityConstraints(context.Background(), next, rows)
		if err != nil {
			t.Fatal(err)
		}
		target.mu.Lock()
		mapping, err := control.TemporaryCollisionMapping(context.Background(), next, retained, constraints, target.catalog.AllocateOIDAtLeastLocked)
		target.mu.Unlock()
		if upper == numeric {
			var diagnostic interface{ SQLState() string }
			if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22000" || !strings.Contains(err.Error(), fmt.Sprint(numeric)) {
				t.Fatal("exhausted structural choices must reject before applying any identity map", mapping, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if mapping[relation.Address()].ObjectID <= numeric {
			t.Fatal("monotonic allocator reused retained prefix", mapping)
		}
		for _, destination := range mapping {
			if retained[destination] != nil {
				t.Fatal("reassigned retained TEMP", destination)
			}
		}
		mapped, err := regobj.RemapClassDatum(source, func(id ids.OID) ids.OID {
			if destination, ok := mapping[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}]; ok {
				return destination.ObjectID
			}
			return id
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mapped.ValidateMappedRanges(context.Background()); err != nil {
			t.Fatal("large numeric upper", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := mapped.ValidateMappedRanges(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("comparison repair lost cancellation", err)
		}
	}
	if target.catalog.State != original {
		t.Fatal("failed detached mapping/validation published a catalog root")
	}
	if next.Get(relation.Address()) != relation || retained[temp.Address()] != &temp {
		t.Fatal("identity planning mutated source or retained generations")
	}
}
