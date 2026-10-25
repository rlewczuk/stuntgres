package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGAggregateCaptureOwnership(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600}
	source := view.MustRelation(address)
	ref := e.Catalog.Domain.Ref(view.Epoch(), address)
	if source.RowType != 10027 || source.VirtualView || !source.History.HasIndexes || len(source.Columns) != 22 {
		t.Fatal("aggregate source descriptor", source)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10027})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10026})
	if row.OwningRelation != address || array.OwningRelation != address || row.Array != 10026 || array.SQLType.Element() != row.SQLType || row.Owner != source.Owner || array.Owner != source.Owner {
		t.Fatal("aggregate row/array ownership")
	}
	nextOID, nextRevision := e.Catalog.NextOID(), e.Catalog.NextRevision()
	captured, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Ref != ref || len(captured.Anonymous) != 144 {
		t.Fatal("aggregate owner inventory", captured)
	}
	for _, values := range captured.Anonymous {
		if len(values) != 22 {
			t.Fatal("definition width", len(values))
		}
		for i, v := range values {
			if v.Type() != source.Columns[i].Usage.Type || i < 20 && v.IsNull() {
				t.Fatal("typed aggregate field", i, v)
			}
		}
	}
	for i, c := range source.Columns {
		collation := uint32(0)
		if i >= 20 {
			collation = 950
		}
		if c.OriginTable != 2600 || c.OriginAttribute != int16(i+1) || !c.NullabilityKnown || c.Nullable != (i >= 20) || c.Usage.Collation.ID != collation || source.ColumnMeta.CatalogAttributes[i].NotNull != (i < 20) {
			t.Fatal("aggregate attribute facts", i, c)
		}
	}
	second, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate)
	if err != nil {
		t.Fatal(err)
	}
	second.Anonymous[0][0] = sqltype.Null(source.Columns[0].Usage.Type)
	second.Anonymous[1][1] = sqltype.Null(source.Columns[1].Usage.Type)
	if captured.Anonymous[0][0].IsNull() || captured.Anonymous[1][1].IsNull() || second.Anonymous[0][1].IsNull() {
		t.Fatal("aggregate row slots alias")
	}
	if e.Catalog.State != view.State() || e.Catalog.NextOID() != nextOID || e.Catalog.NextRevision() != nextRevision {
		t.Fatal("catalog observation changed frontiers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source publication", got, err)
	}
	foreign := engine.New().CatalogRead()
	badRef := ref
	badRef.Epoch++
	for _, tc := range []struct {
		catalog catalog.Read
		ref     ids.ObjectRef
		source  *catalog.Relation
	}{
		{catalog.Read{}, ref, source}, {foreign, ref, source}, {view, badRef, source}, {view, ref, nil},
	} {
		if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: tc.catalog, Ref: tc.ref, Source: tc.source, Owner: (tc.catalog).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate); got != nil || err == nil {
			t.Fatal("foreign/stale source admitted", got, err)
		}
	}
	for i := range source.Columns {
		state := catalog.CloneState(view.State())
		bad := *source
		bad.Columns = append([]catalog.Column(nil), source.Columns...)
		bad.Columns[i].Usage.Type, _ = foreign.LookupType(source.Columns[i].Usage.Type.ID())
		state.SetDescriptor(address, &bad)
		if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.Catalog.Read(state), Ref: ref, Source: &bad, Owner: (e.Catalog.Read(state)).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate); got != nil || err == nil {
			t.Fatal("foreign column generation admitted", i, err)
		}
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	fresh := e.CatalogRead()
	newSource := fresh.MustRelation(address)
	if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: fresh, Ref: ref, Source: source, Owner: (fresh).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate); got != nil || err == nil {
		t.Fatal("retired source admitted", err)
	}
	newRef := e.Catalog.Domain.Ref(fresh.Epoch(), address)
	if got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: fresh, Ref: newRef, Source: newSource, Owner: (fresh).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate); err != nil || len(got.Anonymous) != 144 {
		t.Fatal("Reset lost builtin definitions", err)
	}
	for i, v := range captured.Anonymous[0] {
		if v.Type() != source.Columns[i].Usage.Type {
			t.Fatal("retained capture retagged", i)
		}
	}
}

