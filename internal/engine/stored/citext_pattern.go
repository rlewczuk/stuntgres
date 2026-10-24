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
type citextPatternCapture struct {
	ref         ids.ObjectRef
	left, right *sqltype.Descriptor
	name        string
	result      sqltype.ID
}

func citextPatternCaptureOf(node expr.Expr) (citextPatternCapture, bool) {
	capture := citextPatternCapture{result: sqltype.Bool}
	n, ok := node.(plan.CitextPatternExpr)
	if !ok {
		return capture, false
	}
	capture.ref = n.Ref
	if n.Args[0] != nil && n.Args[1] != nil {
		capture.left, capture.right = n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
	}
	if int(n.Kind) < len(extension.CitextPatternNames) {
		capture.name = extension.CitextPatternNames[n.Kind]
	}
	return capture, true
}

// The reference is already mapped. The scoped callback supplies the mapped
// ORIGINAL; current descriptors are used only to validate destination authority.
func remapCitextPatternRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, capture citextPatternCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	owner := view.Owner()
	if owner == nil || capture.ref.Domain() != owner.Domain || capture.ref.Epoch != view.Epoch() || capture.ref.Address.ClassID != ids.ProcedureClassID || capture.ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid captured citext pattern reference")
	}
	if !capture.left.IsCitext() || (capture.left != capture.right && (capture.right == nil || !capture.right.Concrete() || capture.right.ID() != sqltype.Text)) {
		return fmt.Errorf("stuntgres: invalid captured citext pattern operands")
	}
	if captured == nil || original == nil || captured.Revision() == 0 || captured.Revision() != original.Revision() || captured.Implementation != extension.StockCitext || captured.Signature != extension.CitextPatternSignature(capture.left.ID(), capture.right.ID()) || captured.Result != ids.OID(capture.result) || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Immutable || captured.Builtin != nil {
		return fmt.Errorf("stuntgres: invalid captured citext pattern declaration")
	}
	capture.left, capture.right = typ(capture.left), typ(capture.right)
	descriptor, _ := catalog.DescriptorAt(view, reader, capture.ref.Address)
	declaration, ok := descriptor.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(declaration, original) {
		return fmt.Errorf("stuntgres: missing remapped citext pattern declaration")
	}
	_, err := currentCitextPatternProcedure(view, original, capture)
	return err
}

func currentCitextPatternProcedure(view catalog.Read, original *catalog.Procedure, capture citextPatternCapture) (*catalog.Procedure, error) {
	if !capture.left.IsCitext() || (capture.left != capture.right && (capture.right == nil || !capture.right.Concrete() || capture.right.ID() != sqltype.Text)) {
		return nil, fmt.Errorf("stuntgres: invalid mapped citext pattern operands")
	}
	if capture.right != capture.left {
		text, ok := view.LookupType(sqltype.Text)
		if !ok || capture.right != text {
			return nil, fmt.Errorf("stuntgres: foreign mapped citext pattern text")
		}
	}
	current, symbol, err := currentInstalledProcedure(view, capture.ref, original)
	if err != nil {
		return nil, err
	}
	if current.Implementation != extension.StockCitext || symbol != capture.name || current.Signature != extension.CitextPatternSignature(capture.left.ID(), capture.right.ID()) || current.Result != ids.OID(capture.result) || current.RoutineKind != catalog.FunctionRoutine || !current.Strict || current.Volatility != expr.Immutable || current.Builtin != nil {
		return nil, fmt.Errorf("stuntgres: invalid mapped citext pattern declaration")
	}
	installation, exists := view.Extension(current.Extension)
	if !exists || len(installation.Members) != extension.CitextMemberCount {
		return nil, fmt.Errorf("stuntgres: missing mapped citext pattern installation")
	}
	base, exists := view.Type(installation.Members[0])
	if !exists || base.SQLType != capture.left || base.Extension != installation.Address() {
		return nil, fmt.Errorf("stuntgres: foreign mapped citext pattern type")
	}
	member := -1
	for i, name := range extension.CitextPatternNames {
		if name == capture.name {
			member = extension.CitextPatternProcedureStart + i*2
			if capture.right != capture.left {
				member++
			}
			break
		}
	}
	if member < 0 || installation.Members[member] != current.Address() {
		return nil, fmt.Errorf("stuntgres: invalid mapped citext pattern member")
	}
	if err := extension.StockCitext.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}

func currentCitextPattern(n plan.CitextPatternExpr, view catalog.Read) (*catalog.Procedure, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	original, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	capture, _ := citextPatternCaptureOf(n)
	result, ok := view.LookupType(capture.result)
	if !ok || n.ResultUsage().Type != result {
		return nil, fmt.Errorf("stuntgres: foreign citext pattern result")
	}
	return currentCitextPatternProcedure(view, original, capture)
}

func citextPatternRendering(n plan.CitextPatternExpr, name string) (expr.Rendering, error) {
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
