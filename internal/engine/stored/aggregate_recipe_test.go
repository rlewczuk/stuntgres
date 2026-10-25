package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This fixture exercises detached presentation authority, not SQL binding or
// execution. Public Consumers/Persistence/TemporaryRemap cover complete queries.
func aggregateRecipeFixture(t *testing.T) (catalog.Read, *View, parser.AggregateExpr, analyze.ViewLiteralPath, *catalog.Procedure) {
	t.Helper()
	read, relation, integer, _ := routineCaptureFixture(t)
	principals := new(authorization.Domain)
	roles := authorization.Seed(principals)
	bootstrap, ok := roles.Read(principals.Principal(authorization.BootstrapPrincipalID)).Lookup("postgres")
	if !ok {
		t.Fatal("missing real bootstrap principal")
	}
	read.Owner().Principals = principals
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "capture", Owner: bootstrap.Ref}
	read.State().SetDescriptor(database.Address(), database)
	declaration := routine.BuiltinRoutine(177)
	if declaration == nil {
		t.Fatal("missing real int4pl declaration")
	}
	support := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: declaration.Identity.Namespace}, ObjectRevision: 1, ObjectName: declaration.Identity.Name, Signature: declaration.Identity.Signature, RoutineKind: declaration.Kind, Result: declaration.Result, Strict: declaration.Strict, Volatility: declaration.Volatility, Builtin: declaration, Owner: bootstrap.Ref}
	read.State().SetDescriptor(support.Address(), support)
	aggregate := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 903}, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, ObjectName: "aggregate_recipe", Signature: "23", RoutineKind: catalog.AggregateRoutine, Result: 23, Volatility: expr.Immutable, Owner: bootstrap.Ref, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 903, Kind: "n", Functions: [8]ids.OID{177}, TransitionType: 23, HasInitial: true, Initial: "0", FinalModify: "r", MovingFinalModify: "r"}}}
	read.State().SetDescriptor(aggregate.Address(), aggregate)
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.AggregateExpr{Name: aggregate.Name(), Schema: &parser.Identifier{Text: "capture"}, StoredAggregate: true, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}}
	path := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}
	literalPath := path
	literalPath.Node = 1
	ref := read.Owner().Domain.Ref(read.Epoch(), aggregate.Address())
	view := NewView(ViewParts{ExpressionSyntax: raw, Frames: map[uint32]*plan.ProjectNode{1: {}}, Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: aggregate.Revision()}}, RoutineRefs: map[analyze.ViewLiteralPath]ids.ObjectRef{path: ref}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{literalPath: {Info: expr.Info{Usage: integer.Usage()}, Value: value}}})
	return read, view, raw, path, aggregate
}

func TestCreateAggregateFrozenRecipeAuthority(t *testing.T) {
	for _, kind := range []string{"valid", "missing-marker", "missing-reference", "foreign-reference", "stale-epoch", "subobject-reference", "missing-dependency", "zero-revision", "wrong-kind", "wrong-payload", "wrong-shape", "missing-literal", "extra-reference"} {
		t.Run(kind, func(t *testing.T) {
			read, view, raw, path, aggregate := aggregateRecipeFixture(t)
			if err := view.ValidateLiterals(read); err != nil {
				t.Fatal("valid detached control refused", err)
			}
			switch kind {
			case "missing-marker":
				raw.StoredAggregate = false
			case "missing-reference":
				view.routineRefs = nil
			case "foreign-reference":
				view.routineRefs[path] = new(ids.Domain).Ref(read.Epoch(), aggregate.Address())
			case "stale-epoch":
				view.routineRefs[path] = read.Owner().Domain.Ref(read.Epoch()+1, aggregate.Address())
			case "subobject-reference":
				address := aggregate.Address()
				address.SubID = 1
				view.routineRefs[path] = read.Owner().Domain.Ref(read.Epoch(), address)
			case "missing-dependency":
				view.dependencies = nil
			case "zero-revision":
				view.dependencies[0].Revision = 0
			case "wrong-kind":
				changed := *aggregate
				changed.RoutineKind = catalog.FunctionRoutine
				read.State().SetDescriptor(changed.Address(), &changed)
			case "wrong-payload":
				changed := *aggregate
				payload := *aggregate.Aggregate
				payload.Definition.Functions[0] = 0
				changed.Aggregate = &payload
				read.State().SetDescriptor(changed.Address(), &changed)
			case "wrong-shape":
				raw.Args, raw.Star = nil, true
			case "missing-literal":
				view.literals = nil
			case "extra-reference":
				extra := path
				extra.Node = 99
				view.routineRefs[extra] = view.routineRefs[path]
			}
			view.expressionSyntax = raw
			err := view.ValidateLiterals(read)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s detached validation=%v", kind, err)
			}
			if kind == "extra-reference" {
				return // Whole-tree counting, not one occurrence, rejects this.
			}
			renderer := NewQueryRenderer(view)
			ordinal := 0
			_, err = renderer.ExpressionSQLAt(context.Background(), read, raw, path, &ordinal, DeparseParent{})
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s detached rendering=%v", kind, err)
			}
		})
	}
}

