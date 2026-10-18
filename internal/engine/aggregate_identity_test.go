package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGAggregateIntaggOwnership(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.state.descriptors[ext.members[0]].(*procedureDescriptor)
	enum := view.state.descriptors[ext.members[1]].(*procedureDescriptor)
	if len(ext.members) != 2 || proc.aggregate == nil || enum.aggregate != nil {
		t.Fatal("callable/aggregate owner inventory")
	}
	if err := stockIntagg.validate(view, ext); err != nil {
		t.Fatal(err)
	}
	if err := validateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	for i, helper := range proc.aggregate.helpers {
		if helper.id == proc.address.ObjectID || helper.id == enum.address.ObjectID || helper.id == 2333 || helper.id == 2334 || helper.id == 0 {
			t.Fatal("helper identity substituted", helper)
		}
		identity, ok := routineIdentityByOID(view, helper.id)
		if !ok || identity != helper || routineDescriptorOwner(view, helper.id) != proc {
			t.Fatal("owned alias lookup", i, identity)
		}
		selected, n := selectExactRoutine(view, namespaceSelection{qualified: proc.namespace}, helper.name, helper.signature)
		if n != 1 || selected != helper {
			t.Fatal("owned exact signature", selected, n)
		}
		selected, n = selectRoutine(view, namespaceSelection{qualified: proc.namespace}, helper.name, nil)
		if n != 1 || selected != helper {
			t.Fatal("owned bare alias", selected, n)
		}
		if procedureInNamespaces(view, namespaceSelection{qualified: proc.namespace}, helper.name, helper.signature) != nil {
			t.Fatal("metadata helper became callable")
		}
		reserved := make(map[ids.OID]bool)
		reserveDefinitionOIDs(reserved, proc)
		if !reserved[helper.id] || !definitionOIDUsed(proc, helper.id) {
			t.Fatal("allocator lost owned helper", helper.id)
		}
		typ, _ := view.LookupType(sqltype.Regprocedure)
		value, err := sqltype.RegprocValue(typ, uint32(helper.id))
		if err != nil {
			t.Fatal(err)
		}
		var refs []ids.ObjectAddress
		if err := walkRoutineReferences(view, value, func(a ids.ObjectAddress) error { refs = append(refs, a); return nil }); err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{proc.address}) {
			t.Fatal("helper dependency is not real aggregate owner", refs, err)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	typ, _ := view.LookupType(sqltype.Regprocedure)
	if _, err := b.routineConstant("public.int_agg_state(internal,integer)", typ.Usage(), parser.Position{Offset: 8, Character: 8}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operand := range b.regtypeConstants[0].operands {
		if operand.ref.Address == proc.address && operand.revision == proc.revision {
			found = true
		}
	}
	if !found {
		t.Fatal("prepared helper literal omitted real owner dependency")
	}
	source := view.state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600}].(*relationDescriptor)
	ref := e.catalog.domain.Ref(view.Epoch(), source.address)
	rows, err := capturePGAggregate(context.Background(), view, ref, source)
	if err != nil || len(rows.anonymous) != 145 {
		t.Fatal("installed aggregate source", err)
	}
	removed := cloneCatalog(view.state)
	delete(removed.descriptors, proc.address)
	delete(removed.names, descriptorNameKey(proc))
	retired := e.catalog.read(removed)
	for _, helper := range proc.aggregate.helpers {
		if _, ok := routineIdentityByOID(retired, helper.id); ok {
			t.Fatal("retired helper still resolves")
		}
	}
	// Old capture ownership remains intact independently of current owner removal.
	if id, err := rows.anonymous[len(rows.anonymous)-1][0].ProcedureOID(); err != nil || id != uint32(proc.address.ObjectID) {
		t.Fatal("retained aggregate row lost owner", id, err)
	}
}

func TestPGAggregateIntaggValidation(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.state.descriptors[ext.members[0]].(*procedureDescriptor)
	for _, mutate := range []func(*ownedAggregate){
		func(a *ownedAggregate) { a.helpers[0].id = 0 },
		func(a *ownedAggregate) {
			a.helpers[0].id = a.helpers[1].id
			a.definition.functions[0] = a.helpers[0].id
		},
		func(a *ownedAggregate) { a.helpers[0].id = 2333; a.definition.functions[0] = 2333 },
		func(a *ownedAggregate) {
			a.helpers[0].id = ext.members[1].ObjectID
			a.definition.functions[0] = a.helpers[0].id
		},
		func(a *ownedAggregate) { a.helpers[0].namespace = 0 },
		func(a *ownedAggregate) { a.helpers[0].name = "array_agg_transfn" },
		func(a *ownedAggregate) { a.helpers[1].signature = "23" },
		func(a *ownedAggregate) { a.definition.function++ },
		func(a *ownedAggregate) { a.definition.functions[2] = 463 },
		func(a *ownedAggregate) { a.definition.hasInitial = true },
	} {
		bad := *proc
		owned := *proc.aggregate
		mutate(&owned)
		bad.aggregate = &owned
		state := cloneCatalog(view.state)
		state.descriptors[proc.address] = &bad
		if err := stockIntagg.validate(e.catalog.read(state), ext); err == nil {
			t.Fatal("invalid owned aggregate admitted", owned)
		}
	}
	for _, member := range ext.members {
		bad := *view.state.descriptors[member].(*procedureDescriptor)
		if member == proc.address {
			bad.aggregate = nil
		} else {
			bad.aggregate = proc.aggregate
		}
		state := cloneCatalog(view.state)
		state.descriptors[member] = &bad
		if err := stockIntagg.validate(e.catalog.read(state), ext); err == nil {
			t.Fatal("aggregate payload attached to wrong owner", member)
		}
	}
	collision := cloneCatalog(view.state)
	helper := proc.aggregate.helpers[0]
	namespace := *view.state.descriptors[proc.namespace].(*namespaceDescriptor)
	namespace.address = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: helper.id}
	collision.descriptors[namespace.address] = &namespace
	if err := stockIntagg.validate(e.catalog.read(collision), ext); err == nil {
		t.Fatal("cross-class owned helper collision admitted")
	}
	if err := validateDefinitionOIDs(e.catalog.read(collision)); err == nil {
		t.Fatal("global definition collision omitted helper")
	}
}

