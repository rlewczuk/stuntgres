package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRoutineFormalInstalledOwnerRemap(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION pgrowlocks")
	enginetest.Run(t, e, "CREATE EXTENSION amcheck")
	view := e.CatalogRead()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for _, d := range view.Descriptors() {
		installed, ok := d.(*catalog.Extension)
		if !ok {
			continue
		}
		addresses := append([]ids.ObjectAddress{installed.Address(), installed.TargetNamespace}, installed.Members...)
		for _, address := range addresses {
			next := address
			next.ObjectID += 100000
			mapping[address] = next
		}
	}
	state := catalog.NewState(2)
	for address, d := range view.Descriptors() {
		if mapping[address] == (ids.ObjectAddress{}) {
			state.SetDescriptor(address, d)
			state.BindName(catalog.DescriptorNameKey(d), address)
			continue
		}
		if ns, ok := d.(*catalog.Namespace); ok {
			next := *ns
			next.ObjectAddress = mapping[address]
			state.SetDescriptor(next.Address(), &next)
			state.BindName(catalog.DescriptorNameKey(&next), next.Address())
			continue
		}
		next := extension.RemapDescriptor(d, mapping)
		state.SetDescriptor(next.Address(), next)
		state.BindName(catalog.DescriptorNameKey(next), next.Address())
	}
	mapped := view.WithState(state)
	seen := 0
	for _, d := range view.Descriptors() {
		original, ok := d.(*catalog.Procedure)
		if !ok || (original.Name() != "pgrowlocks" && original.Name() != "verify_heapam") {
			continue
		}
		seen++
		current := mapped.MustProcedure(mapping[original.Address()])
		if current == original || current.Installed != original.Installed || current.Signature != original.Signature || current.Result != original.Result || current.Extension != mapping[original.Extension] {
			t.Fatal("installed declaration remap lost immutable metadata", original.Name())
		}
		if symbol, err := extension.ProcedureSymbol(mapped, current); err != nil || symbol != original.Name() {
			t.Fatal("mapped installed identity", symbol, err)
		}
		if _, err := extension.ProcedureSymbol(mapped, original); err == nil {
			t.Fatal("source generation accepted after remap")
		}
		outputs := current.OutputDeclaration()
		if outputs == nil || !outputs.MatchesInputs(current.Signature) {
			t.Fatal("missing genuine installed output declaration")
		}
		types := make([]ids.OID, outputs.Len())
		names := make([]string, outputs.Inputs()+outputs.Len())
		for i := range types {
			types[i] = outputs.Type(i)
		}
		for i := range names {
			names[i] = outputs.Name(i)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Signature = "23" },
			func(p *catalog.Procedure) { p.Installed = catalog.InstalledRoutineDeclaration{} },
			func(p *catalog.Procedure) { p.Installed.Outputs = nil },
			func(p *catalog.Procedure) { p.Installed.Outputs = catalog.NewRoutineOutputs(types, names...) },
		} {
			bad := *current
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if _, err := extension.ProcedureSymbol(mapped, &bad); err == nil {
				t.Fatal("forged installed identity accepted", original.Name())
			}
			state.SetDescriptor(current.Address(), current)
		}
	}
	if seen != 2 {
		t.Fatal("missing installed formal owners", seen)
	}
}

func TestRoutineFormalConstructorOwnerRemap(t *testing.T) {
	source, target := engine.New(), engine.New()
	enginetest.Run(t, source, "CREATE TYPE rr_formal_span AS RANGE(SUBTYPE=integer,MULTIRANGE_TYPE_NAME=rr_formal_many)")
	enginetest.Run(t, target, "CREATE TYPE rr_formal_padding AS (v integer)")
	enginetest.Run(t, target, "CREATE TYPE rr_formal_span AS RANGE(SUBTYPE=integer,MULTIRANGE_TYPE_NAME=rr_formal_many)")
	before, after := source.CatalogRead(), target.CatalogRead()
	types := map[*sqltype.Descriptor]*sqltype.Descriptor{}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for _, d := range before.Descriptors() {
		old, ok := d.(*catalog.Type)
		if !ok {
			continue
		}
		for _, candidate := range after.Descriptors() {
			next, ok := candidate.(*catalog.Type)
			if ok && old.Name() == next.Name() && old.Namespace() == next.Namespace() {
				types[old.SQLType] = next.SQLType
				mapping[old.Address()] = next.Address()
				break
			}
		}
	}
	for _, d := range before.Descriptors() {
		old, ok := d.(*catalog.Procedure)
		if !ok || old.RangeConstructor == nil {
			continue
		}
		for _, candidate := range after.Descriptors() {
			next, ok := candidate.(*catalog.Procedure)
			if ok && next.RangeConstructor != nil && old.RangeConstructor.Form == next.RangeConstructor.Form {
				mapping[old.Address()] = next.Address()
				break
			}
		}
	}
	principal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return after.Owner().Principals.Principal(ref.ID()), nil
	}
	owners := catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal: principal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(ref)
	}})
	remap := stored.NewRoutineRemap(after, mapping, func(t *sqltype.Descriptor) *sqltype.Descriptor {
		if next := types[t]; next != nil {
			return next
		}
		return t
	}, owners, true)
	if err := remap.Declarations(); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range before.Descriptors() {
		original, ok := d.(*catalog.Procedure)
		if !ok || original.RangeConstructor == nil {
			continue
		}
		seen++
		originalType, originalSignature := original.RangeConstructor.Type, original.Signature
		current, err := remap.Procedure(original)
		if err != nil {
			t.Fatal(err)
		}
		if current == original || current.RangeConstructor.Type != types[originalType] || current.RangeConstructor.Type == originalType || current.RangeConstructor.Form != original.RangeConstructor.Form {
			t.Fatal("constructor retained foreign generation")
		}
		want, err := current.RangeConstructor.Signature(after)
		if err != nil || current.Signature != want {
			t.Fatal("mapped constructor signature", current.Signature, want, err)
		}
		if current.RangeConstructor.Form == catalog.MultirangeConstructorMany {
			if current.RangeConstructor.Variadic() != ids.OID(types[originalType].RangeType().ID()) || current.Signature == originalSignature {
				t.Fatal("variadic range element/array did not remap")
			}
		}
		if original.RangeConstructor.Type != originalType || original.Signature != originalSignature {
			t.Fatal("source constructor mutated")
		}
	}
	if seen != 5 {
		t.Fatal("expected five actual generated owners", seen)
	}
}
