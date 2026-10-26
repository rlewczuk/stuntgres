package schemacmds_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGAggregateIntaggOwnership(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	proc := view.MustProcedure(ext.Members[0])
	enum := view.MustProcedure(ext.Members[1])
	if len(ext.Members) != 2 || proc.Aggregate == nil || enum.Aggregate != nil {
		t.Fatal("callable/aggregate owner inventory")
	}
	if err := extension.StockIntagg.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	for i, helper := range proc.Aggregate.Helpers {
		if helper.ID == proc.Address().ObjectID || helper.ID == enum.Address().ObjectID || helper.ID == 2333 || helper.ID == 2334 || helper.ID == 0 {
			t.Fatal("helper identity substituted", helper)
		}
		identity, ok := regobj.RoutineIdentityByOID(view, helper.ID)
		if !ok || identity != helper || regobj.RoutineDescriptorOwner(view, helper.ID) != proc {
			t.Fatal("owned alias lookup", i, identity)
		}
		selected, n := regobj.SelectExactRoutine(view, analyze.NamespaceSelection{Qualified: proc.Namespace()}, helper.Name, helper.Signature)
		if n != 1 || selected != helper {
			t.Fatal("owned exact signature", selected, n)
		}
		selected, n = regobj.SelectRoutine(view, analyze.NamespaceSelection{Qualified: proc.Namespace()}, helper.Name, nil)
		if n != 1 || selected != helper {
			t.Fatal("owned bare alias", selected, n)
		}
		if analyze.ProcedureInNamespaces(view, analyze.NamespaceSelection{Qualified: proc.Namespace()}, helper.Name, helper.Signature) != nil {
			t.Fatal("metadata helper became callable")
		}
		reserved := make(map[ids.OID]bool)
		catalog.ReserveDefinitionOIDs(reserved, proc)
		if !reserved[helper.ID] || !catalog.DefinitionOIDUsed(proc, helper.ID) {
			t.Fatal("allocator lost owned helper", helper.ID)
		}
		typ, _ := view.LookupType(sqltype.Regprocedure)
		value, err := sqltype.RegprocValue(typ, uint32(helper.ID))
		if err != nil {
			t.Fatal(err)
		}
		var refs []ids.ObjectAddress
		if err := regobj.WalkRoutineReferences(view, value, func(a ids.ObjectAddress) error { refs = append(refs, a); return nil }); err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{proc.Address()}) {
			t.Fatal("helper dependency is not real aggregate owner", refs, err)
		}
	}
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	typ, _ := view.LookupType(sqltype.Regprocedure)
	if _, err := b.RoutineConstant("public.int_agg_state(internal,integer)", typ.Usage(), parser.Position{Offset: 8, Character: 8}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operand := range b.Prepared.RegtypeConstants[0].Operands {
		if operand.Ref.Address == proc.Address() && operand.Revision == proc.Revision() {
			found = true
		}
	}
	if !found {
		t.Fatal("prepared helper literal omitted real owner dependency")
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2600})
	ref := e.Catalog.Domain.Ref(view.Epoch(), source.Address())
	rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: core.EngineCatalogRuntime{}}, capturePGAggregate)
	if err != nil || len(rows.Anonymous) != 145 {
		t.Fatal("installed aggregate source", err)
	}
	removed := catalog.CloneState(view.State())
	removed.RemoveDescriptor(proc.Address())
	removed.UnbindName(catalog.DescriptorNameKey(proc))
	retired := e.Catalog.Read(removed)
	for _, helper := range proc.Aggregate.Helpers {
		if _, ok := regobj.RoutineIdentityByOID(retired, helper.ID); ok {
			t.Fatal("retired helper still resolves")
		}
	}
	// Old capture ownership remains intact independently of current owner removal.
	if id, err := rows.Anonymous[len(rows.Anonymous)-1][0].ProcedureOID(); err != nil || id != uint32(proc.Address().ObjectID) {
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
		if err := extension.StockIntagg.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
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
		if err := extension.StockIntagg.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
			t.Fatal("aggregate payload attached to wrong owner", member)
		}
	}
	collision := catalog.CloneState(view.State())
	helper := proc.Aggregate.Helpers[0]
	namespace := *view.MustNamespace(proc.Namespace())
	namespace.ObjectAddress = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: helper.ID}
	collision.SetDescriptor(namespace.Address(), &namespace)
	if err := extension.StockIntagg.ValidateExtension(e.Catalog.Read(collision), ext); err == nil {
		t.Fatal("cross-class owned helper collision admitted")
	}
	if err := catalog.ValidateDefinitionOIDs(e.Catalog.Read(collision)); err == nil {
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
	remapped := extension.RemapDescriptor(proc, mapping).(*catalog.Procedure)
	if remapped == proc || remapped.Aggregate == proc.Aggregate || remapped.Address().ObjectID != 900000 || remapped.Namespace().ObjectID != 900001 || !catalog.SameAggregateDefinition(proc.Aggregate, &original) {
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
	if extension.RemapDescriptor(proc, nil) != proc {
		t.Fatal("unchanged remap copied immutable metadata")
	}
	typ, _ := view.LookupType(sqltype.Regproc)
	for _, id := range []ids.OID{original.Helpers[0].ID, original.Helpers[1].ID, 4294967295} {
		value, err := sqltype.RegprocValue(typ, uint32(id))
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := regobj.RemapRoutineDatum(value, func(id ids.OID) ids.OID {
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
	e.Catalog.InstallOIDFrontierLocked(original.Helpers[0].ID)
	allocated, err := e.Catalog.AllocateOIDLocked()
	if err != nil || allocated == original.Helpers[0].ID || allocated == original.Helpers[1].ID {
		t.Fatal("allocator reused owned helper", allocated, err)
	}
}

func TestPGAggregateIntaggAllocationFailure(t *testing.T) {
	e := engine.New()
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 200000}, ObjectName: "intagg", Version: "1.1", TargetNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.RoleStore.Domain.Principal(10)}
	e.Catalog.InstallOIDFrontierLocked(^ids.OID(0) - 1) // aggregate and state can allocate; final helper cannot.
	if err := extension.StockIntagg.Stage(&schemacmds.ExtensionHost{Engine: e, Stage: stage}, ext, ext.Owner); err == nil {
		t.Fatal("helper allocation exhaustion accepted")
	}
	if e.Catalog.State != before.State() {
		t.Fatal("failed provider published partial installation")
	}
	for _, d := range e.Catalog.State.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && extension.IntaggProviderOf(p) != nil {
			t.Fatal("failed provider leaked a callable/aggregate owner")
		}
	}
}

func TestPGAggregateIntaggRelabelIdentity(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	view := e.CatalogRead()
	original := view.MustProcedure(ext.Members[0])
	if symbol, err := extension.ProcedureSymbol(view, original); err != nil || symbol != "int_array_aggregate" {
		t.Fatal("invalid relabel baseline", symbol, err)
	}
	renamed := *original
	renamed.ObjectName = "relabeled_intagg"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	state := catalog.CloneState(view.State())
	state.UnbindName(catalog.DescriptorNameKey(original))
	state.SetDescriptor(renamed.Address(), &renamed)
	state.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
	current := e.Catalog.Read(state)
	if symbol, err := extension.ProcedureSymbol(current, &renamed); err != nil || symbol != "int_array_aggregate" {
		t.Fatal(symbol, err)
	}
	if err := extension.StockIntagg.ValidateExtension(current, ext); err != nil {
		t.Fatal("moved aggregate rejected", err)
	}
	if renamed.Aggregate != original.Aggregate || renamed.Aggregate.Helpers[0].Namespace != ext.TargetNamespace.ObjectID {
		t.Fatal("move rewrote hidden helper authority")
	}
	mapped := extension.RemapDescriptor(&renamed, map[ids.ObjectAddress]ids.ObjectAddress{
		renamed.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 910001},
	}).(*catalog.Procedure)
	if mapped.Namespace().ObjectID != 910001 || mapped.Aggregate != original.Aggregate {
		t.Fatal("routine-only namespace remap changed hidden helpers")
	}
	copy := *ext
	copy.Members = append([]ids.ObjectAddress(nil), ext.Members...)
	if err := extension.StockIntagg.ValidateExtension(current, &copy); err != nil {
		t.Fatal("equivalent COW manifest rejected", err)
	}
	copy.Members[0], copy.Members[1] = copy.Members[1], copy.Members[0]
	if err := extension.StockIntagg.ValidateExtension(current, &copy); err == nil {
		t.Fatal("reordered manifest accepted")
	}
	if view.Get(original.Address()) != original || ext.TargetNamespace != original.Namespace() {
		t.Fatal("source installation mutated")
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
