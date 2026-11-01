package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type isnCapture struct {
	ref                 ids.ObjectRef
	left, right, target *sqltype.Descriptor
	result              sqltype.ID
	op                  string
	cast                bool
}

func isnCaptureOf(node expr.Expr) (isnCapture, bool) {
	c := isnCapture{result: sqltype.Bool, op: "="}
	switch n := node.(type) {
	case plan.ComparisonExpr:
		c.ref, c.left, c.right, c.op = n.RoutineRef, n.Left.ResultUsage().Type, n.Right.ResultUsage().Type, n.Operator
	case plan.DistinctExpr:
		c.ref, c.left, c.right = n.RoutineRef, n.Left.ResultUsage().Type, n.Right.ResultUsage().Type
	case plan.ScalarArrayComparisonExpr:
		c.ref, c.left, c.right, c.op = n.RoutineRef, n.Left.ResultUsage().Type, n.ElementType(), n.Operator
	case plan.ConditionalExpr:
		if n.Kind != parser.NullIf || len(n.Args) != 2 {
			return c, false
		}
		c.ref, c.left, c.right = n.RoutineRef, n.Args[0].ResultUsage().Type, n.Args[1].ResultUsage().Type
	case functions.CastRoutine:
		if n.Count != 1 || !n.Usage.Type.IsISN() || !n.Args[0].ResultUsage().Type.IsISN() {
			return c, false
		}
		c.left, c.target, c.result, c.cast = n.Args[0].ResultUsage().Type, n.Usage.Type, n.Usage.Type.ID(), true
		if p, ok := n.RoutineReference.(*catalog.Procedure); ok && p != nil {
			c.ref.Address = p.Address()
		}
	default:
		return c, false
	}
	return c, c.left.IsISN() || c.right.IsISN() || c.cast && c.target.IsISN()
}
func isnCaptureSignature(c isnCapture) catalog.ProcedureSignature {
	if c.left == nil || !c.cast && c.right == nil {
		return ""
	}
	if c.cast {
		return extension.IsnSignature(c.left.ID())
	}
	return catalog.ProcedureSignature(fmt.Sprintf("%d,%d", c.left.ID(), c.right.ID()))
}
func validateIsnCaptureProcedure(view catalog.Read, p *catalog.Procedure, c isnCapture) error {
	if p == nil || p.Implementation != extension.StockIsn || p.Signature != isnCaptureSignature(c) || p.Result != ids.OID(c.result) {
		return fmt.Errorf("stuntgres: invalid ISN captured signature")
	}
	if err := extension.StockIsn.ValidateProcedure(view, p); err != nil {
		return err
	}
	for _, typ := range [...]*sqltype.Descriptor{c.left, c.right, c.target} {
		if typ == nil {
			continue
		}
		current, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		if !ok || current.SQLType != typ || !catalog.SameInstallation(current, p.Origin.Installation) {
			return fmt.Errorf("stuntgres: foreign ISN captured type generation")
		}
		if err := extension.ValidateTypeOrigin(view, current); err != nil {
			return err
		}
	}
	if c.cast {
		if p.Origin.Declaration < extension.IsnConversionStart || p.Origin.Declaration >= extension.IsnSupportStart {
			return fmt.Errorf("stuntgres: invalid ISN conversion declaration")
		}
	} else {
		op, ok := extension.IsnComparisonOperation(view, p)
		if !ok || []string{"=", "<>", "<", "<=", ">", ">="}[op] != c.op {
			return fmt.Errorf("stuntgres: invalid ISN comparison declaration")
		}
	}
	return nil
}
func remapIsnRoutine(view catalog.Read, reader catalog.DefinitionReader, captured, original *catalog.Procedure, c isnCapture, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	if captured == nil || original == nil || captured.Revision() == 0 || captured.Revision() != original.Revision() || captured.Implementation != extension.StockIsn || captured.Signature != isnCaptureSignature(c) || captured.Result != ids.OID(c.result) || !captured.Strict || captured.Volatility != expr.Immutable {
		return fmt.Errorf("stuntgres: invalid original ISN declaration")
	}
	if !c.cast && (view.Owner() == nil || c.ref.Domain() != view.Owner().Domain || c.ref.Epoch != view.Epoch() || c.ref.Address != original.Address()) {
		return fmt.Errorf("stuntgres: invalid ISN routine reference")
	}
	if !captured.Origin.Valid(captured.Address()) || captured.Origin.Installation.Implementation != extension.StockIsn {
		return fmt.Errorf("stuntgres: foreign ISN original origin")
	}
	sameGeneration := c.left == typ(c.left) && (!c.cast && c.right == typ(c.right) || c.cast && c.target == typ(c.target))
	c.left = typ(c.left)
	if c.cast {
		c.target = typ(c.target)
		if c.target == nil {
			return fmt.Errorf("stuntgres: missing mapped ISN result")
		}
		c.result = c.target.ID()
	} else {
		c.right = typ(c.right)
	}
	d, _ := catalog.DescriptorAt(view, reader, original.Address())
	current, ok := d.(*catalog.Procedure)
	if !ok || !exec.SameRoutineDeclaration(current, original) {
		return fmt.Errorf("stuntgres: missing remapped ISN original")
	}
	if captured == original && sameGeneration && original.Revision() > current.Revision() {
		return fmt.Errorf("stuntgres: future ISN original declaration")
	}
	live, err := exec.ValidateStoredRoutineDescriptor(view, original)
	if err != nil {
		return err
	}
	return validateIsnCaptureProcedure(view, live, c)
}

func isnNamedComparisonSQL(view catalog.Read, n plan.ComparisonExpr, render func(expr.Expr) (string, error)) (string, error) {
	captured, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return "", err
	}
	current, err := exec.ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return "", err
	}
	c, ok := isnCaptureOf(n)
	if !ok {
		return "", fmt.Errorf("stuntgres: invalid named comparison")
	}
	if err := validateIsnCaptureProcedure(view, current, c); err != nil {
		return "", err
	}
	left, err := render(n.Left)
	if err != nil {
		return "", err
	}
	right, err := render(n.Right)
	if err != nil {
		return "", err
	}
	ns, ok := view.Namespace(current.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing ISN namespace")
	}
	return catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(current.Name()) + "(" + left + ", " + right + ")", nil
}
