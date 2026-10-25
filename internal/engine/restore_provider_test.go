package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
)

func replacementForeignFixture(t *testing.T, callback func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error) (*core.Engine, *catalog.Procedure) {
	t.Helper()
	e := New()
	name := catalog.ForeignFunctionName{Schema: "public", Name: "replacement_validator"}
	if err := e.InstallForeignDefinitions(nil, []fdw.ValidatorDefinition{{Name: name, Validate: callback}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range e.CatalogRead().Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Foreign != nil && p.Foreign.Name == name {
			return e, p
		}
	}
	t.Fatal("installed validator missing")
	return nil, nil
}

func TestRestoreForeignProviderUsesDestinationRegistration(t *testing.T) {
	sourceCalls, targetCalls := 0, 0
	sourceError, targetError := errors.New("source callback"), errors.New("destination callback")
	source, original := replacementForeignFixture(t, func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error {
		sourceCalls++
		return sourceError
	})
	target, destination := replacementForeignFixture(t, func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error {
		targetCalls++
		return targetError
	})
	// Neither destination nor imported public labels are registration keys.
	current := catalog.CloneState(target.CatalogRead().State())
	renamedDestination := *destination
	renamedDestination.ObjectName = "destination_current_label"
	current.UnbindName(catalog.DescriptorNameKey(destination))
	current.SetDescriptor(destination.Address(), &renamedDestination)
	current.BindName(catalog.DescriptorNameKey(&renamedDestination), destination.Address())
	next := catalog.CloneState(source.CatalogRead().State())
	imported := *original
	imported.ObjectAddress.ObjectID = 901000
	imported.ObjectName = "imported_current_label"
	imported.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	next.RemoveDescriptor(original.Address())
	next.UnbindName(catalog.DescriptorNameKey(original))
	next.SetDescriptor(imported.Address(), &imported)
	next.BindName(catalog.DescriptorNameKey(&imported), imported.Address())
	if err := fdw.RebindReplacementProviders(target.CatalogRead().WithState(current), target.Foreign, next); err != nil {
		t.Fatal(err)
	}
	rebound := next.MustProcedure(imported.Address())
	if rebound.Foreign != destination.Foreign || rebound.Foreign == imported.Foreign || rebound.Foreign.Name != destination.Foreign.Name || rebound.Name() != imported.Name() || rebound.Namespace() != imported.Namespace() || rebound.Owner != imported.Owner || rebound.Address() != imported.Address() {
		t.Fatal("destination authority changed imported identity", rebound)
	}
	// Retained TEMP consumers compare the immutable declaration token exactly.
	// Rebinding an already-destination candidate must not mint another token.
	if err := fdw.RebindReplacementProviders(target.CatalogRead().WithState(current), target.Foreign, next); err != nil {
		t.Fatal(err)
	}
	if next.MustProcedure(imported.Address()) != rebound {
		t.Fatal("destination token was replaced on repeated admission")
	}
	if sourceCalls != 0 || targetCalls != 0 {
		t.Fatal("replacement invoked application callbacks")
	}
	if err := rebound.Foreign.Validator(context.Background(), catalog.ForeignServer, nil); !errors.Is(err, targetError) {
		t.Fatal("wrong callback", err)
	}
	if sourceCalls != 0 || targetCalls != 1 {
		t.Fatal("source callback transplanted", sourceCalls, targetCalls)
	}
	// SQL can drop the installed declaration without unregistering the
	// destination driver's capability. Reset reseeds that registration.
	current.RemoveDescriptor(destination.Address())
	current.UnbindName(catalog.DescriptorNameKey(&renamedDestination))
	next.SetDescriptor(imported.Address(), &imported)
	if err := fdw.RebindReplacementProviders(target.CatalogRead().WithState(current), target.Foreign, next); err != nil {
		t.Fatal("dropped declaration prevented registration reseed", err)
	}
	if sourceCalls != 0 || targetCalls != 1 {
		t.Fatal("reseed invoked application callbacks")
	}
	if err := next.MustProcedure(imported.Address()).Foreign.Validator(context.Background(), catalog.ForeignServer, nil); !errors.Is(err, targetError) || sourceCalls != 0 || targetCalls != 2 {
		t.Fatal("reseed did not use destination registration", err)
	}
	if original.Foreign != imported.Foreign || original.Name() == imported.Name() {
		t.Fatal("source provider was mutated")
	}
}

func TestRestoreForeignProviderRejectsMissingAndWrongCapability(t *testing.T) {
	source, original := replacementForeignFixture(t, func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error { return nil })
	target, _ := replacementForeignFixture(t, func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error { return nil })
	for _, change := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) {
			token := *p.Foreign
			token.Name.Name = "absent_registration"
			p.Foreign = &token
		},
		func(p *catalog.Procedure) { p.Signature = catalog.ForeignHandlerSignature },
		func(p *catalog.Procedure) { p.Result = 3115 },
		func(p *catalog.Procedure) { p.RoutineKind = catalog.ProcedureRoutine },
		func(p *catalog.Procedure) { token := *p.Foreign; token.Validator = nil; p.Foreign = &token },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
		func(p *catalog.Procedure) { p.Body = []catalog.RoutineBodyEdge{{Routine: original}} },
		func(p *catalog.Procedure) {
			p.Dictionary = ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 90001}
		},
	} {
		next := catalog.CloneState(source.CatalogRead().State())
		malformed := *original
		change(&malformed)
		next.SetDescriptor(malformed.Address(), &malformed)
		if err := fdw.RebindReplacementProviders(target.CatalogRead(), target.Foreign, next); err == nil {
			t.Fatal("incompatible foreign capability accepted", malformed)
		}
		if next.MustProcedure(malformed.Address()) != &malformed {
			t.Fatal("rejected capability was replaced")
		}
	}
	// Live source-compatible SQL metadata cannot invent a driver registration.
	next := catalog.CloneState(source.CatalogRead().State())
	if err := fdw.RebindReplacementProviders(target.CatalogRead(), fdw.Registry{}, next); err == nil {
		t.Fatal("unregistered destination accepted source callback")
	}
}
