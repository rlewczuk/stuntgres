package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestAggregateMovingDetachedDefinition(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	var forward, inverse, final ids.OID
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		p := &procedures[i]
		state.SetDescriptor(p.Address(), p)
		if p.Name() == "int4pl" && p.Signature == "23,23" {
			forward = p.Address().ObjectID
		}
		if p.Name() == "int4mi" && p.Signature == "23,23" {
			inverse = p.Address().ObjectID
		}
		if p.Name() == "abs" && p.Signature == "23" {
			final = p.Address().ObjectID
		}
	}
	if forward == 0 || inverse == 0 || final == 0 {
		t.Fatal("missing real builtin support declarations")
	}
	base := catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98010}, ObjectName: "moving_definition", RoutineKind: catalog.AggregateRoutine, Signature: "23", Result: 23, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 98010, Kind: "n", Functions: [8]ids.OID{forward, 0, 0, 0, 0, forward, inverse, final}, TransitionType: 23, HasInitial: true, Initial: "0", FinalModify: "r", MovingType: 23, HasMovingInitial: true, MovingInitial: "0", MovingSpace: -17, MovingFinalModify: "r"}}}
	read := catalog.NewRead(nil, state)
	if err := ValidateAggregateDefinition(read, &base); err != nil {
		t.Fatal("coherent moving definition", err)
	}
	for _, kind := range []string{"no_state", "no_forward", "no_inverse", "bad_state", "wrong_result", "strict_extra", "combine", "bad_mode", "seed_presence", "unused_edge"} {
		t.Run(kind, func(t *testing.T) {
			d, owned := base, *base.Aggregate
			d.Aggregate = &owned
			switch kind {
			case "no_state":
				owned.Definition.MovingType = 0
			case "no_forward":
				owned.Definition.Functions[5] = 0
			case "no_inverse":
				owned.Definition.Functions[6] = 0
			case "bad_state":
				owned.Definition.MovingType = 25
			case "wrong_result":
				d.Result = 25
			case "strict_extra":
				owned.Definition.MovingFinalExtra = true
			case "combine":
				owned.Definition.Functions[2] = forward
			case "bad_mode":
				owned.Definition.MovingFinalModify = "x"
			case "seed_presence":
				owned.Definition.HasMovingInitial = false
			case "unused_edge":
				owned.BinaryRecipe = &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{{OID: 98011, ObjectRevision: 1, Source: sqltype.Oid, Target: sqltype.Float4, Method: "b", Context: "i"}}}
			}
			if err := ValidateAggregateDefinition(read, &d); err == nil {
				t.Fatal("malformed moving definition admitted", kind)
			}
		})
	}
	for _, mode := range []string{"r", "s", "w"} {
		d, owned := base, *base.Aggregate
		d.Aggregate = &owned
		owned.Definition.Functions[5], owned.Definition.Functions[6], owned.Definition.Functions[7] = 0, 0, 0
		owned.Definition.MovingType, owned.Definition.MovingSpace = 0, 0
		owned.Definition.MovingInitial, owned.Definition.HasMovingInitial = "", false
		owned.Definition.MovingFinalExtra, owned.Definition.MovingFinalModify = true, mode
		if err := ValidateAggregateDefinition(read, &d); err != nil {
			t.Fatal("unused moving flags refused", mode, err)
		}
	}
}
