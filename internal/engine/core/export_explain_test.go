package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

// ExplainActiveForTest checks activation-local execution lifetime, independently
// of detached output equality. It grants no access to graphs or authority.
func ExplainActiveForTest(scope *StatementScope) bool {
	return scope.explain != nil
}

// Borrow only during the executing provider callback; tests retain these
// invocation-local identities until the synchronous Execute has settled.
func ExplainObservationForTest(scope *StatementScope) (*exec.Observation, []plan.Node) {
	if scope.explain == nil || len(scope.explain.renderers) == 0 { return nil, nil }
	renderer := scope.explain.renderers[len(scope.explain.renderers)-1]
	var nodes []plan.Node
	var visit func(*explainTree)
	visit = func(tree *explainTree) {
		if tree.node != nil { nodes = append(nodes, tree.node) }
		for _, child := range tree.children { visit(child) }
	}
	for _, tree := range renderer.trees { visit(tree) }
	return renderer.observation, nodes
}
