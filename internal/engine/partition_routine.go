package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Each demanded key has its own original manifest. The statement owns cooked
// expressions, never row values or permission results; routinePrograms owns the
// consumed graph for the existing wait/publication readmission fences.
type partitionRoutineIdentity struct {
	owner *catalog.Relation
	phase typedef.DomainCheckIdentity
	actor authorization.PrincipalRef
	ri    bool
}

func evaluatePartitionRecipe(evaluation *EvalContext, owner *catalog.Relation, keyIndex int, row []sqltype.Value) (sqltype.Value, error) {
	key := owner.Partition.Keys[keyIndex].Derived
	local := indexRecipeEvaluation(evaluation, key.Dependencies, owner)
	local.Row, local.Scope, local.Frame = row, 1, 1
	scope := local.liveScope()
	if scope != nil {
		local.domainPhase = typedef.DomainCheckIdentity{Transaction: scope.txRef, Command: scope.command}
		local.preparedDomainChecks = scope.domainPreparedChecks
	}
	auth, _, err := local.routineAdmissionAuthority()
	if err != nil {
		return sqltype.Value{}, err
	}
	identity := partitionRoutineIdentity{owner: owner, phase: local.domainPhase, actor: auth.State.CurrentUser}
	if scope != nil {
		identity.ri = scope.foreignActionAuth != nil && scope.foreignActionAuth.State.CurrentUser == auth.State.CurrentUser
	}
	var expressions []expr.Expr
	if scope != nil {
		expressions = scope.partitionsOrZero().programs[identity]
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
			if scope.partitionsOrZero().programs == nil {
				scope.partitions().programs = make(map[partitionRoutineIdentity][]expr.Expr)
			}
			scope.partitions().programs[identity] = expressions
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

// partitionScope is the statement's partition slot: consumed partition routine
// programs and the latest-DDL validation scan view (never the retained query
// snapshot).
type partitionScope struct {
	programs       map[partitionRoutineIdentity][]expr.Expr
	validationView *visibility.View // Latest DDL scan, same owner; never the retained query snapshot.
}

// partitions returns the statement's partitions slot, allocating it on first use.
func (s *StatementScope) partitions() *partitionScope {
	if s.partitionsSlot == nil {
		s.partitionsSlot = new(partitionScope)
	}
	return s.partitionsSlot
}

// partitionsOrZero reads the partitions slot; a nil slot reads as zero.
func (s *StatementScope) partitionsOrZero() partitionScope {
	if s.partitionsSlot == nil {
		return partitionScope{}
	}
	return *s.partitionsSlot
}

// dropPrograms drops the consumed partition routine programs together with
// the borrowed admission context they belong to.
func (p *partitionScope) dropPrograms() {
	if p != nil {
		p.programs = nil
	}
}

// releaseValidationView releases the latest-DDL scan view, if any.
func (p *partitionScope) releaseValidationView() {
	if p != nil && p.validationView != nil {
		p.validationView.Release()
		p.validationView = nil
	}
}

// setPartitionValidationView installs a saved scan view; restoring none
// never allocates the slot.
func (s *StatementScope) setPartitionValidationView(view *visibility.View) {
	if view != nil || s.partitionsSlot != nil {
		s.partitions().validationView = view
	}
}