func TestCreateAggregateTransitionFrameRemap(t *testing.T) {
	read, _, integer, boolean := routineCaptureFixture(t)
	var current *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == integer.ID() {
			current = typ
			break
		}
	}
	if current == nil || current == integer {
		t.Fatal("missing independent destination type generation")
	}
	descriptor := &catalog.Type{SQLType: current}
	read.State().SetDescriptor(descriptor.Address(), descriptor)
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	if _, err := RemapExpression(parameter, nil, nil); err == nil {
		t.Fatal("ordinary persisted external parameter was admitted")
	}
	for _, kind := range []string{"valid", "references-only", "source-type", "source-typmod", "destination-type", "destination-typmod", "formal-count", "out-of-frame", "nonpositive", "untyped", "untyped-source-vector", "untyped-destination-vector", "references-only-destination"} {
		t.Run(kind, func(t *testing.T) {
			node := parameter
			source := []sqltype.Usage{integer.Usage()}
			destination := []sqltype.Usage{current.Usage()}
			mapper := &ViewQueryRemapper{Catalog: read, routineFormals: 64, Type: func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == integer {
					return current
				}
				return old
			}}
			want := current
			switch kind {
			case "references-only":
				mapper.referencesOnly = true
				destination[0], want = integer.Usage(), integer
			case "source-type":
				source[0] = boolean.Usage()
			case "source-typmod":
				source[0].Typmod = 99
			case "destination-type":
				destination[0] = boolean.Usage()
			case "destination-typmod":
				destination[0].Typmod = 99
			case "formal-count":
				destination = nil
			case "out-of-frame":
				node.Number = 2
			case "nonpositive":
				node.Number = 0
			case "untyped":
				node.Usage = sqltype.Usage{}
			case "untyped-source-vector":
				node.Usage, source[0] = sqltype.Usage{}, sqltype.Usage{}
			case "untyped-destination-vector":
				destination[0] = sqltype.Usage{}
			case "references-only-destination":
				mapper.referencesOnly = true
			}
			next, err := mapper.transitionExpression(node, source, destination)
			valid := kind == "valid" || kind == "references-only"
			if (err == nil) != valid {
				t.Fatalf("%s private formal mapping=%v", kind, err)
			}
			if valid {
				mapped, ok := next.(plan.ParameterExpr)
				if !ok || mapped.Number != 1 || mapped.ResultUsage() != want.Usage() || !mapped.Properties().ParameterDependent {
					t.Fatal("private formal namespace/generation lost", next)
				}
			}
			if parameter.ResultUsage().Type != integer {
				t.Fatal("source parameter generation mutated")
			}
		})
	}
}

