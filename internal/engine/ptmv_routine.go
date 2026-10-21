package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Each demanded key has its own original manifest. The statement owns cooked
// expressions, never row values or permission results; routinePrograms owns the
// consumed graph for the existing wait/publication readmission fences.
type partitionRoutineIdentity struct {
	owner *catalog.Relation
	phase ids.CommandIdentity
	actor authorization.PrincipalRef
	ri    bool
}

// partitionRecipeHost is the statement state a partition key recipe reads
// through its evaluation's live host. The root implements it on statementHost;
// without a statement host the recipe runs uncached under the evaluation's own
// routine program.
type partitionRecipeHost interface {
	// CommandIdentity is the statement's transaction and command identity.
	CommandIdentity() ids.CommandIdentity
	// PreparedDomainChecks are the statement's prepared domain check programs.
	PreparedDomainChecks() map[*catalog.Type]exec.DomainCheckProgram
	// ReferentialActor is the current user of the referential action
	// authority; false outside a referential action.
	ReferentialActor() (authorization.PrincipalRef, bool)
	// PartitionPrograms returns the consumed recipe programs of identity, or nil.
	PartitionPrograms(identity partitionRoutineIdentity) []expr.Expr
	// RecordPartitionPrograms caches the programs of identity and records their
	// routine admission among the statement's consumed routine programs.
	RecordPartitionPrograms(identity partitionRoutineIdentity, expressions []expr.Expr, admission exec.RoutineProgram)
}

func evaluatePartitionRecipe(evaluation *exec.Context, owner *catalog.Relation, keyIndex int, row []sqltype.Value) (sqltype.Value, error) {
	key := owner.Partition.Keys[keyIndex].Derived
	local := index.RecipeEvaluation(evaluation, key.Dependencies, owner)
	local.Row, local.Scope, local.Frame = row, 1, 1
	host, _ := local.LiveHost().(partitionRecipeHost)
	if host != nil {
		local.DomainPhase = host.CommandIdentity()
		local.PreparedDomainChecks = host.PreparedDomainChecks()
	}
	auth, _, err := exec.RoutineAdmissionAuthority(&local)
	if err != nil {
		return sqltype.Value{}, err
	}
	identity := partitionRoutineIdentity{owner: owner, phase: local.DomainPhase, actor: auth.State.CurrentUser}
	if host != nil {
		actor, ok := host.ReferentialActor()
		identity.ri = ok && actor == auth.State.CurrentUser
	}
	var expressions []expr.Expr
	if host != nil {
		expressions = host.PartitionPrograms(identity)
	}
	if expressions == nil {
		var admission exec.RoutineProgram
		expressions = make([]expr.Expr, len(owner.Partition.Keys))
		for i, entry := range owner.Partition.Keys {
			if entry.Derived == nil {
				continue
			}
			expressions[i], err = index.PrepareIndexRecipe(&local, entry.Derived.Definition, entry.Derived.Dependencies, owner, &admission, true)
			evaluation.PreparedDomainChecks = local.PreparedDomainChecks
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if host != nil {
			host.RecordPartitionPrograms(identity, expressions, admission)
		} else if program := local.RoutineProgram; program != nil {
			program.Include(&admission)
		}
	}
	// Keep this key's manifest selected during lazy SQL-body evaluation too.
	value, err := expressions[keyIndex].Eval(&local)
	evaluation.PreparedDomainChecks = local.PreparedDomainChecks
	return value, err
}

// partitionScope is the statement's partition slot: consumed partition routine
// programs and the latest-DDL validation scan view (never the retained query
// snapshot).
type partitionScope struct {
	programs       map[partitionRoutineIdentity][]expr.Expr
	validationView *visibility.View // Latest DDL scan, same owner; never the retained query snapshot.
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
