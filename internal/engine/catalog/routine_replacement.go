package catalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
)

// CheckRoutineReplacementKind compares declarations only. The caller admits
// ownership first and supplies any script Context; this leaf retains no authority.
func CheckRoutineReplacementKind(previous *Procedure, kind RoutineKind) *sqlstate.Error {
	if previous.RoutineKind == kind {
		return nil
	}
	noun := "function"
	if previous.RoutineKind == ProcedureRoutine {
		noun = "procedure"
	} else if previous.RoutineKind == AggregateRoutine {
		noun = "aggregate function"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot change routine kind", Detail: fmt.Sprintf("%q is a %s.", previous.Name(), noun)}
}

// CheckRoutineReplacementResult checks result identity, setness and declared
// RECORD tuple compatibility. A nil next layout denotes a native declaration
// without OUT fields, not an unchecked or implicitly matching result tuple.
// Callers retain eligibility, input/default checks, Hint formatting and publication.
func CheckRoutineReplacementResult(previous *Procedure, result ids.OID, returnsSet bool, next *proc.Definition) *sqlstate.Error {
	if previous.Result != result || previous.ReturnsSet() != returnsSet {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "cannot change return type of existing function"}
	}
	mismatch := false
	if old := previous.RoutineDefinition(); result == 2249 && old != nil && old.DerivedResult() {
		mismatch = next == nil || !next.MatchesOutputTuple(old.Result.Type)
	} else if result == 2249 && next == nil {
		// A fixed native multi-OUT tuple cannot become an anonymous runtime
		// record either. No tuple allocation is needed to establish absence.
		outputs := previous.OutputDeclaration()
		mismatch = outputs != nil && outputs.Len() > 1
	}
	if mismatch {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "cannot change return type of existing function", Detail: "Row type defined by OUT parameters is different."}
	}
	return nil
}
