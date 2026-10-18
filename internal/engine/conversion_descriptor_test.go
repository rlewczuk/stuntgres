package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func conversionUnitDefinition(e *Engine, oid OID, name string, def bool) *conversionDescriptor {
	return &conversionDescriptor{oid: oid, name: name, namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, revision: 1, owner: e.authorizationDomain.Principal(10), source: 6, destination: 8, handler: ObjectAddress{ClassID: ProcedureClassID, ObjectID: 4375}, defaultConversion: def}
}

func conversionUnitName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{Parts: make([]parser.Identifier, len(parts))}
	for i, text := range parts {
		name.Parts[i] = parser.Identifier{Text: text}
	}
	return name
}

func conversionUnitAuth(t *testing.T, e *Engine) (*Operation, AuthorizationView) {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return op, auth
}

func TestConversionDescriptorAuthority(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	d := conversionUnitDefinition(e, 90000, "authority", false)
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	view := e.CatalogRead()
	if err := (conversionProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []func(*conversionDescriptor){
		func(d *conversionDescriptor) { d.source = 0 }, func(d *conversionDescriptor) { d.destination = 9 },
		func(d *conversionDescriptor) { d.handler.ObjectID = 4374 }, func(d *conversionDescriptor) { d.handler.SubID = 1 },
		func(d *conversionDescriptor) { d.namespace.ObjectID = 999999 }, func(d *conversionDescriptor) { d.revision = 0 },
	} {
		bad := *d
		corrupt(&bad)
		if err := (conversionProvider{}).Validate(view, &bad); err == nil {
			t.Fatal("invalid conversion capability admitted", bad)
		}
	}
	bad := *d
	bad.owner = New().authorizationDomain.Principal(10)
	bad.revision++
	stage = e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&bad); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("foreign principal admitted")
	}
	seed := *view.state.descriptors[ObjectAddress{ClassID: ConversionClassID, ObjectID: 4519}].(*conversionDescriptor)
	seed.defaultConversion = false
	if err := (conversionProvider{}).Validate(view, &seed); err == nil {
		t.Fatal("pinned capability/default invariant changed")
	}
	seed = *view.state.descriptors[seed.Address()].(*conversionDescriptor)
	seed.name, seed.namespace, seed.revision = "legal_seed", d.Namespace(), 2
	stage = e.BeginCatalogStage(view)
	if err := stage.Replace(&seed); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if !seed.seeded() || !pgDependPinned(e.CatalogRead(), seed.Address()) || seed.handler != d.handler {
		t.Fatal("legal metadata mutation lost pin/provider")
	}
	if original := view.state.descriptors[seed.Address()].(*conversionDescriptor); original.name != "utf8_to_iso_8859_1" || original.Namespace().ObjectID != PGCatalogNamespaceOID {
		t.Fatal("seed mutation changed old snapshot")
	}
}

func TestConversionDescriptorCreateAndMoveUniqueness(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	create := &parser.CreateConversion{Name: conversionUnitName("public", "first"), Source: "UTF8", Destination: "LATIN1", Function: conversionUnitName("pg_catalog", "utf8_to_iso8859_1"), Default: true}
	stage := e.BeginCatalogStage(auth.catalog)
	e.mu.Lock()
	err := e.stageCreateConversionLocked(op, auth, stage, create)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	_, auth = conversionUnitAuth(t, e)
	before := e.CatalogRead()
	create.Name = conversionUnitName("public", "second")
	stage = e.BeginCatalogStage(before)
	e.mu.Lock()
	err = e.stageCreateConversionLocked(op, auth, stage, create)
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != "default conversion for UTF8 to LATIN1 already exists" {
		t.Fatal("CREATE default conflict", err)
	}
	e.DiscardCatalogStage(stage)
	// Provider validation deliberately permits a legal post-move duplicate.
	second := conversionUnitDefinition(e, 90000, "second", true)
	stage = e.BeginCatalogStage(before)
	if err = stage.Create(second); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if e.CatalogRead().state.descriptors[second.Address()] != second {
		t.Fatal("legal moved-default shape rejected")
	}
	if e.CatalogRead().state == before.state {
		t.Fatal("duplicate shape not published")
	}
	if err = conversionProbeResult("real_routine", 1); !errors.As(err, &diagnostic) || diagnostic.Code != "42P17" || diagnostic.Message != "encoding conversion function real_routine returned incorrect result for empty input" {
		t.Fatal("consumed empty-input contract", err)
	}
}

