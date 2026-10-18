package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGAggregateIntaggOwnership(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.MustProcedure(ext.Members[0])
	enum := view.MustProcedure(ext.Members[1])
	if len(ext.Members) != 2 || proc.Aggregate == nil || enum.Aggregate != nil {
		t.Fatal("callable/aggregate owner inventory")
	}
	if err := stockIntagg.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	if err := validateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	for i, helper := range proc.Aggregate.Helpers {
		if helper.ID == proc.Address().ObjectID || helper.ID == enum.Address().ObjectID || helper.ID == 2333 || helper.ID == 2334 || helper.ID == 0 {
			t.Fatal("helper identity substituted", helper)
		}
		identity, ok := routineIdentityByOID(view, helper.ID)
		if !ok || identity != helper || routineDescriptorOwner(view, helper.ID) != proc {
			t.Fatal("owned alias lookup", i, identity)
		}
		selected, n := selectExactRoutine(view, namespaceSelection{qualified: proc.Namespace()}, helper.Name, helper.Signature)
		if n != 1 || selected != helper {
			t.Fatal("owned exact signature", selected, n)
		}
		selected, n = selectRoutine(view, namespaceSelection{qualified: proc.Namespace()}, helper.Name, nil)
		if n != 1 || selected != helper {
			t.Fatal("owned bare alias", selected, n)
		}
		if procedureInNamespaces(view, namespaceSelection{qualified: proc.Namespace()}, helper.Name, helper.Signature) != nil {
			t.Fatal("metadata helper became callable")
		}
		reserved := make(map[ids.OID]bool)
		reserveDefinitionOIDs(reserved, proc)
		if !reserved[helper.ID] || !definitionOIDUsed(proc, helper.ID) {
			t.Fatal("allocator lost owned helper", helper.ID)
		}
		typ, _ := view.LookupType(sqltype.Regprocedure)
		value, err := sqltype.RegprocValue(typ, uint32(helper.ID))
		if err != nil {
			t.Fatal(err)
		}
		var refs []ids.ObjectAddress
		if err := walkRoutineReferences(view, value, func(a ids.ObjectAddress) error { refs = append(refs, a); return nil }); err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{proc.Address()}) {
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
		if operand.Ref.Address == proc.Address() && operand.Revision == proc.Revision() {
			found = true
		}
	}
	if !found {
		t.Fatal("prepared helper literal omitted real owner dependency")
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600})
	ref := e.catalog.Domain.Ref(view.Epoch(), source.Address())
	rows, err := capturePGAggregate(context.Background(), view, ref, source)
	if err != nil || len(rows.anonymous) != 145 {
		t.Fatal("installed aggregate source", err)
	}
	removed := catalog.CloneState(view.State())
	removed.RemoveDescriptor(proc.Address())
	removed.UnbindName(catalog.DescriptorNameKey(proc))
	retired := e.catalog.read(removed)
	for _, helper := range proc.Aggregate.Helpers {
		if _, ok := routineIdentityByOID(retired, helper.ID); ok {
			t.Fatal("retired helper still resolves")
		}
	}
	// Old capture ownership remains intact independently of current owner removal.
	if id, err := rows.anonymous[len(rows.anonymous)-1][0].ProcedureOID(); err != nil || id != uint32(proc.Address().ObjectID) {
		t.Fatal("retained aggregate row lost owner", id, err)
	}
}

func TestPGAggregateIntaggValidation(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.MustProcedure(ext.Members[0])
	for _, mutate := range []func(*catalog.OwnedAggregate){
		func(a *catalog.OwnedAggregate) { a.Helpers[0].ID = 0 },
		func(a *catalog.OwnedAggregate) {
			a.Helpers[0].ID = a.Helpers[1].ID
			a.Definition.Functions[0] = a.Helpers[0].ID
		},
		func(a *catalog.OwnedAggregate) { a.Helpers[0].ID = 2333; a.Definition.Functions[0] = 2333 },
		func(a *catalog.OwnedAggregate) {
			a.Helpers[0].ID = ext.Members[1].ObjectID
			a.Definition.Functions[0] = a.Helpers[0].ID
		},
		func(a *catalog.OwnedAggregate) { a.Helpers[0].Namespace = 0 },
		func(a *catalog.OwnedAggregate) { a.Helpers[0].Name = "array_agg_transfn" },
		func(a *catalog.OwnedAggregate) { a.Helpers[1].Signature = "23" },
		func(a *catalog.OwnedAggregate) { a.Definition.Function++ },
		func(a *catalog.OwnedAggregate) { a.Definition.Functions[2] = 463 },
		func(a *catalog.OwnedAggregate) { a.Definition.HasInitial = true },
	} {
		bad := *proc
		owned := *proc.Aggregate
		mutate(&owned)
		bad.Aggregate = &owned
		state := catalog.CloneState(view.State())
		state.SetDescriptor(proc.Address(), &bad)
		if err := stockIntagg.ValidateExtension(e.catalog.read(state), ext); err == nil {
			t.Fatal("invalid owned aggregate admitted", owned)
		}
	}
	for _, member := range ext.Members {
		bad := *view.MustProcedure(member)
		if member == proc.Address() {
			bad.Aggregate = nil
		} else {
			bad.Aggregate = proc.Aggregate
		}
		state := catalog.CloneState(view.State())
		state.SetDescriptor(member, &bad)
		if err := stockIntagg.ValidateExtension(e.catalog.read(state), ext); err == nil {
			t.Fatal("aggregate payload attached to wrong owner", member)
		}
	}
	collision := catalog.CloneState(view.State())
	helper := proc.Aggregate.Helpers[0]
	namespace := *view.MustNamespace(proc.Namespace())
	namespace.ObjectAddress = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: helper.ID}
	collision.SetDescriptor(namespace.Address(), &namespace)
	if err := stockIntagg.ValidateExtension(e.catalog.read(collision), ext); err == nil {
		t.Fatal("cross-class owned helper collision admitted")
	}
	if err := validateDefinitionOIDs(e.catalog.read(collision)); err == nil {
		t.Fatal("global definition collision omitted helper")
	}
}

