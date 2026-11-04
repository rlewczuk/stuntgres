package stored_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestAccessMethodHandlerRegistration(t *testing.T) {
	e := engine.New()
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
		{333, 325, catalog.GinAccessMethod, "ginhandler"},
		{335, 325, catalog.BrinAccessMethod, "brinhandler"},
		{334, 325, catalog.SpgistAccessMethod, "spghandler"},
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
		renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		renamed.ObjectRevision++
		renamed.Parallel, renamed.Cost = 'r', 7
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
			func(d *catalog.Procedure) { d.BrinSupport = 1 },
			func(d *catalog.Procedure) { d.GinSupport = 1 },
			func(d *catalog.Procedure) { d.Display.Source = "forged_handler" },
			func(d *catalog.Procedure) { d.Display.Language = catalog.CRoutineLanguage },
			func(d *catalog.Procedure) { d.SecurityDefiner = true },
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
	if catalog.NativeHandler(333) != catalog.GinAccessMethod {
		t.Fatal("released GIN handler lost native implementation")
	}
}

func TestIntrinsicSupportRegistration(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	for _, seed := range catalog.GinSupportProcedures(view.Owner().Principals.Principal(10)) {
		p := view.MustProcedure(seed.Address())
		moved := *p
		moved.ObjectName += "_renamed"
		moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		moved.ObjectRevision++
		moved.Parallel, moved.Cost = 'r', 7
		state := catalog.CloneState(view.State())
		state.UnbindName(catalog.DescriptorNameKey(p))
		state.SetDescriptor(moved.Address(), &moved)
		state.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
		if !catalog.OriginalGinSupport(&moved) {
			t.Fatal("supported COW revoked original GIN identity", seed.Name())
		}
		if err := (stored.ProcedureProvider{}).Validate(view.WithState(state), &moved); err != nil {
			t.Fatal("supported GIN COW rejected", seed.Name(), err)
		}
		for _, change := range []func(*catalog.Procedure){
			func(d *catalog.Procedure) { d.ObjectAddress.ObjectID = 90000 },
			func(d *catalog.Procedure) { d.GinSupport = 0 },
			func(d *catalog.Procedure) { d.GinSupport = d.GinSupport%10 + 1 },
			func(d *catalog.Procedure) { d.BrinSupport = 1 },
			func(d *catalog.Procedure) { d.AccessMethod = catalog.HeapAccessMethod },
			func(d *catalog.Procedure) { d.Signature = "23" },
			func(d *catalog.Procedure) { d.Result = 0 },
			func(d *catalog.Procedure) { d.Strict = false },
			func(d *catalog.Procedure) { d.Volatility = expr.Volatile },
			func(d *catalog.Procedure) { d.Display.Source = "forged_support" },
			func(d *catalog.Procedure) { d.Display.Language = catalog.CRoutineLanguage },
			func(d *catalog.Procedure) { d.SecurityDefiner = true },
			func(d *catalog.Procedure) { d.Builtin = &catalog.BuiltinRoutineDeclaration{} },
		} {
			bad := *p
			change(&bad)
			if err := catalog.ValidateGinSupport(&bad); err == nil {
				t.Fatal("forged or unsupported GIN capability accepted", seed.Name())
			}
		}
	}
}
