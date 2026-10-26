package routine

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestAggregateOrderedFinalDeclarationVectors(t *testing.T) {
	types := make(map[sqltype.ID]sqltype.Usage)
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ.Usage() }
	integer, moving, text, anyType, internal := types[sqltype.Int4], types[sqltype.Int8], types[sqltype.Text], types[2276], types[2281]
	for _, tc := range []struct {
		name string
		state sqltype.Usage
		declarations []sqltype.Usage
		direct int
		extra bool
		want []sqltype.Usage
	}{
		{"ordinary", integer, []sqltype.Usage{integer}, 0, false, []sqltype.Usage{integer}},
		{"ordinary extra", integer, []sqltype.Usage{integer}, 0, true, []sqltype.Usage{integer, integer}},
		{"ordered direct", integer, []sqltype.Usage{text, integer}, 1, false, []sqltype.Usage{integer, text}},
		{"ordered extra", integer, []sqltype.Usage{text, integer}, 1, true, []sqltype.Usage{integer, text, integer}},
		{"moving direct", moving, []sqltype.Usage{text, integer}, 1, false, []sqltype.Usage{moving, text}},
		{"moving extra", moving, []sqltype.Usage{text, integer}, 1, true, []sqltype.Usage{moving, text, integer}},
		{"paired any", internal, []sqltype.Usage{anyType}, 1, true, []sqltype.Usage{internal, anyType}},
		{"paired any direct", internal, []sqltype.Usage{anyType}, 1, false, []sqltype.Usage{internal, anyType}},
		{"prefixed paired any", internal, []sqltype.Usage{integer, anyType}, 2, true, []sqltype.Usage{internal, integer, anyType}},
		{"row only any", internal, []sqltype.Usage{anyType}, 0, false, []sqltype.Usage{internal}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]sqltype.Usage(nil), tc.declarations...)
			got := aggregateFinalDeclarationInputs(tc.state, tc.declarations, tc.direct, tc.extra)
			if !slices.Equal(got, tc.want) { t.Fatalf("final declaration vector = %v, want %v", got, tc.want) }
			if len(got) > 1 { got[1] = sqltype.Usage{} }
			if !slices.Equal(tc.declarations, before) { t.Fatal("final vector aliases canonical declaration storage") }
		})
	}
}

func TestAggregateOrderedCanonicalStrictDeclaration(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	var plus, minus, mixedPlus, mixedMinus ids.OID
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		d := &procedures[i]
		state.SetDescriptor(d.Address(), d)
		switch {
		case d.Name() == "int4pl" && d.Signature == "23,23": plus = d.Address().ObjectID
		case d.Name() == "int4mi" && d.Signature == "23,23": minus = d.Address().ObjectID
		case d.Name() == "int42pl" && d.Signature == "23,21": mixedPlus = d.Address().ObjectID
		case d.Name() == "int42mi" && d.Signature == "23,21": mixedMinus = d.Address().ObjectID
		}
	}
	if plus == 0 || minus == 0 || mixedPlus == 0 || mixedMinus == 0 { t.Fatal("missing real strict transition declarations") }
	read := catalog.NewRead(nil, state)
	for _, moving := range []bool{false, true} {
		name := "ordinary transition"
		if moving { name = "moving transition" }
		t.Run(name, func(t *testing.T) {
			d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 94010}, ObjectName: "canonical_seed", RoutineKind: catalog.AggregateRoutine, Signature: "25,23", Result: 23,
				Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 94010, Kind: "o", DirectArgs: 1, Functions: [8]ids.OID{plus}, TransitionType: 23, FinalModify: "w", MovingFinalModify: "w"}}}
			a := &d.Aggregate.Definition
			if moving {
				a.HasInitial, a.Initial = true, "0"
				a.MovingType, a.Functions[5], a.Functions[6] = 23, plus, minus
			}
			if err := ValidateAggregateDefinition(read, d); err == nil { t.Fatal("row-compatible state admitted incompatible first direct declaration without seed") }
			if moving { a.HasMovingInitial, a.MovingInitial = true, "0" } else { a.HasInitial, a.Initial = true, "0" }
			if err := ValidateAggregateDefinition(read, d); err != nil { t.Fatal("explicit seed did not repair canonical-first admission", err) }

			// The converse must use the declaration as well: int4 direct is
			// binary-compatible with state even though the int2 row is not.
			d.Signature = "23,21"
			a.Functions[0] = mixedPlus
			if moving {
				a.Functions[5], a.Functions[6] = mixedPlus, mixedMinus
				a.HasMovingInitial, a.MovingInitial = false, ""
			} else { a.HasInitial, a.Initial = false, "" }
			if err := ValidateAggregateDefinition(read, d); err != nil { t.Fatal("compatible first canonical direct declaration rejected in favor of row suffix", err) }
		})
	}
}

func TestAggregateOrderedMovingFinalDetachedDefinition(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
	}
	read := catalog.NewRead(nil, state)
	for _, extra := range []bool{false, true} {
		for _, movingExtra := range []bool{false, true} {
			// Real nonstrict array_position overloads distinguish the direct
			// vector from the complete EXTRA vector without fake supports.
			final, movingFinal := ids.OID(3277), ids.OID(3277)
			if extra {
				final = 3278
			}
			if movingExtra {
				movingFinal = 3278
			}
			d := &catalog.Procedure{
				ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 94011},
				ObjectName: "moving_final_vectors", RoutineKind: catalog.AggregateRoutine, Signature: "23,23", Result: 23,
				Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{
					Function: 94011, Kind: "o", DirectArgs: 1,
					Functions: [8]ids.OID{378, final, 0, 0, 0, 378, 3167, movingFinal},
					TransitionType: 1007, MovingType: 1007,
					FinalExtra: extra, MovingFinalExtra: movingExtra,
					FinalModify: "w", MovingFinalModify: "w",
				}},
			}
			if err := ValidateAggregateDefinition(read, d); err != nil {
				t.Fatalf("independent ordinary/moving EXTRA vectors (%t,%t): %v", extra, movingExtra, err)
			}
			for _, slot := range []int{1, 7} {
				before := d.Aggregate.Definition.Functions[slot]
				wrong := ids.OID(3278)
				if before == wrong {
					wrong = 3277
				}
				d.Aggregate.Definition.Functions[slot] = wrong
				if err := ValidateAggregateDefinition(read, d); err == nil {
					t.Fatalf("wrong final arity admitted for slot %d, EXTRA (%t,%t)", slot, extra, movingExtra)
				}
				d.Aggregate.Definition.Functions[slot] = before
			}

			// Both implementations must use the single collapsed ANY slot,
			// irrespective of their independently selected EXTRA flags.
			d.Signature, d.Result = "2276", 20
			d.Aggregate.Variadic = true
			a := &d.Aggregate.Definition
			a.Kind, a.TransitionType, a.MovingType = "h", 2281, 2281
			a.Functions = [8]ids.OID{3971, 3987, 0, 0, 0, 3971, 3971, 3987}
			if err := ValidateAggregateDefinition(read, d); err != nil {
				t.Fatalf("collapsed ordinary/moving ANY vectors (%t,%t): %v", extra, movingExtra, err)
			}
			d.Signature, a.DirectArgs = "23,2276", 2
			if err := ValidateAggregateDefinition(read, d); err == nil {
				t.Fatal("fixed-prefix canonical declaration expanded into rank_final")
			}
		}
	}
}
