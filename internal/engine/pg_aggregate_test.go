package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGAggregateCaptureOwnership(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600}
	source := catalog.MustRelation(address)
	ref := e.catalog.domain.Ref(catalog.Epoch(), address)
	if source.rowType != 10027 || source.virtualView || !source.hasIndexes || len(source.columns) != 22 {
		t.Fatal("aggregate source descriptor", source)
	}
	row := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10027})
	array := catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10026})
	if row.owningRelation != address || array.owningRelation != address || row.array != 10026 || array.typ.Element() != row.typ || row.owner != source.owner || array.owner != source.owner {
		t.Fatal("aggregate row/array ownership")
	}
	nextOID, nextRevision := e.catalog.nextOID, e.catalog.nextRevision
	captured, err := capturePGAggregate(context.Background(), catalog, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	if captured.ref != ref || len(captured.anonymous) != 144 {
		t.Fatal("aggregate owner inventory", captured)
	}
	for _, values := range captured.anonymous {
		if len(values) != 22 {
			t.Fatal("definition width", len(values))
		}
		for i, v := range values {
			if v.Type() != source.columns[i].Usage.Type || i < 20 && v.IsNull() {
				t.Fatal("typed aggregate field", i, v)
			}
		}
	}
	for i, c := range source.columns {
		collation := uint32(0)
		if i >= 20 {
			collation = 950
		}
		if c.OriginTable != 2600 || c.OriginAttribute != int16(i+1) || !c.NullabilityKnown || c.Nullable != (i >= 20) || c.Usage.Collation.ID != collation || source.catalogAttributes[i].notNull != (i < 20) {
			t.Fatal("aggregate attribute facts", i, c)
		}
	}
	second, err := capturePGAggregate(context.Background(), catalog, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	second.anonymous[0][0] = sqltype.Null(source.columns[0].Usage.Type)
	second.anonymous[1][1] = sqltype.Null(source.columns[1].Usage.Type)
	if captured.anonymous[0][0].IsNull() || captured.anonymous[1][1].IsNull() || second.anonymous[0][1].IsNull() {
		t.Fatal("aggregate row slots alias")
	}
	if e.catalog.state != catalog.state || e.catalog.nextOID != nextOID || e.catalog.nextRevision != nextRevision {
		t.Fatal("catalog observation changed frontiers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := capturePGAggregate(ctx, catalog, ref, source); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source publication", got, err)
	}
	foreign := New().CatalogRead()
	badRef := ref
	badRef.Epoch++
	for _, tc := range []struct {
		catalog CatalogRead
		ref     ids.ObjectRef
		source  *relationDescriptor
	}{
		{CatalogRead{}, ref, source}, {foreign, ref, source}, {catalog, badRef, source}, {catalog, ref, nil},
	} {
		if got, err := capturePGAggregate(context.Background(), tc.catalog, tc.ref, tc.source); got != nil || err == nil {
			t.Fatal("foreign/stale source admitted", got, err)
		}
	}
	for i := range source.columns {
		state := cloneCatalog(catalog.state)
		bad := *source
		bad.columns = append([]ColumnDescriptor(nil), source.columns...)
		bad.columns[i].Usage.Type, _ = foreign.LookupType(source.columns[i].Usage.Type.ID())
		state.descriptors[address] = &bad
		if got, err := capturePGAggregate(context.Background(), e.catalog.read(state), ref, &bad); got != nil || err == nil {
			t.Fatal("foreign column generation admitted", i, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := e.CatalogRead()
	newSource := fresh.MustRelation(address)
	if got, err := capturePGAggregate(context.Background(), fresh, ref, source); got != nil || err == nil {
		t.Fatal("retired source admitted", err)
	}
	newRef := e.catalog.domain.Ref(fresh.Epoch(), address)
	if got, err := capturePGAggregate(context.Background(), fresh, newRef, newSource); err != nil || len(got.anonymous) != 144 {
		t.Fatal("Reset lost builtin definitions", err)
	}
	for i, v := range captured.anonymous[0] {
		if v.Type() != source.columns[i].Usage.Type {
			t.Fatal("retained capture retagged", i)
		}
	}
}

func TestPGAggregateReleasedOwnership(t *testing.T) {
	if len(builtinAggregateDefinitions) != 144 {
		t.Fatal("owned aggregate signatures", len(builtinAggregateDefinitions))
	}
	seen := make(map[ids.OID]bool)
	for _, d := range builtinAggregateDefinitions {
		identity, ok := routineByOID[d.function]
		if !ok || seen[d.function] || identity.namespace != ids.PGCatalogNamespaceOID {
			t.Fatal("unowned/duplicate aggregate signature", d.function)
		}
		seen[d.function] = true
		for _, id := range d.functions {
			if id != 0 {
				if _, ok := routineByOID[id]; !ok {
					t.Fatal("unknown released helper", d.function, id)
				}
			}
		}
	}
	for _, id := range []ids.OID{2901, 2125, 2141, 2050, 2051, 6373, 6374, 2797, 2798, 3526, 3527, 3564, 3565, 4189, 4190, 5099, 5100, 3101, 3102, 3103, 3104, 2333, 2334} {
		if seen[id] {
			t.Fatal("unimplemented/scalar/helper aggregate admitted", id)
		}
	}
	// NULL/empty/text-zero seeds are distinct states, including typed NULL capture.
	e := New()
	catalog := e.CatalogRead()
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600})
	ref := e.catalog.domain.Ref(catalog.Epoch(), source.address)
	got, err := capturePGAggregate(context.Background(), catalog, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got.anonymous {
		id, _ := r[0].ProcedureOID()
		if id == 2803 {
			if r[20].IsNull() || r[21].IsNull() {
				t.Fatal("count seeds became NULL")
			}
			text, err := r[20].Text()
			if err != nil || text != "0" {
				t.Fatal("count seed", text, err)
			}
		}
		if id == 2335 && (!r[20].IsNull() || !r[21].IsNull()) {
			t.Fatal("array NULL seeds defaulted")
		}
	}
}
