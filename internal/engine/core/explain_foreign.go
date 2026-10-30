package core

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func (r *explainRenderer) foreignOptions() catalog.ForeignExplainOptions {
	return catalog.ForeignExplainOptions{Verbose: r.options.verbose, Costs: r.options.costs}
}

func foreignProperties(tree *explainTree, properties []catalog.ForeignExplainProperty) error {
	for _, property := range properties {
		if strings.TrimSpace(property.Name) == "" || strings.ContainsAny(property.Name, "\r\n:") || strings.ContainsAny(property.Value, "\r\n") {
			return fmt.Errorf("stuntgres: invalid foreign EXPLAIN property")
		}
		tree.details = append(tree.details, property.Name+": "+property.Value)
	}
	return nil
}

// Entered with Mu; callbacks run with neither engine nor replacement locks.
// Every occurrence captures its own principal and actual provider plan.
func (r *explainRenderer) foreignScan(tree *explainTree, node *plan.SourceNode, relation *catalog.Relation) (float64, error) {
	if err := CheckRestoreEffect(r.scope.Op); err != nil {
		return 0, err
	}
	principal := r.auth.State.CurrentUser
	mark := r.inheritedMark
	var uses []int
	if node.Occurrence >= 0 && node.Occurrence < len(r.leaves) {
		leaf := r.leaves[node.Occurrence]
		if leaf.Ref.Address == node.Ref.Address {
			mark = exec.MergeQueryRowMark(mark, leaf.Mark)
			uses = leaf.Uses
			if uses == nil { uses = []int{} }
		}
		if leaf.Ref.Address == node.Ref.Address && leaf.Principal != (authorization.PrincipalRef{}) {
			principal = leaf.Principal
		}
	}
	if node.Occurrence < 0 && node.Ref.Address == r.policyTarget && r.policyPrincipal != (authorization.PrincipalRef{}) {
		principal = r.policyPrincipal
	}
	mark = exec.EffectiveQueryRowMark(relation, mark)
	handler, request, err := fdw.ScanRequest(r.auth.View, principal, relation, uses, r.auth.RestrictedRelations())
	if err != nil {
		return 0, err
	}
	request.Parameters = r.parameters
	if node.Occurrence < 0 && node.Ref.Address == r.policyTarget {
		request.Identity = r.modifyIdentity
	}
	source := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Request: request}
	captured := r.auth.Catalog.State()
	e := r.scope.Engine()
	e.Mu.Unlock()
	r.scope.foreignUnlocked()
	var execution exec.ForeignExecution
	if r.rowMarked {
		request, err = exec.PlanForeignRowMark(r.scope.executionContext(), handler.Wrapper, request, mark)
	}
	if err == nil {
		err = source.Plan(r.scope.executionContext(), request, &execution)
	}
	var properties []catalog.ForeignExplainProperty
	if err == nil {
		if provider, ok := handler.Wrapper.(catalog.ForeignExplainScanProvider); ok {
			path, planned := execution.Path, execution.Plan
			path.Attributes = slices.Clone(path.Attributes)
			planned.Attributes = slices.Clone(planned.Attributes)
			properties, err = provider.ExplainForeignScan(r.scope.executionContext(), catalog.ForeignExplainScanRequest{ForeignRequest: exec.CopyForeignRequest(request), Size: execution.Size, Path: path, Plan: planned, Options: r.foreignOptions()})
			err = exec.ForeignCallbackError(err)
		}
	}
	// Copy properties before returning to mutable engine ownership.
	properties = slices.Clone(properties)
	e.Mu.Lock()
	if err != nil {
		return 0, err
	}
	if err = r.foreignFence(captured); err != nil {
		return 0, err
	}
	if err = foreignProperties(tree, properties); err != nil {
		return 0, err
	}
	if r.options.costs {
		tree.details = append(tree.details, fmt.Sprintf("Provider estimates: startup cost=%.2f total cost=%.2f rows=%.0f width=%.0f", execution.Path.StartupCost, execution.Path.TotalCost, execution.Size.Rows, execution.Size.Width))
	}
	return execution.Size.Rows, nil
}

func (r *explainRenderer) foreignFence(captured *catalog.State) error {
	if err := r.scope.executionContext().Err(); err != nil {
		return err
	}
	current, err := r.scope.Engine().AuthorizationLocked(r.scope.Op)
	if err != nil {
		return err
	}
	if current.Catalog.State() != captured || current.Roles != r.auth.Roles {
		return ids.ErrCatalogReplaced
	}
	return nil
}

func (r *explainRenderer) foreignModify(tree *explainTree, relation *catalog.Relation, operation catalog.ForeignModifyOperation) error {
	if err := CheckRestoreEffect(r.scope.Op); err != nil {
		return err
	}
	handler, request, err := fdw.ScanRequest(r.auth.View, r.policyPrincipal, relation, nil, r.auth.RestrictedRelations())
	if err != nil {
		return err
	}
	request.Parameters = r.parameters
	source := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Request: request}
	captured := r.auth.Catalog.State()
	e := r.scope.Engine()
	e.Mu.Unlock()
	r.scope.foreignUnlocked()
	modification, err := fdw.PlanModification(r.scope.executionContext(), source, operation, false)
	var properties []catalog.ForeignExplainProperty
	if err == nil {
		if provider, ok := handler.Wrapper.(catalog.ForeignExplainModifyProvider); ok {
			properties, err = provider.ExplainForeignModify(r.scope.executionContext(), catalog.ForeignExplainModifyRequest{ForeignModifyRequest: modification.CallbackRequest(), Options: r.foreignOptions()})
			err = exec.ForeignCallbackError(err)
		}
	}
	properties = slices.Clone(properties)
	e.Mu.Lock()
	if err != nil {
		return err
	}
	if err = r.foreignFence(captured); err != nil {
		return err
	}
	targets := modification.Request.Targets
	targets.Names = slices.Clone(targets.Names)
	r.modifyIdentity = &targets
	return foreignProperties(tree, properties)
}
