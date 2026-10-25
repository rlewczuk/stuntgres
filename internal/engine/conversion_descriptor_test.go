package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func conversionUnitDefinition(e *Engine, oid ids.OID, name string, def bool) *catalog.Conversion {
	return &catalog.Conversion{OID: oid, ObjectName: name, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, Owner: e.RoleStore.Domain.Principal(10), Source: 6, Destination: 8, Handler: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4375}, DefaultConversion: def}
}

func conversionUnitName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{Parts: make([]parser.Identifier, len(parts))}
	for i, text := range parts {
		name.Parts[i] = parser.Identifier{Text: text}
	}
	return name
}

func conversionUnitAuth(t *testing.T, e *Engine) (*session.Operation, AuthorizationView) {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
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
	if err := (catalog.ConversionProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []func(*catalog.Conversion){
		func(d *catalog.Conversion) { d.Source = 0 }, func(d *catalog.Conversion) { d.Destination = 9 },
		func(d *catalog.Conversion) { d.Handler.ObjectID = 4374 }, func(d *catalog.Conversion) { d.Handler.SubID = 1 },
		func(d *catalog.Conversion) { d.ObjectNamespace.ObjectID = 999999 }, func(d *catalog.Conversion) { d.ObjectRevision = 0 },
	} {
		bad := *d
		corrupt(&bad)
		if err := (catalog.ConversionProvider{}).Validate(view, &bad); err == nil {
			t.Fatal("invalid conversion capability admitted", bad)
		}
	}
	bad := *d
	bad.Owner = New().RoleStore.Domain.Principal(10)
	bad.ObjectRevision++
	stage = e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&bad); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("foreign principal admitted")
	}
	seed := *view.MustConversion(ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: 4519})
	seed.DefaultConversion = false
	if err := (catalog.ConversionProvider{}).Validate(view, &seed); err == nil {
		t.Fatal("pinned capability/default invariant changed")
	}
	seed = *view.MustConversion(seed.Address())
	seed.ObjectName, seed.ObjectNamespace, seed.ObjectRevision = "legal_seed", d.Namespace(), 2
	stage = e.BeginCatalogStage(view)
	if err := stage.Replace(&seed); err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	if !seed.Seeded() || !stored.DependPinned(e.CatalogRead(), nil, seed.Address()) || seed.Handler != d.Handler {
		t.Fatal("legal metadata mutation lost pin/provider")
	}
	if original := view.MustConversion(seed.Address()); original.Name() != "utf8_to_iso_8859_1" || original.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
		t.Fatal("seed mutation changed old snapshot")
	}
}

func TestConversionDescriptorCreateAndMoveUniqueness(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	create := &parser.CreateConversion{Name: conversionUnitName("public", "first"), Source: "UTF8", Destination: "LATIN1", Function: conversionUnitName("pg_catalog", "utf8_to_iso8859_1"), Default: true}
	stage := e.BeginCatalogStage(auth.Catalog)
	e.Mu.Lock()
	err := typedef.StageCreateConversionLocked(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, create)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, stage)
	_, auth = conversionUnitAuth(t, e)
	before := e.CatalogRead()
	create.Name = conversionUnitName("public", "second")
	stage = e.BeginCatalogStage(before)
	e.Mu.Lock()
	err = typedef.StageCreateConversionLocked(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, create)
	e.Mu.Unlock()
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
	if e.CatalogRead().Get(second.Address()) != second {
		t.Fatal("legal moved-default shape rejected")
	}
	if e.CatalogRead().State() == before.State() {
		t.Fatal("duplicate shape not published")
	}
	if err = typedef.ConversionProbeResult("real_routine", 1); !errors.As(err, &diagnostic) || diagnostic.Code != "42P17" || diagnostic.Message != "encoding conversion function real_routine returned incorrect result for empty input" {
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
	e.Mu.Lock()
	err := typedef.StageDropConversionLocked(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, &parser.DropConversion{Names: []parser.ObjectName{conversionUnitName("public", "keep"), conversionUnitName("public", "missing")}, Mode: parser.DropRestrict})
	e.Mu.Unlock()
	if err == nil {
		t.Fatal("late missing batch target succeeded")
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().State() != before.State() || before.Get(d.Address()) != d {
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
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
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
	auth.State.CurrentUser = auth.Roles.LookupRef("pg_monitor")
	for _, alteration := range []*parser.AlterConversion{
		{Name: conversionUnitName("public", "unchanged"), Action: "schema", Target: parser.Identifier{Text: "public"}},
		{Name: conversionUnitName("public", "unchanged"), Action: "owner", Target: parser.Identifier{Text: "postgres"}},
	} {
		stage = e.BeginCatalogStage(before)
		e.Mu.Lock()
		err := typedef.StageAlterConversionLocked(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, alteration)
		e.Mu.Unlock()
		if err != nil || stage.DescriptorCount() != 0 {
			t.Fatal("generic no-op permission boundary", alteration, err)
		}
		e.DiscardCatalogStage(stage)
	}
	auth.State.CurrentUser = e.RoleStore.Domain.Principal(10)
	stage = e.BeginCatalogStage(before)
	e.Mu.Lock()
	err := typedef.StageAlterConversionLocked(DDLHost{E: e, Op: op, CatalogStage: stage}, auth, &parser.AlterConversion{Name: conversionUnitName("public", "unchanged"), Action: "rename", Target: parser.Identifier{Text: "unchanged"}})
	e.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Message != `conversion "unchanged" already exists in schema "public"` {
		t.Fatal("rename-to-self unique-name contract", err)
	}
	e.DiscardCatalogStage(stage)
	procedure := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4375}
	if actual := stored.DependencyObject(before, procedure, nil); actual != "function utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)" {
		t.Fatal("actual conversion procedure signature presentation", actual)
	}
	if actual := stored.DependencyObject(before, procedure, []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}); actual != "function pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)" {
		t.Fatal("actual conversion procedure qualified presentation", actual)
	}
	for _, address := range []ids.ObjectAddress{{ClassID: ids.ConversionClassID, ObjectID: 4519}, {ClassID: ids.ProcedureClassID, ObjectID: 4375}} {
		stage = e.BeginCatalogStage(before)
		if err = stage.Remove(e.Catalog.Domain.Ref(before.Epoch(), address)); !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" {
			t.Fatal("real provider pin removed", address, err)
		}
		e.DiscardCatalogStage(stage)
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("no-op/error/pin checks published state")
	}
	if result, err := e.Execute(op, &parser.DropConversion{Names: []parser.ObjectName{conversionUnitName("public", "unchanged")}}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
		t.Fatal("actual conversion root retirement", result, err)
	}
	if _, exists := e.CatalogRead().Find(d.Address()); exists {
		t.Fatal("DROP retired dependents but retained conversion root")
	}
	if retained := before.Get(d.Address()); retained != d {
		t.Fatal("DROP mutated prior conversion snapshot")
	}
}
