package stored

import (
	"strings"
	"testing"

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

func TestAggregateMovingFrameRemap(t *testing.T) {
	t.Run("frozen_window", func(t *testing.T) {
		read, view, raw, path, aggregate := aggregateRecipeFixture(t)
		raw.Window = &parser.WindowSpec{}
		if selected, err := view.AggregateRecipeProcedure(read, path, raw); err != nil || selected != aggregate {
			t.Fatal("owned window lost its actual captured aggregate reference", selected, err)
		}
		raw.Distinct = true
		if _, err := view.AggregateRecipeProcedure(read, path, raw); err == nil {
			t.Fatal("invalid stored window DISTINCT admitted")
		}
		raw.Distinct = false
		view.routineRefs = nil
		if _, err := view.AggregateRecipeProcedure(read, path, raw); err == nil {
			t.Fatal("owned window admitted without captured identity")
		}
	})
	oldState, newState := catalog.NewState(1), catalog.NewState(1)
	for _, state := range []*catalog.State{oldState, newState} {
		for _, typ := range sqltype.Builtins() {
			d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
			state.SetDescriptor(d.Address(), d)
		}
		procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
		for i := range procedures {
			state.SetDescriptor(procedures[i].Address(), &procedures[i])
		}
	}
	oldRead, newRead := catalog.NewRead(nil, oldState), catalog.NewRead(nil, newState)
	integer, _ := oldRead.LookupType(sqltype.Int4)
	newInteger, _ := newRead.LookupType(sqltype.Int4)
	text, _ := oldRead.LookupType(sqltype.Text)
	newText, _ := newRead.LookupType(sqltype.Text)
	oldBase, newBase := text.Usage(), newText.Usage()
	var err error
	oldBase.Collation, err = sqltype.RegisteredCollation(950, 950)
	if err != nil {
		t.Fatal(err)
	}
	newBase.Collation, err = sqltype.RegisteredCollation(951, 950)
	if err != nil {
		t.Fatal(err)
	}
	oldDomain, err := sqltype.Domain(98001, "old_moving", "old_moving", oldBase)
	if err != nil {
		t.Fatal(err)
	}
	newDomain, err := sqltype.Domain(99001, "new_moving", "new_moving", newBase)
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
	ordinary := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98006}, ObjectRevision: 1, ObjectName: "ordinary_forward", Signature: "23,23,25", RoutineKind: catalog.FunctionRoutine, Result: 23, SQL: &proc.Definition{}}
	forward := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98002}, ObjectRevision: 1, ObjectName: "moving_forward", Signature: "98001,23,25", RoutineKind: catalog.FunctionRoutine, Result: 98001, SQL: &proc.Definition{}}
	inverse := *forward
	inverse.ObjectAddress.ObjectID, inverse.ObjectName = 98003, "moving_inverse"
	final := *forward
	final.ObjectAddress.ObjectID, final.ObjectName, final.Result = 98004, "moving_final", 23
	aggregate := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98005}, ObjectRevision: 1, ObjectName: "moving_frame", Signature: "23,25", RoutineKind: catalog.AggregateRoutine, Result: 23, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 98005, Kind: "n", Functions: [8]ids.OID{ordinary.Address().ObjectID, 0, 0, 0, 0, 98002, 98003, 98004}, TransitionType: 23, HasInitial: true, Initial: "0", FinalModify: "r", MovingType: 98001, HasMovingInitial: true, MovingInitial: "000", MovingSpace: -17, MovingFinalExtra: true, MovingFinalModify: "r"}}}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for _, d := range []catalog.Descriptor{ordinary, forward, &inverse, &final, aggregate, &catalog.Type{SQLType: oldDomain}} {
		old := d.Address()
		next := old
		next.ObjectID += 1000
		mapping[old] = next
	}
	remap := func(old *catalog.Procedure) (*catalog.Procedure, error) {
		next := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
		if old.SQL != nil {
			copy := *next
			copy.Signature = catalog.ProcedureSignature(strings.ReplaceAll(string(old.Signature), "98001", "99001"))
			if old.Result == 98001 {
				copy.Result = 99001
			}
			next = &copy
		}
		return next, nil
	}
	for _, p := range []*catalog.Procedure{ordinary, forward, &inverse, &final, aggregate} {
		oldState.SetDescriptor(p.Address(), p)
		next, _ := remap(p)
		newState.SetDescriptor(next.Address(), next)
	}
	inputs := []sqltype.Usage{oldDomain.Usage(), integer.Usage(), oldBase}
	conversions := make([]sqltype.CoercionPlan, len(inputs))
	for i, input := range inputs {
		conversions[i], err = catalog.AggregateConversion(nil, input, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	p := &exec.TransitionAggregate{Aggregate: aggregate, Support: forward, InverseSupport: &inverse, Final: &final, Moving: true, Initial: "000", HasInitial: true, FinalModify: "r", StateUsage: oldDomain.Usage(), Result: integer.Usage(), Inputs: inputs, Formals: append([]sqltype.Usage(nil), inputs...), Conversions: conversions, InverseFormals: append([]sqltype.Usage(nil), inputs...), InverseConversions: append([]sqltype.CoercionPlan(nil), conversions...), FinalInputs: append([]sqltype.Usage(nil), inputs...), FinalFormals: append([]sqltype.Usage(nil), inputs...), FinalConversions: append([]sqltype.CoercionPlan(nil), conversions...)}
	p.InputCollation = oldBase.Collation
	mapper := &ViewQueryRemapper{Catalog: newRead, Procedure: remap, Type: func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == oldDomain {
			return newDomain
		}
		next, _ := newRead.LookupType(old.ID())
		return next
	}, Collation: func(old sqltype.CollationRef) (sqltype.CollationRef, error) {
		if old.ID == 950 {
			return newBase.Collation, nil
		}
		return old, nil
	}, routineFormals: 64}
	for _, kind := range []string{"valid", "wrong_inverse", "wrong_mode", "foreign_inverse_usage", "missing_inverse", "missing_final"} {
		t.Run(kind, func(t *testing.T) {
			provider := *p
			switch kind {
			case "wrong_inverse":
				copy := *p.InverseSupport
				copy.ObjectAddress.ObjectID++
				provider.InverseSupport = &copy
			case "wrong_mode":
				provider.Moving = false
			case "foreign_inverse_usage":
				provider.InverseFormals = append([]sqltype.Usage(nil), p.InverseFormals...)
				provider.InverseFormals[0] = newDomain.Usage()
			case "missing_inverse":
				provider.InverseSupport = nil
			case "missing_final":
				provider.Final = nil
			}
			var slot functions.AggregateProvider = &provider
			err := mapper.provider(&slot, &provider, nil)
			if kind != "valid" {
				if err == nil {
					t.Fatal("malformed moving frame admitted", kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			next := slot.(*exec.TransitionAggregate)
			if next.StateUsage.Type != newDomain || next.Aggregate.Aggregate.Definition.MovingType != 99001 || next.Aggregate.Aggregate.Definition.TransitionType != 23 || next.Result.Type != newInteger || next.InverseSupport.Address().ObjectID != 99003 || next.Final.Address().ObjectID != 99004 || next.InverseSupport.Result != 99001 || next.InverseFormals[0].Type != newDomain || next.FinalInputs[0].Type != newDomain {
				t.Fatal("moving state conflated or mapped more than once", next)
			}
			if next.InputCollation.ID != 951 || next.StateUsage.Collation.ID != 951 || next.InverseFormals[0].Collation.ID != 951 || next.InverseFormals[2].Collation.ID != 951 || next.FinalInputs[2].Collation.ID != 951 || next.Inputs[2].Type != newText {
				t.Fatal("moving inverse/state/extra collation remap lost", next)
			}
			parameter := plan.ParameterExpr{Info: expr.Info{Usage: inputs[0], Property: expr.Properties{ParameterDependent: true}}, Number: 1}
			if mapped, err := mapper.transitionExpression(parameter, p.InverseFormals, next.InverseFormals); err != nil || mapped.ResultUsage() != next.InverseFormals[0] {
				t.Fatal("inverse private parameter mapping", mapped, err)
			}
			parameter.Number = 4
			if _, err := mapper.transitionExpression(parameter, p.InverseFormals, next.InverseFormals); err == nil {
				t.Fatal("inverse escaped closed local frame")
			}
		})
	}
	if p.StateUsage.Type != oldDomain || p.InverseSupport.Result != 98001 || p.Aggregate.Aggregate.Definition.MovingType != 98001 || p.InverseFormals[0].Type != oldDomain {
		t.Fatal("remap mutated source provider")
	}
}
