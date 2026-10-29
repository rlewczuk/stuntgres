package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Capture separates original operand and routine authority from mapped references.
type citextComparisonCapture struct {
	ref         ids.ObjectRef
	left, right *sqltype.Descriptor
	name        string
	result      sqltype.ID
}

func citextComparisonCaptureOf(node expr.Expr) (citextComparisonCapture, bool) {
	capture := citextComparisonCapture{result: sqltype.Bool}
	n, ok := node.(plan.CitextComparisonExpr)
	if !ok {
		return capture, false
	}
	capture.ref = n.Ref
	if n.Args[0] != nil && n.Args[1] != nil {
		capture.left, capture.right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
	}
	if int(n.Kind) < len(extension.CitextComparisonNames) {
		capture.name = extension.CitextComparisonNames[n.Kind]
	}
	return capture, true
}

// The reference is already mapped. The scoped callback supplies the mapped
// ORIGINAL; current descriptors are used only to validate destination authority.
func remapCitextComparisonRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, capture citextComparisonCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	owner := view.Owner()
	if owner == nil || capture.ref.Domain() != owner.Domain || capture.ref.Epoch != view.Epoch() || capture.ref.Address.ClassID != ids.ProcedureClassID || capture.ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid captured citext comparison reference")
	}
	if !capture.left.IsCitext() || capture.left != capture.right {
		return fmt.Errorf("stuntgres: invalid captured citext comparison operands")
	}
	if captured == nil || original == nil || captured.Revision() == 0 || captured.Revision() != original.Revision() || captured.Implementation != extension.StockCitext || captured.Signature != extension.CitextComparisonSignature(capture.left.ID()) || captured.Result != ids.OID(capture.result) || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Builtin != nil {
		return fmt.Errorf("stuntgres: invalid captured citext comparison declaration")
	}
	capture.left, capture.right = typ(capture.left), typ(capture.right)
	descriptor, _ := catalog.DescriptorAt(view, reader, capture.ref.Address)
	declaration, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(declaration, original) {
		return fmt.Errorf("stuntgres: missing remapped citext comparison declaration")
	}
	_, err := currentCitextComparisonProcedure(view, original, capture)
	return err
}

func currentCitextComparisonProcedure(view catalog.Read, original *catalog.Procedure, capture citextComparisonCapture) (*catalog.Procedure, error) {
	if !capture.left.IsCitext() || capture.left != capture.right {
		return nil, fmt.Errorf("stuntgres: invalid mapped citext comparison operands")
	}
	current, symbol, err := currentInstalledProcedure(view, capture.ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockCitext || symbol != capture.name || current.Signature != extension.CitextComparisonSignature(capture.left.ID()) || current.Result != ids.OID(capture.result) || current.RoutineKind != catalog.FunctionRoutine || !current.Strict || current.Volatility != expr.Immutable || current.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: invalid mapped citext comparison declaration")
	}
	installation, exists := catalog.OriginInstallation(current)
	if !exists || len(installation.Members) != extension.CitextMemberCount {
		return nil, fmt.Errorf("stuntgres: missing mapped citext comparison installation")
	}
	base, exists := view.Type(installation.Members[0])
	if !exists || base.SQLType != capture.left || !catalog.SameInstallation(base, installation) {
		return nil, fmt.Errorf("stuntgres: foreign mapped citext comparison type")
	}
	member := -1
	for i, name := range extension.CitextComparisonNames {
		if name == capture.name {
			member = 3 + i
			break
		}
	}
	if member < 0 || installation.Members[member] != current.Address() {
		return nil, fmt.Errorf("stuntgres: invalid mapped citext comparison member")
	}
	if err := extension.StockCitext.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func currentCitextComparison(n plan.CitextComparisonExpr, view catalog.Read) (*catalog.Procedure, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	original, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	capture, _ := citextComparisonCaptureOf(n)
	result, ok := view.LookupType(capture.result)
	if !ok || n.ResultUsage().Type != result {
		return nil, fmt.Errorf("stuntgres: foreign citext comparison result")
	}
	return currentCitextComparisonProcedure(view, original, capture)
}

func citextComparisonRendering(n plan.CitextComparisonExpr, name string) (expr.Rendering, error) {
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
