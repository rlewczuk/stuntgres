package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Each demanded key has its own original manifest. The statement owns cooked
// expressions, never row values or permission results; routinePrograms owns the
// consumed graph for the existing wait/publication readmission fences.
type partitionRoutineIdentity struct {
	owner *catalog.Relation
	phase domainCheckIdentity
	actor authorization.PrincipalRef
	ri    bool
}

func evaluatePartitionRecipe(evaluation *EvalContext, owner *catalog.Relation, keyIndex int, row []sqltype.Value) (sqltype.Value, error) {
	key := owner.Partition.Keys[keyIndex].Derived
	local := indexRecipeEvaluation(evaluation, key.Dependencies, owner)
	local.Row, local.Scope, local.Frame = row, 1, 1
	scope := local.liveScope()
	if scope != nil {
		local.domainPhase = domainCheckIdentity{transaction: scope.txRef, command: scope.command}
		local.preparedDomainChecks = scope.domainPreparedChecks
	}
	auth, _, err := local.routineAdmissionAuthority()
	if err != nil {
		return sqltype.Value{}, err
	}
	identity := partitionRoutineIdentity{owner: owner, phase: local.domainPhase, actor: auth.state.CurrentUser}
	if scope != nil {
		identity.ri = scope.foreignActionAuth != nil && scope.foreignActionAuth.state.CurrentUser == auth.state.CurrentUser
	}
	var expressions []expr.Expr
	if scope != nil {
		expressions = scope.partitionPrograms[identity]
	}
	if expressions == nil {
		var admission routineAdmissionProgram
		expressions = make([]expr.Expr, len(owner.Partition.Keys))
		for i, entry := range owner.Partition.Keys {
			if entry.Derived == nil {
				continue
			}
			expressions[i], err = prepareIndexRecipe(&local, entry.Derived.Definition, entry.Derived.Dependencies, owner, &admission, true)
			evaluation.preparedDomainChecks = local.preparedDomainChecks
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if scope != nil {
			if scope.partitionPrograms == nil {
				scope.partitionPrograms = make(map[partitionRoutineIdentity][]expr.Expr)
			}
			scope.partitionPrograms[identity] = expressions
			scope.routinePrograms = append(scope.routinePrograms, commandRoutineProgram{admission: admission})
		} else if local.routineProgram != nil {
			local.routineProgram.include(&admission)
		}
	}
	// Keep this key's manifest selected during lazy SQL-body evaluation too.
	value, err := expressions[keyIndex].Eval(&local)
	evaluation.preparedDomainChecks = local.preparedDomainChecks
	return value, err
}
