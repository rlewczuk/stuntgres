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
// NULLIF returns its hstore operand while its selected equality returns BOOL.
type hstoreComparisonCapture struct {
	ref         ids.ObjectRef
	left, right *sqltype.Descriptor
	name        string
	result      sqltype.ID
}

func hstoreComparisonCaptureOf(node expr.Expr) (hstoreComparisonCapture, bool) {
	capture := hstoreComparisonCapture{result: sqltype.Bool}
	operator := "="
	direct := false
	switch n := node.(type) {
	case plan.HstoreComparisonExpr:
		direct = true
		capture.ref = n.Ref
		if len(n.Args) > 0 && n.Args[0] != nil {
			capture.left = n.Args[0].ResultUsage().Type
			capture.right = capture.left
		}
		if len(n.Args) == 2 && n.Args[0] != nil && n.Args[1] != nil {
			capture.left, capture.right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
		}
		if int(n.Kind) < len(extension.HstoreComparisonNames) {
			capture.name = extension.HstoreComparisonNames[n.Kind]
		}
		if n.Kind == 6 || n.Kind == 7 {
			capture.result = sqltype.Int4
		}
		if n.Kind == 8 {
			capture.result = sqltype.Int8
		}
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
	if !direct {
		switch operator {
		case "=":
			capture.name = "hstore_eq"
		case "<>", "!=":
			capture.name = "hstore_ne"
		case "<":
			capture.name = "hstore_lt"
		case "<=":
			capture.name = "hstore_le"
		case ">":
			capture.name = "hstore_gt"
		case ">=":
			capture.name = "hstore_ge"
		}
	}
	return capture, direct || capture.left.IsHstore() || capture.right.IsHstore()
}

// The reference is already mapped. The scoped callback supplies the mapped
// ORIGINAL; current descriptors are used only to validate destination authority.
func remapHstoreComparisonRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, capture hstoreComparisonCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	owner := view.Owner()
	if owner == nil || capture.ref.Domain() != owner.Domain || capture.ref.Epoch != view.Epoch() || capture.ref.Address.ClassID != ids.ProcedureClassID || capture.ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid captured hstore comparison reference")
	}
	if !capture.left.IsHstore() || capture.left != capture.right && capture.name != "hstore_hash_extended" {
		return fmt.Errorf("stuntgres: invalid captured hstore comparison operands")
	}
	if captured == nil || original == nil || captured.Revision() == 0 || captured.Revision() != original.Revision() || captured.Implementation != extension.StockHstore || captured.Signature != hstoreCaptureSignature(capture) || captured.Result != ids.OID(capture.result) || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Builtin != nil {
		return fmt.Errorf("stuntgres: invalid captured hstore comparison declaration")
	}
	sameTypeGeneration := capture.left == typ(capture.left)
	capture.left, capture.right = typ(capture.left), typ(capture.right)
	descriptor, _ := catalog.DescriptorAt(view, reader, capture.ref.Address)
	declaration, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(declaration, original) {
		return fmt.Errorf("stuntgres: missing remapped hstore comparison declaration")
	}
	// An unchanged type identity keeps this capture in its original generation.
	// Historical presentation revisions remain valid, but a declaration cannot
	// have been captured from a revision newer than that generation's current root.
	// Cross-generation transport preserves the source revision independently.
	if original == captured && sameTypeGeneration && original.Revision() > declaration.Revision() {
		return fmt.Errorf("stuntgres: future hstore comparison declaration")
	}
	_, err := currentHstoreComparisonProcedure(view, original, capture)
	return err
}

func currentHstoreComparisonProcedure(view catalog.Read, original *catalog.Procedure, capture hstoreComparisonCapture) (*catalog.Procedure, error) {
	if !capture.left.IsHstore() || capture.left != capture.right && capture.name != "hstore_hash_extended" {
		return nil, fmt.Errorf("stuntgres: invalid mapped hstore comparison operands")
	}
	current, symbol, err := currentInstalledProcedure(view, capture.ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockHstore || symbol != capture.name || current.Signature != hstoreCaptureSignature(capture) || current.Result != ids.OID(capture.result) || current.RoutineKind != catalog.FunctionRoutine || !current.Strict || current.Volatility != expr.Immutable || current.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: invalid mapped hstore comparison declaration")
	}
	installation, exists := catalog.OriginInstallation(current)
	if !exists || len(installation.Members) != extension.HstoreMemberCount {
		return nil, fmt.Errorf("stuntgres: missing mapped hstore comparison installation")
	}
	base, exists := view.Type(installation.Members[0])
	if !exists || base.SQLType != capture.left || !catalog.SameInstallation(base, installation) {
		return nil, fmt.Errorf("stuntgres: foreign mapped hstore comparison type")
	}
	member := -1
	for i, name := range extension.HstoreComparisonNames {
		if name == capture.name {
			member = extension.HstoreComparisonProcedureStart + i
			break
		}
	}
	if member < 0 || installation.Members[member] != current.Address() {
		return nil, fmt.Errorf("stuntgres: invalid mapped hstore comparison member")
	}
	if err := extension.StockHstore.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func currentHstoreComparison(n plan.HstoreComparisonExpr, view catalog.Read) (*catalog.Procedure, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	original, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	capture, _ := hstoreComparisonCaptureOf(n)
	result, ok := view.LookupType(capture.result)
	if !ok || n.ResultUsage().Type != result {
		return nil, fmt.Errorf("stuntgres: foreign hstore comparison result")
	}
	return currentHstoreComparisonProcedure(view, original, capture)
}

func hstoreComparisonRendering(n plan.HstoreComparisonExpr, name string) (expr.Rendering, error) {
	spec, err := n.Render()
	if err != nil {
		return expr.Rendering{}, err
	}
	if spec.Name != name {
		spec.Name = name
		spec.Replay.Pieces[0] = expr.TextPiece(name + "(")
	}
	return spec, nil
}

func hstoreCaptureSignature(c hstoreComparisonCapture) catalog.ProcedureSignature {
	for i, n := range extension.HstoreComparisonNames {
		if n == c.name {
			_, sig, _ := extension.HstoreComparisonDeclaration(i, c.left.ID())
			return sig
		}
	}
	return ""
}
