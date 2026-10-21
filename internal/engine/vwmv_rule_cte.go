package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// The original definition remains the provider key and SQL/output authority.
// Only this execution-private tuple chooses the executable command/context.
type ruleCTECommand struct {
	definition *plan.CTEDefinition
	command    ruleCommand
	caller     ruleAuthority
	events     []catalog.ExpressionDependency
	mutation   int
}

func ruleCTEProgramError(program ruleProgram) error {
	if len(program.commands) == 1 {
		return nil
	}
	if len(program.commands) == 0 {
		return sqlError("0A000", "DO INSTEAD NOTHING rules are not supported for data-modifying statements in WITH")
	}
	for _, command := range program.commands {
		switch command.source {
		case ruleConditionalInstead:
			return sqlError("0A000", "conditional DO INSTEAD rules are not supported for data-modifying statements in WITH")
		case ruleAlso:
			return sqlError("0A000", "DO ALSO rules are not supported for data-modifying statements in WITH")
		}
	}
	return sqlError("0A000", "multi-statement DO INSTEAD rules are not supported for data-modifying statements in WITH")
}
