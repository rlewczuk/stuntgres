package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func accessMethodUnit(t *testing.T, e *Engine, name string, native catalog.NativeAccessMethod, handler ids.OID) *catalog.AccessMethod {
	t.Helper()
	return &catalog.AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: catalogUnitOID(t, e)}, ObjectRevision: 1, ObjectName: name, Native: native, Handler: handler}
}
func accessMethodPublish(t *testing.T, e *Engine, definitions ...catalog.Descriptor) catalog.Read {
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
	if _, err = publishCatalog(e, prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.CatalogRead()
}
func TestAccessMethodDescriptor(t *testing.T) {
	e := New()
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
	for _, oid := range []ids.OID{2, 403, 405, 783} {
		builtin, ok := view.AccessMethod(oid)
		if !ok || !catalog.AccessMethodSeeded(builtin) || !stored.DependPinned(view, nil, builtin.Address()) {
			t.Fatal("missing pinned bootstrap authority", oid)
		}
	}
	if catalog.AccessMethodSeeded(m) || stored.DependPinned(view, nil, m.Address()) {
		t.Fatal("user method pinned")
	}
}
func TestAccessMethodHandlerRegistration(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	// Even an empty genuine defaults capability must not coexist with a native
	// handler. Construct it through its real owner, rather than a test double.
	defaults, err := stored.NewRoutineDefaults(context.Background(), view, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		oid, result ids.OID
		native      catalog.NativeAccessMethod
		name        string
	}{
		{3, 269, catalog.HeapAccessMethod, "heap_tableam_handler"},
		{330, 325, catalog.BtreeAccessMethod, "bthandler"},
		{331, 325, catalog.HashAccessMethod, "hashhandler"},
		{332, 325, catalog.GistAccessMethod, "gisthandler"},
	} {
		p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid})
		if !ok || p.AccessMethod != tc.native || p.Result != tc.result || p.Name() != tc.name || p.Signature != "2281" || p.Volatility != expr.Volatile || !p.Strict {
			t.Fatal("real handler registration", tc, p)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
			t.Fatal(err)
		}
		if tc.native == catalog.HeapAccessMethod {
			if _, indexKind := p.AccessMethod.IndexImplementation(); indexKind {
				t.Fatal("heap routed to Btree placeholder")
			}
		}
		renamed := *p
		renamed.ObjectName = "relabeled_" + tc.name
		state := catalog.CloneState(view.State())
		state.UnbindName(catalog.DescriptorNameKey(p))
		state.SetDescriptor(renamed.Address(), &renamed)
		state.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
		if err := (stored.ProcedureProvider{}).Validate(view.WithState(state), &renamed); err != nil {
			t.Fatal("genuine handler relabel rejected", err)
		}
		for _, change := range []func(*catalog.Procedure){
			func(d *catalog.Procedure) { d.ObjectAddress.ObjectID = 90000 },
			func(d *catalog.Procedure) { d.Signature = "23" },
			func(d *catalog.Procedure) { d.Result = 23 },
			func(d *catalog.Procedure) { d.Strict = false },
			func(d *catalog.Procedure) { d.Volatility = expr.Immutable },
			func(d *catalog.Procedure) { d.RoutineKind = catalog.ProcedureRoutine },
			func(d *catalog.Procedure) { d.AccessMethod = catalog.NoAccessMethod },
			func(d *catalog.Procedure) { d.SQLDefaults = defaults },
			func(d *catalog.Procedure) {
				other, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 351})
				d.Builtin = other.Builtin
			},
		} {
			bad := *p
			change(&bad)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(bad.Address(), &bad)
			state.BindName(catalog.DescriptorNameKey(&bad), bad.Address())
			if err := (stored.ProcedureProvider{}).Validate(view.WithState(state), &bad); err == nil {
				t.Fatal("forged/mixed capability accepted", bad)
			}
		}
	}
	if catalog.NativeHandler(333) != catalog.NoAccessMethod {
		t.Fatal("lookup-only gin signature granted native implementation")
	}
}
