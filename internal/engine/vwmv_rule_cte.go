package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

// The original definition remains the provider key and SQL/output authority.
// Only this execution-private tuple chooses the executable command/context.
type ruleCTECommand struct {
	Definition *plan.CTEDefinition
	Command    ruleCommand
	Caller     ruleAuthority
	Events     []catalog.ExpressionDependency
	Mutation   int
}

func RuleCTEProgramError(program ruleProgram) error {
	if len(program.Commands) == 1 {
		return nil
	}
	if len(program.Commands) == 0 {
		return sqlError("0A000", "DO INSTEAD NOTHING rules are not supported for data-modifying statements in WITH")
	}
	for _, command := range program.Commands {
		switch command.Source {
		case RuleConditionalInstead:
			return sqlError("0A000", "conditional DO INSTEAD rules are not supported for data-modifying statements in WITH")
		case RuleAlso:
			return sqlError("0A000", "DO ALSO rules are not supported for data-modifying statements in WITH")
		}
	}
	return sqlError("0A000", "multi-statement DO INSTEAD rules are not supported for data-modifying statements in WITH")
}
