package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func accessMethodUnit(t *testing.T, e *core.Engine, name string, native catalog.NativeAccessMethod, handler ids.OID) *catalog.AccessMethod {
	t.Helper()
	return &catalog.AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, ObjectName: name, Native: native, Handler: handler}
}
func accessMethodPublish(t *testing.T, e *core.Engine, definitions ...catalog.Descriptor) catalog.Read {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, d := range definitions {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.CatalogRead()
}
func TestAccessMethodDescriptor(t *testing.T) {
	e := engine.New()
	before := e.CatalogRead()
	m := accessMethodUnit(t, e, "am_descriptor", catalog.BtreeAccessMethod, 330)
	view := accessMethodPublish(t, e, m)
	if _, exists := before.LookupAccessMethod(m.Name()); exists {
		t.Fatal("old catalog changed")
	}
	if got, ok := view.LookupAccessMethod(m.Name()); !ok || got != m || got.Namespace() != (ids.ObjectAddress{}) || got.Kind() != ids.AccessMethodObject {
		t.Fatal("global descriptor identity", got)
	}
	if err := (catalog.AccessMethodProvider{}).Validate(view, m); err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	duplicate := *m
	duplicate.ObjectAddress.ObjectID = catalogUnitOID(t, e)
	if err := stage.Create(&duplicate); err == nil {
		t.Fatal("global name collision admitted")
	}
	if stage.DescriptorCount() != 0 {
		t.Fatal("collision staged a prefix")
	}
	for _, change := range []func(*catalog.AccessMethod){
		func(d *catalog.AccessMethod) { d.Handler = 333 },
		func(d *catalog.AccessMethod) { d.Native = catalog.HeapAccessMethod },
		func(d *catalog.AccessMethod) { d.Native = catalog.NoAccessMethod },
		func(d *catalog.AccessMethod) { d.ObjectRevision = 0 },
	} {
		bad := *m
		change(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := (catalog.AccessMethodProvider{}).Validate(view.WithState(state), &bad); err == nil {
			t.Fatal("invalid descriptor accepted", bad)
		}
	}
	candidate := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(candidate)
	canceled := accessMethodUnit(t, e, "am_canceled", catalog.BtreeAccessMethod, 330)
	if err := candidate.Create(canceled); err != nil {
		t.Fatal(err)
	}
	if !candidate.AccessMethodsCurrent(view) {
		t.Fatal("unchanged handler rejected")
	}
	handler, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 330})
	changed := *handler
	changed.ObjectRevision++
	changedState := catalog.CloneState(view.State())
	changedState.SetDescriptor(changed.Address(), &changed)
	if candidate.AccessMethodsCurrent(view.WithState(changedState)) {
		t.Fatal("handler changed after private admission without conflict")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, candidate); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled stage", err)
	}
	if e.CatalogRead().State() != view.State() {
		t.Fatal("canceled candidate published")
	}
	if _, exists := e.CatalogRead().LookupAccessMethod(canceled.Name()); exists {
		t.Fatal("canceled name published")
	}
	for _, oid := range []ids.OID{2, 403, 405, 783, 3580, 4000} {
		builtin, ok := view.AccessMethod(oid)
		if !ok || !catalog.AccessMethodSeeded(builtin) || !stored.DependPinned(view, nil, builtin.Address()) {
			t.Fatal("missing pinned bootstrap authority", oid)
		}
	}
	if catalog.AccessMethodSeeded(m) || stored.DependPinned(view, nil, m.Address()) {
		t.Fatal("user method pinned")
	}
}