func TestPGAggregateIntaggRemap(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.state.descriptors[ext.members[0]].(*procedureDescriptor)
	original := *proc.aggregate
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		proc.address:   {ClassID: ids.ProcedureClassID, ObjectID: 900000},
		proc.namespace: {ClassID: ids.NamespaceClassID, ObjectID: 900001},
	}
	for i, helper := range proc.aggregate.helpers {
		mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.id}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900002 + i)}
	}
	remapped := remapExtensionDescriptor(proc, mapping).(*procedureDescriptor)
	if remapped == proc || remapped.aggregate == proc.aggregate || remapped.address.ObjectID != 900000 || remapped.namespace.ObjectID != 900001 || *proc.aggregate != original {
		t.Fatal("copy-on-change metadata remap")
	}
	if remapped.aggregate.definition.function != 900000 || remapped.aggregate.definition.functions[0] != 900002 || remapped.aggregate.definition.functions[1] != 900003 {
		t.Fatal("definition retained old helper IDs")
	}
	for i, h := range remapped.aggregate.helpers {
		if h.id != ids.OID(900002+i) || h.namespace != 900001 {
			t.Fatal("alias retained old namespace/identity")
		}
	}
	if remapExtensionDescriptor(proc, nil) != proc {
		t.Fatal("unchanged remap copied immutable metadata")
	}
	typ, _ := view.LookupType(sqltype.Regproc)
	for _, id := range []ids.OID{original.helpers[0].id, original.helpers[1].id, 4294967295} {
		value, err := sqltype.RegprocValue(typ, uint32(id))
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := remapRoutineDatum(value, func(id ids.OID) ids.OID {
			if next, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}]; ok {
				return next.ObjectID
			}
			return id
		})
		want := id
		if next, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}]; ok {
			want = next.ObjectID
		}
		actual, _ := mapped.ProcedureOID()
		source, _ := value.ProcedureOID()
		if err != nil || actual != uint32(want) || source != uint32(id) {
			t.Fatal("routine datum mapping/source mutation", actual, want, err)
		}
	}
	// The global allocator must skip metadata-only owned IDs just like actual
	// definitions, even when their identities have been remapped out of order.
	e.catalog.nextOID = original.helpers[0].id
	allocated, err := e.catalog.allocateOIDLocked()
	if err != nil || allocated == original.helpers[0].id || allocated == original.helpers[1].id {
		t.Fatal("allocator reused owned helper", allocated, err)
	}
}

func TestPGAggregateIntaggAllocationFailure(t *testing.T) {
	e := New()
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	ext := &extensionDescriptor{address: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, name: "intagg", version: "1.1", targetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, owner: e.authorizationDomain.Principal(10)}
	e.catalog.nextOID = ^ids.OID(0) - 1 // aggregate and state can allocate; final helper cannot.
	if err := stockIntagg.stage(e, stage, ext, ext.owner, AuthorizationView{}); err == nil {
		t.Fatal("helper allocation exhaustion accepted")
	}
	if e.catalog.state != before.state {
		t.Fatal("failed provider published partial installation")
	}
	for _, d := range e.catalog.state.descriptors {
		if p, ok := d.(*procedureDescriptor); ok && p.intagg != nil {
			t.Fatal("failed provider leaked a callable/aggregate owner")
		}
	}
}

func TestPGAggregateIntaggRetainedCollision(t *testing.T) {
	source, ext := intaggUnitInstalled(t)
	proc := source.catalog.state.descriptors[ext.members[0]].(*procedureDescriptor)
	original := *proc.aggregate
	target := New()
	target.catalog.state = cloneCatalog(target.catalog.state)
	namespace := &namespaceDescriptor{
		address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: original.helpers[0].id},
		name:    "pg_temp_pga_collision", revision: 1, owner: target.authorizationDomain.Principal(10),
		temporary: target.catalog.domain.Session(99),
	}
	target.catalog.state.descriptors[namespace.address] = namespace
	target.catalog.state.names[descriptorNameKey(namespace)] = namespace.address
	target.catalog.nextOID = 30000
	next := cloneCatalog(source.catalog.state)
	target.mu.Lock()
	err := target.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*relationStorage{}, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	imported := next.descriptors[proc.address].(*procedureDescriptor)
	if next.descriptors[namespace.address] != namespace || imported == proc || imported.aggregate == proc.aggregate || imported.aggregate.helpers[0].id == original.helpers[0].id || imported.aggregate.definition.functions[0] != imported.aggregate.helpers[0].id {
		t.Fatal("helper collision did not preserve TEMP/remap owner metadata")
	}
	if imported.aggregate.helpers[1].id != original.helpers[1].id || imported.aggregate.helpers[0].namespace != proc.namespace.ObjectID || *proc.aggregate != original {
		t.Fatal("unrelated helper/source changed")
	}
	for _, helper := range imported.aggregate.helpers {
		if next.descriptors[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.id}] != nil {
			t.Fatal("mapping-only helper became a descriptor")
		}
	}
	if err := validateDefinitionOIDs(target.catalog.read(next)); err != nil {
		t.Fatal(err)
	}
}
