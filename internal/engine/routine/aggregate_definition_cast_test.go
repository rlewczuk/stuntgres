package routine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestCreateAggregateDetachedDeclaredCastSupport(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
	}
	a, err := sqltype.Enum(91000, "aggregate_cast_a", "public.aggregate_cast_a", []string{"red", "blue"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := sqltype.Enum(91002, "aggregate_cast_b", "public.aggregate_cast_b", []string{"red", "blue"})
	if err != nil {
		t.Fatal(err)
	}
	arrayA, err := sqltype.Array(91001, "_aggregate_cast_a", a)
	if err != nil {
		t.Fatal(err)
	}
	arrayB, err := sqltype.Array(91003, "_aggregate_cast_b", b)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []*catalog.Type{
		{SQLType: a, Array: arrayA.ID()},
		{SQLType: b, Array: arrayB.ID()},
		{SQLType: arrayA},
		{SQLType: arrayB},
	} {
		state.SetDescriptor(d.Address(), d)
	}
	view := catalog.NewRead(nil, state)
	support, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 378})
	if !ok {
		t.Fatal("missing real array_append")
	}

	// REL_18_6 anycompatible selection follows the one-way implicit edge.
	// Assignment and explicit declarations cannot satisfy implicit specialization.
	// These are specialization targets, not permission to execute a nonbinary
	// aggregate support conversion (validated separately by the DDL boundary).
	for _, tc := range []struct {
		name        string
		source      *sqltype.Descriptor
		target      *sqltype.Descriptor
		context     string
		wantElement *sqltype.Descriptor
		wantArray   *sqltype.Descriptor
	}{
		{"implicit_a_to_b", a, b, "i", b, arrayB},
		{"implicit_b_to_a", b, a, "i", a, arrayA},
		{"assignment_a_to_b", a, b, "a", nil, nil},
		{"assignment_b_to_a", b, a, "a", nil, nil},
		{"explicit_a_to_b", a, b, "e", nil, nil},
		{"explicit_b_to_a", b, a, "e", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := catalog.CloneState(state)
			cast := &catalog.Cast{OID: 91004, ObjectRevision: 1, Source: tc.source.ID(), Target: tc.target.ID(), Context: tc.context, Method: "i"}
			candidate.SetDescriptor(cast.Address(), cast)
			targets, result, err := AggregateSupportTypes(catalog.NewRead(nil, candidate), nil, support, nil, []sqltype.Usage{arrayA.Usage(), b.Usage()})
			if support.Signature != "5078,5077" || support.Result != 5078 || support.Address().ObjectID != 378 {
				t.Fatal("specialization rewrote the actual polymorphic declaration", support)
			}
			if tc.wantElement == nil {
				if err == nil {
					t.Fatalf("nonimplicit cast admitted specialization: targets=%v result=%v", targets, result)
				}
				return
			}
			if err != nil {
				t.Fatal("implicit declared cast refused specialization", err)
			}
			if len(targets) != 2 || targets[0] != tc.wantArray.Usage() || targets[1] != tc.wantElement.Usage() || result != tc.wantArray.Usage() {
				t.Fatalf("specialization targets=%v result=%v; want [%v %v] result=%v", targets, result, tc.wantArray.Usage(), tc.wantElement.Usage(), tc.wantArray.Usage())
			}
		})
	}
}

