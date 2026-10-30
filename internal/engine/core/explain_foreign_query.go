package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// foreignQuery captures occurrence authority under Mu, then shares execution's
// translator and path selection without constructing live sources or owners.
func (r *explainRenderer) foreignQuery(root plan.Node) (*explainTree, bool, error) {
	switch root.(type) {
	case *plan.JoinNode, *plan.AggregateNode, *plan.SortNode, *plan.LimitNode, *plan.EvaluationNode:
	default:
		return nil, false, nil
	}
	if r.rowMarked || r.inheritedMark.Mode != 0 || r.foreign.outer || r.foreign.invocation || r.foreign.identityReads { return nil, false, nil }
	if err := CheckRestoreEffect(r.scope.Op); err != nil { return nil, false, err }
	occurrences := make(map[int]exec.ForeignPlanningOccurrence)
	input := exec.ForeignPlanningInput{Frame: r.frame, Input: r.scope.Input, Output: r.scope.Output, Parameters: r.typedParameters, ExportValue: r.exportValue, Occurrence: func(i int) exec.ForeignPlanningOccurrence { return occurrences[i] }}
	var capture func(plan.Node) error
	capture = func(node plan.Node) error {
		if node == nil { return nil }
		if source, ok := node.(*plan.SourceNode); ok {
			relation, exists := r.auth.Catalog.Relation(source.Ref.Address)
			if !exists { return ids.ErrCatalogReplaced }
			if relation.Foreign == nil { return nil }
			principal := r.auth.State.CurrentUser
			var uses []int
			if source.Occurrence < 0 || source.Occurrence >= len(r.leaves) { return nil }
			leaf := r.leaves[source.Occurrence]
			if leaf.Ref.Address != source.Ref.Address { return nil }
			uses = leaf.Uses
			if uses == nil { uses = []int{} }
			if leaf.Principal != (authorization.PrincipalRef{}) { principal = leaf.Principal }
			condition, err := r.rowSecurity(relation, catalog.PolicySelect, false, false, principal)
			if err != nil { return err }
			if condition != nil { return nil }
			handler, request, err := fdw.ScanRequest(r.auth.View, principal, relation, uses, r.auth.RestrictedRelations())
			if err != nil { return err }
			occurrences[source.Occurrence] = exec.ForeignPlanningOccurrence{Source: &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Request: request, Columns: source.Schema()}, Owner: r, Eligible: true}
			return nil
		}
		if _, derived := node.(*plan.DerivedNode); derived { return nil }
		left, right := plan.QueryInputs(node)
		if err := capture(left); err != nil { return err }
		return capture(right)
	}
	if err := capture(root); err != nil { return nil, false, err }
	captured := r.auth.Catalog.State()
	e := r.scope.Engine()
	e.Mu.Unlock()
	r.scope.foreignUnlocked()
	selected, ok, err := exec.PlanForeignQuery(r.scope.executionContext(), input, root)
	var properties []catalog.ForeignExplainProperty
	if err == nil && ok {
		if provider, supported := selected.Source.Wrapper.(catalog.ForeignExplainScanProvider); supported {
			path := selected.Path
			path.Plan.Slots = slices.Clone(path.Plan.Slots)
			properties, err = provider.ExplainForeignScan(r.scope.executionContext(), catalog.ForeignExplainScanRequest{ForeignRequest: exec.CopyForeignRequest(selected.Source.Request), Query: &catalog.ForeignExplainQueryPlan{Request: exec.CopyForeignQueryRequest(selected.Request), Path: path}, Options: r.foreignOptions()})
			err = exec.ForeignCallbackError(err)
		}
	}
	properties = slices.Clone(properties)
	e.Mu.Lock()
	if err != nil { return nil, false, err }
	if err := r.foreignFence(captured); err != nil { return nil, false, err }
	if !ok { return nil, false, nil }
	tree := &explainTree{label: "Foreign Scan", unknownEstimate: true}
	tree.details = append(tree.details, "Foreign operation: "+selected.Request.Stage.Kind)
	for _, source := range selected.Request.Sources {
		tree.details = append(tree.details, fmt.Sprintf("Foreign source %d: %s.%s", source.Occurrence+1, catalog.Identifier(source.Request.Schema), catalog.Identifier(source.Request.Table)))
	}
	if err := foreignProperties(tree, properties); err != nil { return nil, false, err }
	if r.options.costs {
		tree.details = append(tree.details, fmt.Sprintf("Provider estimates: startup cost=%.2f total cost=%.2f", selected.Path.StartupCost, selected.Path.TotalCost))
	}
	return tree, true, nil
}