func TestCreateAggregatePolymorphicTransitionRemap(t *testing.T) {
	build := func(oid ids.OID) (catalog.Read, *catalog.Procedure) {
		state := catalog.NewState(1)
		for _, typ := range sqltype.Builtins() {
			d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
			state.SetDescriptor(d.Address(), d)
		}
		procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
		for i := range procedures {
			state.SetDescriptor(procedures[i].Address(), &procedures[i])
		}
		aggregate := &catalog.Procedure{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid},
			ObjectName:    "remapped_polymorphic", Signature: "26", RoutineKind: catalog.AggregateRoutine, Result: 1021,
			Aggregate: &catalog.OwnedAggregate{
				Definition:   catalog.AggregateDefinition{Function: oid, Kind: "n", Functions: [8]ids.OID{378}, TransitionType: 1021, HasInitial: true, Initial: "{}", FinalModify: "r", MovingFinalModify: "r"},
				BinaryRecipe: &catalog.AggregateBinaryRecipe{Casts: []catalog.Cast{{OID: 91000, ObjectRevision: 1, Source: sqltype.Oid, Target: sqltype.Float4, Method: "b", Context: "i"}}},
			},
		}
		state.SetDescriptor(aggregate.Address(), aggregate)
		return catalog.NewRead(nil, state), aggregate
	}
	old, aggregate := build(91001)
	next, destination := build(92001)
	array, _ := old.LookupType(1021)
	oid, _ := old.LookupType(sqltype.Oid)
	real, _ := old.LookupType(sqltype.Float4)
	support, _ := old.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 378})
	inputs := []sqltype.Usage{array.Usage(), oid.Usage()}
	formals := []sqltype.Usage{array.Usage(), real.Usage()}
	call := functions.ArrayTransform{
		Info: expr.Info{Usage: array.Usage(), RoutineReference: support},
		Kind: functions.ArrayAppend, Namespace: "pg_catalog", Slots: [3]uint8{0, 1},
		Args: []expr.Expr{
			plan.ParameterExpr{Info: expr.Info{Usage: formals[0], Property: expr.Properties{ParameterDependent: true}}, Number: 1},
			plan.ParameterExpr{Info: expr.Info{Usage: formals[1], Property: expr.Properties{ParameterDependent: true}}, Number: 2},
		},
	}
	provider := &exec.TransitionAggregate{Aggregate: aggregate, Support: support, StateUsage: array.Usage(), Result: array.Usage(), Inputs: inputs, Formals: formals, Call: call, Conversions: make([]sqltype.CoercionPlan, 2)}
	for i := range inputs {
		var err error
		provider.Conversions[i], err = catalog.AggregateConversion(aggregate.Aggregate.BinaryRecipe, inputs[i], formals[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{aggregate.Address(): destination.Address()}
	mapper := &ViewQueryRemapper{Catalog: next, Procedure: extension.ProcedureRemapper(mapping, nil, nil), Type: func(source *sqltype.Descriptor) *sqltype.Descriptor {
		target, _ := next.LookupType(source.ID())
		return target
	}, addressMapper: func(source ids.ObjectAddress) ids.ObjectAddress {
		if source == aggregate.Address() {
			return destination.Address()
		}
		return source
	}}
	var slot functions.AggregateProvider = provider
	if err := mapper.provider(&slot, provider, nil); err != nil {
		t.Fatal("actual historical polymorphic provider remap", err)
	}
	mapped := slot.(*exec.TransitionAggregate)
	newArray, _ := next.LookupType(1021)
	newOID, _ := next.LookupType(sqltype.Oid)
	newReal, _ := next.LookupType(sqltype.Float4)
	if mapped.Aggregate == aggregate || mapped.Aggregate == destination || mapped.Aggregate.Address() != destination.Address() || mapped.Support != support || mapped.StateUsage != newArray.Usage() || mapped.Result != newArray.Usage() || mapped.Inputs[1] != newOID.Usage() || mapped.Formals[0] != newArray.Usage() || mapped.Formals[1] != newReal.Usage() {
		t.Fatal("transition remap replaced an original declaration or lost frame ownership")
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	state, err := mapped.InitWithContext(context.Background(), evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"1065353216", "1073741824", ""} {
		value := sqltype.Null(newOID)
		if text != "" {
			value, err = newOID.Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := mapped.Advance(context.Background(), state, []sqltype.Value{value}, evaluation); err != nil {
			t.Fatal("remapped actual array_append transition", err)
		}
	}
	value, err := mapped.Finalize(context.Background(), state)
	if err != nil || value.Type() != newArray || value.ArrayLen() != 3 || !value.ArrayElement(2).IsNull() {
		t.Fatal("remapped transition result ownership/NULL", value, err)
	}
	for i, want := range []string{"1", "2"} {
		element := value.ArrayElement(i)
		text, err := newReal.Output(context.Background(), element)
		if err != nil || element.Type() != newReal || text != want {
			t.Fatal("historical binary adapter lost in provider remap", i, text, err)
		}
	}
}

func TestAggregateFinalFrameRemap(t *testing.T) {
	build := func(oid ids.OID) (catalog.Read, *catalog.Procedure, *catalog.Procedure, *catalog.Procedure) {
		state := catalog.NewState(1)
		for _, typ := range sqltype.Builtins() {
			d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
			state.SetDescriptor(d.Address(), d)
		}
		var step, final *catalog.Procedure
		procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
		for i := range procedures {
			p := &procedures[i]
			state.SetDescriptor(p.Address(), p)
			if p.Name() == "int4pl" && p.Signature == "23,23" {
				step = p
			}
			if p.Name() == "chr" && p.Signature == "23" {
				final = p
			}
		}
		if step == nil || final == nil {
			t.Fatal("missing int4pl/chr")
		}
		aggregate := &catalog.Procedure{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectRevision: 1,
			ObjectName: "final_frame", Signature: "23", RoutineKind: catalog.AggregateRoutine, Result: 25,
			Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: oid, Kind: "n", Functions: [8]ids.OID{step.Address().ObjectID, final.Address().ObjectID}, TransitionType: 23, HasInitial: true, Initial: "0", FinalModify: "w", MovingFinalModify: "r"}},
		}
		state.SetDescriptor(aggregate.Address(), aggregate)
		return catalog.NewRead(nil, state), aggregate, step, final
	}
	old, aggregate, step, final := build(94001)
	next, destination, _, _ := build(95001)
	integer, _ := old.LookupType(sqltype.Int4)
	text, _ := old.LookupType(sqltype.Text)
	newInteger, _ := next.LookupType(sqltype.Int4)
	newText, _ := next.LookupType(sqltype.Text)
	parameter := func(usage sqltype.Usage, number int) expr.Expr {
		return plan.ParameterExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{ParameterDependent: true}}, Number: number}
	}
	// Real scalar string nodes exercise the same closed-frame remapper used by
	// selected builtins. SQL support borrows its proc.Frame without an expression.
	finalCall, err := functions.NewString(expr.Info{Usage: text.Usage(), RoutineReference: final}, "chr", text.Usage(), []expr.Expr{parameter(integer.Usage(), 1)}, [4]bool{}, parser.OrdinaryStringSyntax, false)
	if err != nil {
		t.Fatal(err)
	}
	addition, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, integer, integer, integer)
	if err != nil {
		t.Fatal(err)
	}
	stepCall := plan.ArithmeticExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: step}, Left: parameter(integer.Usage(), 1), Right: parameter(integer.Usage(), 2), Provider: addition, Operator: sqltype.ArithmeticAdd, Namespace: "pg_catalog"}
	conversion, err := sqltype.PlanCoercion(integer.Usage(), integer.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	p := &exec.TransitionAggregate{
		Aggregate: aggregate, Support: step, StateUsage: integer.Usage(), Result: text.Usage(),
		Inputs: []sqltype.Usage{integer.Usage(), integer.Usage()}, Formals: []sqltype.Usage{integer.Usage(), integer.Usage()},
		Conversions: []sqltype.CoercionPlan{conversion, conversion}, Call: stepCall,
		Final: final, FinalInputs: []sqltype.Usage{integer.Usage()}, FinalFormals: []sqltype.Usage{integer.Usage()}, FinalConversions: []sqltype.CoercionPlan{conversion}, FinalCall: finalCall,
	}
	for _, kind := range []string{"valid", "references-only", "references-only-stale-final", "out-of-frame", "nonpositive", "foreign-usage", "wrong-address", "changed-declaration", "wrong-result", "wrong-formal", "wrong-conversion", "missing-final"} {
		t.Run(kind, func(t *testing.T) {
			provider := *p
			call := finalCall
			call.Args = append([]expr.Expr(nil), finalCall.Args...)
			switch kind {
			case "out-of-frame":
				call.Args[0] = parameter(integer.Usage(), 2)
			case "nonpositive":
				call.Args[0] = parameter(integer.Usage(), 0)
			case "foreign-usage":
				call.Args[0] = parameter(newInteger.Usage(), 1)
			case "wrong-address", "changed-declaration":
				bad := *final
				if kind == "wrong-address" {
					bad.ObjectAddress.ObjectID++
				} else {
					bad.Strict = !bad.Strict
				}
				call.RoutineReference = &bad
			case "wrong-result":
				provider.Result = integer.Usage()
			case "wrong-formal":
				provider.FinalFormals = []sqltype.Usage{text.Usage()}
			case "wrong-conversion":
				provider.FinalConversions = []sqltype.CoercionPlan{{}}
			case "missing-final":
				provider.Final = nil
			case "references-only-stale-final":
				bad := *final
				bad.Strict = !bad.Strict
				provider.Final = &bad
				call.RoutineReference = &bad
			}
			provider.FinalCall = call
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{aggregate.Address(): destination.Address()}
			mapType := func(source *sqltype.Descriptor) *sqltype.Descriptor {
				// Deliberately non-idempotent: a second map of a destination
				// generation is invalid, rather than accidentally harmless.
				if source == integer {
					return newInteger
				}
				if source == text {
					return newText
				}
				if source == newInteger || source == newText {
					return nil
				}
				return source
			}
			mapper := &ViewQueryRemapper{Catalog: next, routineFormals: 64, Procedure: extension.ProcedureRemapper(mapping, nil, nil), Type: mapType}
			if kind == "references-only" || kind == "references-only-stale-final" {
				mapper.referencesOnly = true
				mapper.Type = func(source *sqltype.Descriptor) *sqltype.Descriptor { return source }
			}
			var slot functions.AggregateProvider = &provider
			err := mapper.provider(&slot, &provider, nil)
			valid := kind == "valid" || kind == "references-only"
			if (err == nil) != valid {
				t.Fatal(kind, err)
			}
			if !valid {
				if slot != &provider {
					t.Fatal("failed remap replaced provider")
				}
				return
			}
			mapped := slot.(*exec.TransitionAggregate)
			wantState, wantResult := newInteger, newText
			if kind == "references-only" {
				wantState, wantResult = integer, text
			}
			if mapped.Aggregate.Result != 25 || mapped.Aggregate.Aggregate.Definition.TransitionType != 23 || mapped.Result.Type != wantResult || mapped.StateUsage.Type != wantState || mapped.FinalInputs[0].Type != wantState || mapped.FinalFormals[0].Type != wantState || mapped.FinalCall.ResultUsage().Type != wantResult {
				t.Fatal("distinct result/final frame remap lost", mapped)
			}
			mapped.FinalInputs[0] = sqltype.Usage{}
			mapped.FinalFormals[0] = sqltype.Usage{}
			if p.FinalInputs[0] != integer.Usage() || p.FinalFormals[0] != integer.Usage() || finalCall.Args[0].ResultUsage() != integer.Usage() || aggregate.Result != 25 {
				t.Fatal("remapping mutated source declaration/frame")
			}
		})
	}
}

