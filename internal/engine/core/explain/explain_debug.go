package explain

import (
	"encoding/json"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// Capture while this actual node's lexical frames are active. Only detached
// scalars/SQL strings enter debug; no evaluation or planner-node imitation.
func (r *Renderer) captureDebug(tree *Tree, root plan.Node) error {
	d := make(map[string]any)
	tree.node = root
	var context plan.Node
	var targets []expr.Expr
	ownsTargets := false
	switch n := root.(type) {
	case *plan.SourceNode:
		if _, ok := r.auth.Catalog.Relation(n.Ref.Address); !ok {
			return ids.ErrCatalogReplaced
		}
		d["Occurrence"], d["Relation OID"] = n.Occurrence, uint32(n.Ref.Address.ObjectID)
	case *plan.ProjectNode:
		d["Frame"] = n.Frame
		context, targets, ownsTargets = n.Child, n.Expressions, true
	case *plan.SortNode:
		context, targets, ownsTargets = n.Child, n.Outputs, true
		keys := make([]map[string]any, len(n.Keys))
		for i, key := range n.Keys {
			text, err := r.expression(n.Child, key.Expression)
			if err != nil {
				return err
			}
			keys[i] = map[string]any{"Target Identity": key.TargetIdentity, "Output Ordinal": key.OutputOrdinal, "Slot": key.Slot, "Cache": key.Cache, "Output": key.Output, "Expression": text}
		}
		d["Sort Targets"] = keys
	case *plan.DerivedNode:
		d["Frame"] = n.ScanFrame
	case *plan.WorktableScanNode:
		d["Frame"] = n.ScanFrame
	case *plan.JoinNode:
		d["Frame"] = n.Frame
	case *plan.FunctionFromNode:
		d["Frame"] = n.Frame
	case *plan.SetProjectNode:
		d["Frame"] = n.Frame
	}
	if ownsTargets {
		parts, err := r.expressionParts(context, targets)
		if err != nil {
			return err
		}
		d["Target List"] = parts // A real zero-width owner has [], not an absent field.
	}
	tree.debug = d
	return nil
}

// Number actual identities in the final presentation walk, not construction's
// postorder. Repeated uses share IDs; synthetic wrappers have no node identity.
func numberDebug(trees []*Tree) {
	ids := make(map[plan.Node]int)
	var visit func(*Tree)
	visit = func(tree *Tree) {
		if tree.debug != nil && tree.node != nil {
			id := ids[tree.node]
			if id == 0 {
				id = len(ids) + 1
				ids[tree.node] = id
			}
			tree.debug["Node ID"] = id
		}
		for _, child := range tree.children {
			visit(child)
		}
	}
	for _, tree := range trees {
		visit(tree)
	}
}

func (r *Renderer) debugLines(tree *Tree, indent string, lines *[]string) {
	if tree.debug != nil {
		*lines = append(*lines, indent+"Embedded Debug:")
		for _, key := range []string{"Node ID", "Frame", "Occurrence", "Relation OID"} {
			if value, exists := tree.debug[key]; exists {
				*lines = append(*lines, fmt.Sprintf("%s  %s: %v", indent, key, value))
			}
		}
		for _, key := range []string{"Target List", "Sort Targets"} {
			if value, exists := tree.debug[key]; exists {
				// Finite captured data; JSON escaping keeps one TEXT row even
				// for SQL expression strings containing literal newlines.
				text, _ := json.Marshal(value)
				*lines = append(*lines, indent+"  "+key+": "+string(text))
			}
		}
	}
	if tree.commandDebug != nil {
		*lines = append(*lines, indent+"Embedded Command Debug:", fmt.Sprintf("%s  Relation OID: %v", indent, tree.commandDebug["Relation OID"]))
		if targets, exists := tree.commandDebug["Returning Target List"]; exists {
			text, _ := json.Marshal(targets)
			*lines = append(*lines, indent+"  Returning Target List: "+string(text))
		}
	}
}
