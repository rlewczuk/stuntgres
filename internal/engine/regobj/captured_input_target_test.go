package regobj

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// A literal retargets the captured resolver in its input environment through
// the analyze interfaces. Each resolver takes only the parts it records, keeps
// a sink the target leaves nil, and leaves the receiver unchanged.
func TestCapturedInputsRetargetOnlyTheirSinks(t *testing.T) {
	oldPos, newPos := parser.Position{Offset: 1, Character: 2}, parser.Position{Offset: 7, Character: 8}
	var oldQualified, newQualified []ids.ObjectAddress
	var oldDeps, newDeps []catalog.ExpressionDependency
	full := analyze.IdentifierTarget{Position: newPos, Qualified: &newQualified, Operands: &newDeps, Resolved: &newDeps}
	types := TypeInput{position: oldPos, qualified: &oldQualified, resolved: &oldDeps}

	t.Run("regtype", func(t *testing.T) {
		got := analyze.RegtypeRetarget(types).RetargetRegtype(full).(TypeInput)
		if got.position != newPos || got.qualified != &newQualified || got.resolved != &oldDeps {
			t.Fatalf("retargeted regtype = %+v", got)
		}
		kept := types.RetargetRegtype(analyze.IdentifierTarget{Position: newPos}).(TypeInput)
		if kept.qualified != &oldQualified {
			t.Fatal("nil qualified sink replaced the captured one")
		}
		if types.position != oldPos || types.qualified != &oldQualified {
			t.Fatal("retargeting changed the receiver")
		}
	})
	t.Run("operator", func(t *testing.T) {
		input := OperatorInput{types: types, qualified: &oldQualified, operands: &oldDeps}
		got := analyze.OperatorRetarget(input).RetargetOperator(full).(OperatorInput)
		if got.types.position != newPos || got.types.qualified != &oldQualified || got.qualified != &newQualified || got.operands != &newDeps {
			t.Fatalf("retargeted operator = %+v", got)
		}
		kept := input.RetargetOperator(analyze.IdentifierTarget{Position: newPos}).(OperatorInput)
		if kept.qualified != &oldQualified || kept.operands != &oldDeps {
			t.Fatal("nil operator sinks replaced the captured ones")
		}
	})
	t.Run("routine", func(t *testing.T) {
		input := RoutineInput{types: types, operands: &oldDeps}
		got := analyze.RoutineRetarget(input).RetargetRoutine(full).(RoutineInput)
		if got.types.position != newPos || got.types.qualified != &newQualified || got.types.resolved != &oldDeps || got.operands != &newDeps {
			t.Fatalf("retargeted routine = %+v", got)
		}
		kept := input.RetargetRoutine(analyze.IdentifierTarget{Position: newPos, Qualified: &newQualified}).(RoutineInput)
		if kept.operands != &oldDeps {
			t.Fatal("nil operand sink replaced the captured one")
		}
	})
	t.Run("regts", func(t *testing.T) {
		input := TextSearchInput{qualified: &oldQualified, resolved: &oldDeps}
		got := analyze.RegTSRetarget(input).RetargetRegTS(full).(TextSearchInput)
		if got.qualified != &newQualified || got.resolved != &newDeps {
			t.Fatalf("retargeted text search = %+v", got)
		}
		kept := input.RetargetRegTS(analyze.IdentifierTarget{Position: newPos}).(TextSearchInput)
		if kept.qualified != &oldQualified || kept.resolved != &oldDeps {
			t.Fatal("nil text-search sinks replaced the captured ones")
		}
	})
}

// A parameter or literal copy of a routine resolver carries its own portal
// context or literal position and leaves the original unchanged.
func TestRoutineInputParameterAndLiteralCopies(t *testing.T) {
	var input RoutineInput
	parameter := input.RoutineParameter(2, false).(RoutineInput)
	if parameter.types.portal != "unnamed portal parameter $2 = '...'" || input.types.portal != "" {
		t.Fatal("parameter authority/redaction mutated original input")
	}
	runtime := input.RoutineLiteral(17).(RoutineInput)
	if runtime.types.position.Character != 17 || input.types.position.Character != 0 || runtime.types.portal != "" {
		t.Fatal("literal authority retained portal or mutated original")
	}
}
