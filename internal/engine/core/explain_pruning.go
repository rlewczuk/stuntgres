package core

import (
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
)

// sourcePruning captures metadata only. The same bounded proof as execution
// runs unlocked; neither an execution source nor row populations are opened.
func (r *explainRenderer) sourcePruning(tree *explainTree, source *plan.SourceNode, relation *catalog.Relation, read catalog.Read, physicals []*catalog.Relation, inherited bool) error {
	filter := r.scanFilter
	if !inherited && relation.Membership == nil && len(relation.Constraints.Check) == 0 && len(relation.Constraints.NotNull) == 0 {
		return nil
	}
	if filter == nil || filter.Child != source || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation {
		return nil
	}
	if filter.Predicate.Properties().ParameterDependent {
		tree.details = append(tree.details, "Pruning: unknown parameter values")
		r.property(tree, "Pruning", "unknown parameter values")
		return nil
	}
	if source.Occurrence < 0 || source.Occurrence >= len(r.leaves) {
		return nil
	}
	leaf := r.leaves[source.Occurrence]
	if leaf.Ref != source.Ref || leaf.Mark.Mode != 0 {
		return nil
	}
	principal := r.auth.State.CurrentUser
	if leaf.Principal != (authorization.PrincipalRef{}) {
		principal = leaf.Principal
	}
	security, err := access.SelectRowSecurity(r.auth.Roles, relation, principal, catalog.PolicySelect, false, false)
	if err != nil {
		return err
	}
	if security.Count != 0 {
		return nil
	}
	frame := uint32(1)
	for f, node := range r.frames {
		if node == filter {
			frame = f
			break
		}
	}
	evaluation := exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: frame}, EvalStatement: exec.EvalStatement{Context: r.scope.executionContext(), Input: r.scope.Input}}
	mode := r.scope.Op.ConstraintExclusion
	e := r.scope.Engine()
	e.Mu.Unlock()
	proof, err := partition.NewBranchProof(r.scope.executionContext(), &evaluation, read, relation, source.Ref, filter.Predicate, mode, inherited)
	var excluded []*catalog.Relation
	if err == nil {
		for _, physical := range physicals {
			var possible bool
			possible, err = proof.Possible(physical, !r.scope.PlannerMethods.NoPartitionPruning)
			if err != nil {
				break
			}
			if !possible {
				excluded = append(excluded, physical)
			}
		}
	}
	e.Mu.Lock()
	if err != nil {
		return err
	}
	if len(excluded) > 0 {
		names := make([]string, len(excluded))
		var identities []map[string]any
		for i, physical := range excluded {
			names[i] = r.objectName(physical.Namespace(), physical.Name())
			if r.options.format == "json" {
				identity := &explainTree{}
				r.relationProperties(identity, physical, physical.Name())
				identities = append(identities, identity.properties)
			}
		}
		tree.details = append(tree.details, "Pruned Sources: "+strings.Join(names, ", "))
		r.property(tree, "Pruned Sources", identities)
	}
	return nil
}