func TestPGAggregateIntaggRemap(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.MustProcedure(ext.Members[0])
	original := *proc.Aggregate
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		proc.Address():   {ClassID: ids.ProcedureClassID, ObjectID: 900000},
		proc.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 900001},
	}
	for i, helper := range proc.Aggregate.Helpers {
		mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900002 + i)}
	}
	remapped := remapExtensionDescriptor(proc, mapping).(*catalog.Procedure)
	if remapped == proc || remapped.Aggregate == proc.Aggregate || remapped.Address().ObjectID != 900000 || remapped.Namespace().ObjectID != 900001 || *proc.Aggregate != original {
		t.Fatal("copy-on-change metadata remap")
	}
	if remapped.Aggregate.Definition.Function != 900000 || remapped.Aggregate.Definition.Functions[0] != 900002 || remapped.Aggregate.Definition.Functions[1] != 900003 {
		t.Fatal("definition retained old helper IDs")
	}
	for i, h := range remapped.Aggregate.Helpers {
		if h.ID != ids.OID(900002+i) || h.Namespace != 900001 {
			t.Fatal("alias retained old namespace/identity")
		}
	}
	if remapExtensionDescriptor(proc, nil) != proc {
		t.Fatal("unchanged remap copied immutable metadata")
	}
	typ, _ := view.LookupType(sqltype.Regproc)
	for _, id := range []ids.OID{original.Helpers[0].ID, original.Helpers[1].ID, 4294967295} {
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
	e.catalog.nextOID = original.Helpers[0].ID
	allocated, err := e.catalog.allocateOIDLocked()
	if err != nil || allocated == original.Helpers[0].ID || allocated == original.Helpers[1].ID {
		t.Fatal("allocator reused owned helper", allocated, err)
	}
}

func TestPGAggregateIntaggAllocationFailure(t *testing.T) {
	e := New()
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, ObjectName: "intagg", Version: "1.1", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.authorizationDomain.Principal(10)}
	e.catalog.nextOID = ^ids.OID(0) - 1 // aggregate and state can allocate; final helper cannot.
	if err := stockIntagg.stage(e, stage, ext, ext.Owner, AuthorizationView{}); err == nil {
		t.Fatal("helper allocation exhaustion accepted")
	}
	if e.catalog.state != before.State() {
		t.Fatal("failed provider published partial installation")
	}
	for _, d := range e.catalog.state.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && intaggProviderOf(p) != nil {
			t.Fatal("failed provider leaked a callable/aggregate owner")
		}
	}
}

func TestPGAggregateIntaggRetainedCollision(t *testing.T) {
	source, ext := intaggUnitInstalled(t)
	proc := source.catalog.state.MustProcedure(ext.Members[0])
	original := *proc.Aggregate
	target := New()
	target.catalog.state = catalog.CloneState(target.catalog.state)
	namespace := &catalog.Namespace{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: original.Helpers[0].ID},
		ObjectName:    "pg_temp_pga_collision", ObjectRevision: 1, Owner: target.authorizationDomain.Principal(10),
		Temporary: target.catalog.Domain.Session(99),
	}
	target.catalog.state.SetDescriptor(namespace.Address(), namespace)
	target.catalog.state.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	target.catalog.nextOID = 30000
	next := catalog.CloneState(source.catalog.state)
	target.mu.Lock()
	err := target.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*relationStorage{}, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	imported := next.MustProcedure(proc.Address())
	if next.Get(namespace.Address()) != namespace || imported == proc || imported.Aggregate == proc.Aggregate || imported.Aggregate.Helpers[0].ID == original.Helpers[0].ID || imported.Aggregate.Definition.Functions[0] != imported.Aggregate.Helpers[0].ID {
		t.Fatal("helper collision did not preserve TEMP/remap owner metadata")
	}
	if imported.Aggregate.Helpers[1].ID != original.Helpers[1].ID || imported.Aggregate.Helpers[0].Namespace != proc.Namespace().ObjectID || *proc.Aggregate != original {
		t.Fatal("unrelated helper/source changed")
	}
	for _, helper := range imported.Aggregate.Helpers {
		if next.Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}) != nil {
			t.Fatal("mapping-only helper became a descriptor")
		}
	}
	if err := validateDefinitionOIDs(target.catalog.read(next)); err != nil {
		t.Fatal(err)
	}
}