func TestCreateAggregateRetainedBinaryDefinition(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	var support *catalog.Procedure
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
		if procedures[i].Name() == "byteacat" && procedures[i].Signature == "17,17" {
			support = &procedures[i]
		}
	}
	if support == nil {
		t.Fatal("missing actual byteacat provider")
	}
	edge := catalog.Cast{OID: 91000, ObjectRevision: 1, Source: sqltype.Text, Target: sqltype.Bytea, Method: "b", Context: "i"}
	aggregate := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 91001},
		ObjectName:    "retained_binary", Signature: "25", RoutineKind: catalog.AggregateRoutine, Result: 17,
		Aggregate: &catalog.OwnedAggregate{
			Definition:   catalog.AggregateDefinition{Function: 91001, Kind: "n", Functions: [8]ids.OID{support.Address().ObjectID}, TransitionType: 17, FinalModify: "r", MovingFinalModify: "r"},
			BinaryRecipe: &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{edge}},
		},
	}
	for _, initialized := range []bool{false, true} {
		aggregate.Aggregate.Definition.HasInitial = initialized
		for _, live := range []*catalog.Cast{nil, &edge, {OID: 91002, ObjectRevision: 1, Source: sqltype.Text, Target: sqltype.Bytea, Method: "i", Context: "i"}} {
			next := catalog.CloneState(state)
			if live != nil {
				next.SetDescriptor(live.Address(), live)
			}
			if err := ValidateAggregateDefinition(catalog.NewRead(nil, next), aggregate); err != nil {
				t.Fatal("retained definition depended on the live cast row", initialized, live, err)
			}
		}
	}
	for _, mutate := range []func(*catalog.AggregateBinaryRecipe){
		func(r *catalog.AggregateBinaryRecipe) { r.Casts[0].Method = "i" },
		func(r *catalog.AggregateBinaryRecipe) { r.Casts[0].Context = "a" },
		func(r *catalog.AggregateBinaryRecipe) { r.Casts[0].Function = support.Address() },
		func(r *catalog.AggregateBinaryRecipe) { r.Casts[0].Target = sqltype.Int4 },
		func(r *catalog.AggregateBinaryRecipe) { r.Casts = append(r.Casts, r.Casts[0]) },
		func(r *catalog.AggregateBinaryRecipe) { r.Casts = nil },
	} {
		bad, owned := *aggregate, *aggregate.Aggregate
		recipe := &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{edge}}
		mutate(recipe)
		owned.BinaryRecipe, bad.Aggregate = recipe, &owned
		if err := ValidateAggregateDefinition(catalog.NewRead(nil, state), &bad); err == nil {
			t.Fatal("malformed retained binary recipe accepted", recipe)
		}
	}
}

