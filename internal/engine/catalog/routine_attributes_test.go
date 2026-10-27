package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRoutineAttributeDeclarationOwnership(t *testing.T) {
	input := []string{"work_mem=64kB", "search_path=public, pg_catalog"}
	configuration := NewRoutineConfiguration(input)
	input[0] = "work_mem=999kB"
	if configuration.Len() != 2 || configuration.At(0) != "work_mem=64kB" || NewRoutineConfiguration(nil) != nil {
		t.Fatal("configuration borrowed mutable input")
	}
	source, target := new(authorization.Domain), new(authorization.Domain)
	owner := source.Principal(10)
	d := &Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001}, ObjectRevision: 7, Owner: owner, Strict: true, Volatility: expr.Immutable, SecurityDefiner: true, Parallel: 'r', Cost: 7, Rows: 12, Configuration: configuration}
	outer := &Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90002}, Owner: owner, Body: []RoutineBodyEdge{{Routine: d}, {Routine: d}}}
	state := NewState(1)
	state.SetDescriptor(d.Address(), d)
	state.SetDescriptor(outer.Address(), outer)
	mapPrincipal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return target.Principal(ref.ID()), nil
	}
	if err := RemapPrincipals(state, PrincipalRemap{Principal: mapPrincipal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return mapPrincipal(ref)
	}}); err != nil {
		t.Fatal(err)
	}
	mapped := state.MustProcedure(d.Address())
	mappedOuter := state.MustProcedure(outer.Address())
	if mapped == d || mapped.Configuration != configuration || mapped.Owner != target.Principal(10) || !mapped.SecurityDefiner || mapped.Parallel != 'r' || mapped.Cost != 7 || mapped.Rows != 12 || !mapped.Strict || mapped.Volatility != expr.Immutable || mapped.Revision() != 7 {
		t.Fatal("semantic declaration lost on principal remap", mapped)
	}
	if mappedOuter.Body[0].Routine != mapped || mappedOuter.Body[1].Routine != mapped {
		t.Fatal("shared original occurrence remapped twice")
	}
	patch := *mapped
	patch.Configuration = NewRoutineConfiguration([]string{"work_mem=128kB"})
	patch.Cost = 9
	if configuration.At(0) != "work_mem=64kB" || mapped.Cost != 7 || d.Owner != owner {
		t.Fatal("COW changed retained publication")
	}
	if SameRoutineConfiguration(patch.Configuration, configuration) || !SameRoutineConfiguration(NewRoutineConfiguration([]string{"work_mem=64kB", "search_path=public, pg_catalog"}), configuration) {
		t.Fatal("replay configuration equivalence")
	}
}