func TestAggregateFinalExtraFrameRemap(t *testing.T) {
	// Distinct domain IDs force result remapping independently of STYPE; SQL
	// support vectors also retain the complete typed-NULL extra namespace.
	oldState, newState := catalog.NewState(1), catalog.NewState(1)
	for _, state := range []*catalog.State{oldState, newState} {
		for _, typ := range sqltype.Builtins() {
			d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
			state.SetDescriptor(d.Address(), d)
		}
	}
	oldRead, newRead := catalog.NewRead(nil, oldState), catalog.NewRead(nil, newState)
	integer, _ := oldRead.LookupType(sqltype.Int4)
	text, _ := oldRead.LookupType(sqltype.Text)
	newInteger, _ := newRead.LookupType(sqltype.Int4)
	newText, _ := newRead.LookupType(sqltype.Text)
	oldBase, newBase := text.Usage(), newText.Usage()
	oldBase.Collation, _ = sqltype.RegisteredCollation(950, 950)
	var err error
	newBase.Collation, err = sqltype.RegisteredCollation(951, 950)
	if err != nil {
		t.Fatal(err)
	}
	oldDomain, err := sqltype.Domain(96001, "old_final", "old_final", oldBase)
	if err != nil {
		t.Fatal(err)
	}
	newDomain, err := sqltype.Domain(97001, "new_final", "new_final", newBase)
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []*sqltype.Descriptor{oldDomain, newDomain} {
		state := oldState
		if i == 1 {
			state = newState
		}
		d := &catalog.Type{SQLType: typ}
		state.SetDescriptor(d.Address(), d)
	}
	step := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96002}, ObjectRevision: 1, ObjectName: "extra_step", Signature: "23,23,25", RoutineKind: catalog.FunctionRoutine, Result: 23, SQL: &proc.Definition{}}
	final := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96003}, ObjectRevision: 1, ObjectName: "extra_final", Signature: "23,23,25", RoutineKind: catalog.FunctionRoutine, Result: 96001, SQL: &proc.Definition{}}
	aggregate := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96004}, ObjectRevision: 1, ObjectName: "extra_aggregate", Signature: "23,25", RoutineKind: catalog.AggregateRoutine, Result: 96001,
		Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 96004, Kind: "n", Functions: [8]ids.OID{96002, 96003}, TransitionType: 23, HasInitial: true, Initial: "0", FinalExtra: true, FinalModify: "s", MovingFinalModify: "r"}}}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for _, d := range []catalog.Descriptor{step, final, aggregate, &catalog.Type{SQLType: oldDomain}} {
		address := d.Address()
		target := address
		target.ObjectID += 1000
		mapping[address] = target
	}
	remapProcedure := func(old *catalog.Procedure) (*catalog.Procedure, error) {
		next := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
		if old.SQL != nil {
			copy := *next
			if old.Result == 96001 {
				copy.Result = 97001
			}
			next = &copy
		}
		return next, nil
	}
	for _, p := range []*catalog.Procedure{step, final, aggregate} {
		oldState.SetDescriptor(p.Address(), p)
		next, _ := remapProcedure(p)
		newState.SetDescriptor(next.Address(), next)
	}
	inputs := []sqltype.Usage{integer.Usage(), integer.Usage(), oldBase}
	formals, _, err := routine.AggregateSupportTypes(oldRead, nil, step, aggregate.Aggregate, inputs)
	if err != nil {
		t.Fatal(err)
	}
	p := &exec.TransitionAggregate{Aggregate: aggregate, Support: step, Final: final, StateUsage: integer.Usage(), Result: oldDomain.Usage(), InputCollation: oldBase.Collation,
		Inputs: inputs, Formals: formals, FinalInputs: append([]sqltype.Usage(nil), inputs...), FinalFormals: append([]sqltype.Usage(nil), formals...), Conversions: make([]sqltype.CoercionPlan, 3), FinalConversions: make([]sqltype.CoercionPlan, 3)}
	for i := range inputs {
		p.Conversions[i], err = catalog.AggregateConversion(nil, inputs[i], formals[i])
		if err != nil {
			t.Fatal(err)
		}
		p.FinalConversions[i] = p.Conversions[i]
	}
	mapper := &ViewQueryRemapper{Catalog: newRead, Procedure: remapProcedure, Type: func(old *sqltype.Descriptor) *sqltype.Descriptor {
		switch old {
		case integer:
			return newInteger
		case text:
			return newText
		case oldDomain:
			return newDomain
		case newInteger, newText, newDomain:
			return nil // Mapping a destination generation twice is invalid.
		default:
			return old
		}
	}, Collation: func(old sqltype.CollationRef) (sqltype.CollationRef, error) {
		if old.ID == 950 {
			return sqltype.RegisteredCollation(951, 950)
		}
		return old, nil
	}, routineFormals: 64}
	var slot functions.AggregateProvider = p
	if err := mapper.provider(&slot, p, nil); err != nil {
		t.Fatal(err)
	}
	mapped := slot.(*exec.TransitionAggregate)
	if mapped.Result.Type != newDomain || mapped.Aggregate.Result != 97001 || mapped.StateUsage.Type != newInteger || mapped.Aggregate.Aggregate.Definition.TransitionType != 23 || mapped.Final.Address().ObjectID != 97003 || mapped.Final.Result != 97001 || mapped.InputCollation.ID != 951 || len(mapped.FinalInputs) != 3 || mapped.FinalInputs[2].Type != newText || mapped.FinalInputs[2].Collation.ID != 951 || mapped.FinalFormals[2] != mapped.FinalInputs[2] {
		t.Fatal("final extra/result identity was conflated with state", mapped)
	}
	param := plan.ParameterExpr{Info: expr.Info{Usage: p.FinalFormals[2], Property: expr.Properties{ParameterDependent: true}}, Number: 3}
	if next, err := mapper.transitionExpression(param, p.FinalFormals, mapped.FinalFormals); err != nil || next.ResultUsage() != mapped.FinalFormals[2] {
		t.Fatal("final extra private parameter did not map exactly once", next, err)
	}
	param.Number = 4
	if _, err := mapper.transitionExpression(param, p.FinalFormals, mapped.FinalFormals); err == nil {
		t.Fatal("outer parameter count admitted an escaping final extra")
	}
	if p.Result.Type != oldDomain || p.FinalInputs[2] != oldBase || p.Final.Result != 96001 || p.Aggregate.Result != 96001 {
		t.Fatal("final extra remap mutated source")
	}
}