func TestCreateAggregateRetainedPolymorphicBinaryDefinition(t *testing.T) {
	build := func(live *catalog.Cast) catalog.Read {
		state := catalog.NewState(1)
		for _, typ := range sqltype.Builtins() {
			d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
			state.SetDescriptor(d.Address(), d)
		}
		procedures := BuiltinProcedures(authorization.PrincipalRef{})
		for i := range procedures {
			state.SetDescriptor(procedures[i].Address(), &procedures[i])
		}
		if live != nil {
			state.SetDescriptor(live.Address(), live)
		}
		return catalog.NewRead(nil, state)
	}
	edge := catalog.Cast{OID: 91000, ObjectRevision: 1, Source: sqltype.Oid, Target: sqltype.Float4, Method: "b", Context: "i"}
	owner := &catalog.OwnedAggregate{
		Definition:   catalog.AggregateDefinition{Function: 91001, Kind: "n", Functions: [8]ids.OID{378}, TransitionType: 1021, HasInitial: true, Initial: "{}", FinalModify: "r", MovingFinalModify: "r"},
		BinaryRecipe: &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{edge}},
	}
	aggregate := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 91001},
		ObjectName:    "retained_polymorphic_binary", Signature: "26", RoutineKind: catalog.AggregateRoutine, Result: 1021, Aggregate: owner,
	}
	original := build(&edge)
	oldOID, _ := original.LookupType(sqltype.Oid)
	oldReal, _ := original.LookupType(sqltype.Float4)
	conversion, err := catalog.AggregateConversion(owner.BinaryRecipe, oldOID.Usage(), oldReal.Usage())
	if err != nil {
		t.Fatal(err)
	}
	oldWord, err := oldOID.Input(context.Background(), "1065353216", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, live := range []*catalog.Cast{
		&edge,
		nil,
		{OID: 91002, ObjectRevision: 1, Source: sqltype.Oid, Target: sqltype.Float4, Method: "i", Context: "a"},
		{OID: 91003, ObjectRevision: 1, Source: sqltype.Float4, Target: sqltype.Oid, Method: "b", Context: "i"},
	} {
		view := build(live) // Fresh descriptor generation, not shared datum ownership.
		support, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 378})
		array, _ := view.LookupType(1021)
		oid, _ := view.LookupType(sqltype.Oid)
		real, _ := view.LookupType(sqltype.Float4)
		inputs := []sqltype.Usage{array.Usage(), oid.Usage()}
		formals, result, err := AggregateSupportTypes(view, nil, support, owner, inputs)
		if err != nil || len(formals) != 2 || formals[0] != array.Usage() || formals[1] != real.Usage() || result != array.Usage() {
			t.Fatal("historical polymorphic specialization changed", live, formals, result, err)
		}
		if err := ValidateAggregateDefinition(view, aggregate); err != nil {
			t.Fatal("historical polymorphic declaration invalidated", live, err)
		}
		fixed := &catalog.OwnedAggregate{}
		if _, _, err := AggregateSupportTypes(view, nil, support, fixed, inputs); err == nil {
			t.Fatal("nil historical recipe borrowed a later live edge", live)
		}
		rebound, err := conversion.Rebind(oid.Usage(), real.Usage(), sqltype.Implicit)
		if err != nil {
			t.Fatal("historical physical conversion could not remap", err)
		}
		if _, err := rebound.Apply(context.Background(), oldWord, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err == nil {
			t.Fatal("remapped conversion admitted a foreign descriptor generation")
		}
		word, err := oid.Input(context.Background(), "1065353216", -1)
		if err != nil {
			t.Fatal(err)
		}
		value, err := rebound.Apply(context.Background(), word, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		if err != nil {
			t.Fatal("remapped physical binary conversion", err)
		}
		text, err := real.Output(context.Background(), value)
		if err != nil || value.Type() != real || text != "1" {
			t.Fatal("remapped binary word became a numeric or IO cast", text, err)
		}
	}
}