func TestPGAggregateReleasedOwnership(t *testing.T) {
	if len(pgcatalog.AggregateDefinitions) != 144 {
		t.Fatal("owned aggregate signatures", len(pgcatalog.AggregateDefinitions))
	}
	seen := make(map[ids.OID]bool)
	for _, d := range pgcatalog.AggregateDefinitions {
		identity, ok := catalog.BuiltinRoutineIdentityByOID(d.Function)
		if !ok || seen[d.Function] || identity.Namespace != ids.PGCatalogNamespaceOID {
			t.Fatal("unowned/duplicate aggregate signature", d.Function)
		}
		seen[d.Function] = true
		for _, id := range d.Functions {
			if id != 0 {
				if _, ok := catalog.BuiltinRoutineIdentityByOID(id); !ok {
					t.Fatal("unknown released helper", d.Function, id)
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
	e := engine.New()
	catalog := e.CatalogRead()
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600})
	ref := e.Catalog.Domain.Ref(catalog.Epoch(), source.Address())
	got, err := engine.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: catalog, Ref: ref, Source: source, Owner: (catalog).Owner(), Runtime: engine.EngineCatalogRuntime{}}, capturePGAggregate)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got.Anonymous {
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

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = registeredCatalogProducer(12081)
	captureDataTypePrivileges  = registeredCatalogProducer(16201)
	captureExtensionCatalog    = registeredCatalogProducer(1255)
	capturePGAM                = registeredCatalogProducer(2601)
	capturePGAggregate         = registeredCatalogProducer(2600)
	capturePGAttribute         = registeredCatalogProducer(1249)
	capturePGCast              = registeredCatalogProducer(2605)
	capturePGCollation         = registeredCatalogProducer(3456)
	capturePGConstraint        = registeredCatalogProducer(2606)
	capturePGCursors           = registeredCatalogProducer(12077)
	capturePGDatabase          = registeredCatalogProducer(1262)
	capturePGDefaultACL        = registeredCatalogProducer(826)
	capturePGDepend            = registeredCatalogProducer(2608)
	capturePGDescription       = registeredCatalogProducer(2609)
	capturePGEnum              = registeredCatalogProducer(3501)
	capturePGGroup             = registeredCatalogProducer(12008)
	capturePGIndexes           = registeredCatalogProducer(12002)
	capturePGInherits          = registeredCatalogProducer(2611)
	capturePGLanguage          = registeredCatalogProducer(2612)
	capturePGNamespace         = registeredCatalogProducer(2615)
	capturePGOpclass           = registeredCatalogProducer(2616)
	capturePGOperator          = registeredCatalogProducer(2617)
	capturePGParameterACL      = registeredCatalogProducer(6243)
	capturePGRange             = registeredCatalogProducer(3541)
	capturePGRoles             = registeredCatalogProducer(12007)
	capturePGSequence          = registeredCatalogProducer(2224)
	capturePGSettings          = registeredCatalogProducer(16202)
	capturePGShDescription     = registeredCatalogProducer(2396)
	capturePGTablespace        = registeredCatalogProducer(1213)
	capturePGType              = registeredCatalogProducer(1247)
	capturePGUser              = registeredCatalogProducer(12014)
	capturePreparedSource      = registeredCatalogProducer(12001)
	captureTablePrivileges     = registeredCatalogProducer(16200)
	captureTableViewSummary    = registeredCatalogProducer(12000)
	captureTimezoneSource      = registeredCatalogProducer(12004)
)

func registeredCatalogProducer(oid ids.OID) pgcatalog.Producer {
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		panic(fmt.Sprintf("no catalog producer registered for OID %d", oid))
	}
	return produce
}