func TestConversionDescriptorCancellationAndBatchAtomicity(t *testing.T) {
	e := New()
	d := conversionUnitDefinition(e, 90000, "keep", false)
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	op, auth := conversionUnitAuth(t, e)
	before := e.CatalogRead()
	stage = e.BeginCatalogStage(before)
	e.mu.Lock()
	err := e.stageDropConversionLocked(op, auth, stage, &parser.DropConversion{Names: []parser.ObjectName{conversionUnitName("public", "keep"), conversionUnitName("public", "missing")}, Mode: parser.DropRestrict})
	e.mu.Unlock()
	if err == nil {
		t.Fatal("late missing batch target succeeded")
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().state != before.state || before.state.descriptors[d.Address()] != d {
		t.Fatal("batch published prefix")
	}
	stage = e.BeginCatalogStage(before)
	added := conversionUnitDefinition(e, 90001, "cancel", false)
	if err = stage.Create(added); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().state != before.state {
		t.Fatal("final cancellation published conversion", err)
	}
}

func TestConversionDescriptorNoopAndPinBoundaries(t *testing.T) {
	e := New()
	d := conversionUnitDefinition(e, 90000, "unchanged", false)
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	op, auth := conversionUnitAuth(t, e)
	before := e.CatalogRead()
	auth.state.CurrentUser = auth.roles.LookupRef("pg_monitor")
	for _, alteration := range []*parser.AlterConversion{
		{Name: conversionUnitName("public", "unchanged"), Action: "schema", Target: parser.Identifier{Text: "public"}},
		{Name: conversionUnitName("public", "unchanged"), Action: "owner", Target: parser.Identifier{Text: "postgres"}},
	} {
		stage = e.BeginCatalogStage(before)
		e.mu.Lock()
		err := e.stageAlterConversionLocked(op, auth, stage, alteration)
		e.mu.Unlock()
		if err != nil || len(stage.descriptors) != 0 {
			t.Fatal("generic no-op permission boundary", alteration, err)
		}
		e.DiscardCatalogStage(stage)
	}
	auth.state.CurrentUser = e.authorizationDomain.Principal(10)
	stage = e.BeginCatalogStage(before)
	e.mu.Lock()
	err := e.stageAlterConversionLocked(op, auth, stage, &parser.AlterConversion{Name: conversionUnitName("public", "unchanged"), Action: "rename", Target: parser.Identifier{Text: "unchanged"}})
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != `conversion "unchanged" already exists in schema "public"` {
		t.Fatal("rename-to-self unique-name contract", err)
	}
	e.DiscardCatalogStage(stage)
	procedure := ObjectAddress{ClassID: ProcedureClassID, ObjectID: 4375}
	if actual := dependencyObject(before, procedure, nil); actual != "function utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)" {
		t.Fatal("actual conversion procedure signature presentation", actual)
	}
	if actual := dependencyObject(before, procedure, []ObjectAddress{{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}}); actual != "function pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)" {
		t.Fatal("actual conversion procedure qualified presentation", actual)
	}
	for _, address := range []ObjectAddress{{ClassID: ConversionClassID, ObjectID: 4519}, {ClassID: ProcedureClassID, ObjectID: 4375}} {
		stage = e.BeginCatalogStage(before)
		if err = stage.Remove(e.domain.Ref(before.Epoch(), address)); !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" {
			t.Fatal("real provider pin removed", address, err)
		}
		e.DiscardCatalogStage(stage)
	}
	if e.CatalogRead().state != before.state {
		t.Fatal("no-op/error/pin checks published state")
	}
	if result, err := e.Execute(op, &parser.DropConversion{Names: []parser.ObjectName{conversionUnitName("public", "unchanged")}}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
		t.Fatal("actual conversion root retirement", result, err)
	}
	if _, exists := e.CatalogRead().state.descriptors[d.Address()]; exists {
		t.Fatal("DROP retired dependents but retained conversion root")
	}
	if retained := before.state.descriptors[d.Address()]; retained != d {
		t.Fatal("DROP mutated prior conversion snapshot")
	}
}