func TestAggregateFinalDetachedDefinition(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	var transition, final *catalog.Procedure
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		p := &procedures[i]
		state.SetDescriptor(p.Address(), p)
		if p.Name() == "byteacat" && p.Signature == "17,17" {
			transition = p
		}
		if p.Name() == "length" && p.Signature == "25" {
			final = p
		}
	}
	if transition == nil || final == nil {
		t.Fatal("missing real byteacat/length providers")
	}
	edge := catalog.Cast{OID: 93000, ObjectRevision: 1, Source: sqltype.Bytea, Target: sqltype.Text, Method: "b", Context: "i"}
	aggregate := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 93001},
		ObjectName:    "final_retained_binary", Signature: "17", RoutineKind: catalog.AggregateRoutine, Result: 23,
		Aggregate: &catalog.OwnedAggregate{
			Definition:   catalog.AggregateDefinition{Function: 93001, Kind: "n", Functions: [8]ids.OID{transition.Address().ObjectID, final.Address().ObjectID}, TransitionType: 17, HasInitial: true, Initial: "", FinalModify: "w", MovingFinalModify: "r"},
			BinaryRecipe: &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{edge}},
		},
	}
	for _, live := range []*catalog.Cast{nil, &edge, {OID: 93002, ObjectRevision: 1, Source: sqltype.Bytea, Target: sqltype.Text, Method: "i", Context: "a"}} {
		next := catalog.CloneState(state)
		if live != nil {
			next.SetDescriptor(live.Address(), live)
		}
		read := catalog.NewRead(nil, next)
		if err := ValidateAggregateDefinition(read, aggregate); err != nil {
			t.Fatal("final-only retained edge borrowed live cast authority", live, err)
		}
		source, _ := read.LookupType(sqltype.Bytea)
		target, _ := read.LookupType(sqltype.Text)
		conversion, err := catalog.AggregateConversion(aggregate.Aggregate.BinaryRecipe, source.Usage(), target.Usage())
		if err != nil {
			t.Fatal(err)
		}
		value, err := source.Input(context.Background(), `\x616263`, -1)
		if err != nil {
			t.Fatal(err)
		}
		result, err := conversion.Apply(context.Background(), value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := result.Text(); err != nil || got != "abc" || result.Type() != target {
			t.Fatal("final retained edge used runtime/IO coercion", got, err)
		}
		var newSource, newTarget *sqltype.Descriptor
		for _, typ := range sqltype.Builtins() {
			switch typ.ID() {
			case sqltype.Bytea:
				newSource = typ
			case sqltype.Text:
				newTarget = typ
			}
		}
		rebound, err := conversion.Rebind(newSource.Usage(), newTarget.Usage(), sqltype.Implicit)
		if err != nil {
			t.Fatal("retained final-only cast could not remap", err)
		}
		if _, err := rebound.Apply(context.Background(), value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err == nil {
			t.Fatal("remapped final cast admitted a source-generation value")
		}
		newValue, err := newSource.Input(context.Background(), `\x616263`, -1)
		if err != nil {
			t.Fatal(err)
		}
		remappedResult, err := rebound.Apply(context.Background(), newValue, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := remappedResult.Text(); err != nil || got != "abc" || remappedResult.Type() != newTarget {
			t.Fatal("remapped final-only physical cast changed", got, err)
		}
	}
	for _, kind := range []string{"wrong-result", "no-final", "missing-final", "wrong-final", "strict-extra", "mode", "unreferenced-edge", "runtime-edge", "missing-edge", "duplicate-edge", "wrong-signature"} {
		t.Run(kind, func(t *testing.T) {
			bad, owned := *aggregate, *aggregate.Aggregate
			bad.Aggregate = &owned
			recipe := &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{edge}}
			owned.BinaryRecipe = recipe
			switch kind {
			case "wrong-result":
				bad.Result = 25
			case "no-final":
				owned.Definition.Functions[1] = 0
			case "missing-final":
				owned.Definition.Functions[1] = 999999
			case "wrong-final":
				owned.Definition.Functions[1] = transition.Address().ObjectID
			case "strict-extra":
				owned.Definition.FinalExtra = true
			case "mode":
				owned.Definition.FinalModify = "x"
			case "unreferenced-edge":
				recipe.Casts = append(recipe.Casts, catalog.Cast{OID: 93003, ObjectRevision: 1, Source: sqltype.Oid, Target: sqltype.Float4, Method: "b", Context: "i"})
			case "runtime-edge":
				recipe.Casts[0].Method = "i"
			case "missing-edge":
				owned.BinaryRecipe = nil
			case "duplicate-edge":
				recipe.Casts = append(recipe.Casts, edge)
			case "wrong-signature":
				bad.Signature = "23"
			}
			if err := ValidateAggregateDefinition(catalog.NewRead(nil, state), &bad); err == nil {
				t.Fatal("invalid detached final declaration admitted", kind)
			}
		})
	}
	// EXTRA and all modification declarations remain meaningful catalog flags
	// even without a final; only result/state equality then becomes mandatory.
	noFinal, owned := *aggregate, *aggregate.Aggregate
	noFinal.Aggregate = &owned
	owned.Definition.Functions[1], owned.BinaryRecipe = 0, nil
	noFinal.Result = owned.Definition.TransitionType
	owned.Definition.FinalExtra = true
	for _, mode := range []string{"r", "s", "w"} {
		owned.Definition.FinalModify = mode
		if err := ValidateAggregateDefinition(catalog.NewRead(nil, state), &noFinal); err != nil {
			t.Fatal("unused final flags refused", mode, err)
		}
	}
}
