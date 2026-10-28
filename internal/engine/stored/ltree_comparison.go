package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The procedure result is the operation's result, not the containing node's:
// NULLIF returns its ltree operand while its selected equality returns BOOL.
type ltreeComparisonCapture struct {
	ref         ids.ObjectRef
	left, right *sqltype.Descriptor
	name        string
	result      sqltype.ID
}

func ltreeComparisonCaptureOf(node expr.Expr) (ltreeComparisonCapture, bool) {
	capture := ltreeComparisonCapture{result: sqltype.Bool}
	operator := "="
	switch n := node.(type) {
	case plan.ComparisonExpr:
		capture.ref, operator = n.RoutineRef, n.Operator
		capture.left, capture.right = n.Left.ResultUsage().Type, n.Right.ResultUsage().Type
	case plan.DistinctExpr:
		capture.ref = n.RoutineRef
		capture.left, capture.right = n.Left.ResultUsage().Type, n.Right.ResultUsage().Type
	case plan.ScalarArrayComparisonExpr:
		capture.ref, operator = n.RoutineRef, n.Operator
		capture.left, capture.right = n.Left.ResultUsage().Type, n.ElementType()
	case plan.ConditionalExpr:
		capture.ref = n.RoutineRef
		if n.Kind == parser.NullIf && len(n.Args) == 2 {
			capture.left, capture.right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
		}
	default:
		return capture, false
	}
	{
		switch operator {
		case "=":
			capture.name = "ltree_eq"
		case "<>", "!=":
			capture.name = "ltree_ne"
		case "<":
			capture.name = "ltree_lt"
		case "<=":
			capture.name = "ltree_le"
		case ">":
			capture.name = "ltree_gt"
		case ">=":
			capture.name = "ltree_ge"
		}
	}
	return capture, capture.left.IsLtree() || capture.right.IsLtree()
}

// The reference is already mapped. The scoped callback supplies the mapped
// ORIGINAL; current descriptors are used only to validate destination authority.
func remapLtreeComparisonRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, capture ltreeComparisonCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	owner := view.Owner()
	if owner == nil || capture.ref.Domain() != owner.Domain || capture.ref.Epoch != view.Epoch() || capture.ref.Address.ClassID != ids.ProcedureClassID || capture.ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid captured ltree comparison reference")
	}
	if !capture.left.IsLtree() || capture.left != capture.right {
		return fmt.Errorf("stuntgres: invalid captured ltree comparison operands")
	}
	if captured == nil || original == nil || captured.Revision() == 0 || captured.Revision() != original.Revision() || captured.Implementation != extension.StockLtree || captured.Signature != ltreeCaptureSignature(capture) || captured.Result != ids.OID(capture.result) || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Builtin != nil {
		return fmt.Errorf("stuntgres: invalid captured ltree comparison declaration")
	}
	sameTypeGeneration := capture.left == typ(capture.left)
	capture.left, capture.right = typ(capture.left), typ(capture.right)
	descriptor, _ := catalog.DescriptorAt(view, reader, capture.ref.Address)
	declaration, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(declaration, original) {
		return fmt.Errorf("stuntgres: missing remapped ltree comparison declaration")
	}
	// An unchanged type identity keeps this capture in its original generation.
	// Historical presentation revisions remain valid, but a declaration cannot
	// have been captured from a revision newer than that generation's current root.
	// Cross-generation transport preserves the source revision independently.
	if original == captured && sameTypeGeneration && original.Revision() > declaration.Revision() {
		return fmt.Errorf("stuntgres: future ltree comparison declaration")
	}
	_, err := currentLtreeComparisonProcedure(view, original, capture)
	return err
}

func currentLtreeComparisonProcedure(view catalog.Read, original *catalog.Procedure, capture ltreeComparisonCapture) (*catalog.Procedure, error) {
	if !capture.left.IsLtree() || capture.left != capture.right {
		return nil, fmt.Errorf("stuntgres: invalid mapped ltree comparison operands")
	}
	current, symbol, err := currentInstalledProcedure(view, capture.ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockLtree || symbol != capture.name || current.Signature != ltreeCaptureSignature(capture) || current.Result != ids.OID(capture.result) || current.RoutineKind != catalog.FunctionRoutine || !current.Strict || current.Volatility != expr.Immutable || current.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: invalid mapped ltree comparison declaration")
	}
	installation, exists := view.Extension(current.Extension)
	if !exists || len(installation.Members) != extension.LtreeMemberCount {
		return nil, fmt.Errorf("stuntgres: missing mapped ltree comparison installation")
	}
	base, exists := view.Type(installation.Members[3])
	if !exists || base.SQLType != capture.left || base.Extension != installation.Address() {
		return nil, fmt.Errorf("stuntgres: foreign mapped ltree comparison type")
	}
	member := -1
	for i, name := range extension.LtreeComparisonNames {
		if name == capture.name {
			member = extension.LtreeComparisonProcedureStart + i
			break
		}
	}
	if member < 0 || installation.Members[member] != current.Address() {
		return nil, fmt.Errorf("stuntgres: invalid mapped ltree comparison member")
	}
	if err := extension.StockLtree.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func ltreeCaptureSignature(c ltreeComparisonCapture) catalog.ProcedureSignature {
 for i,n := range extension.LtreeComparisonNames { if n==c.name { _,sig,_:=extension.LtreeComparisonDeclaration(i,c.left.ID());return sig } };return ""
}
