package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// RangeConstructorExpr carries a selected real declaration, not a name lookup.
type RangeConstructorExpr struct {
	expr.Info
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
	Args      []expr.Expr
	Variadic  bool // Explicit VARIADIC retains the declared range[] argument.
}

func (n RangeConstructorExpr) Children() []expr.Expr { return n.Args }
func (n RangeConstructorExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args), SelectedImplicit: true}, true, nil
}
func (n RangeConstructorExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if p, ok := info.RoutineReference.(*catalog.Procedure); ok {
		n.Procedure = p
	}
	n.Info, n.Args = info, children
	if err := n.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	return n, nil
}
func (n RangeConstructorExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (RangeConstructorExpr) PlanningFold() expr.Fold                { return expr.FoldNever }
func (n RangeConstructorExpr) ValidateStoredProvider() error {
	if n.Procedure == nil || n.Procedure.RangeConstructor == nil || n.RoutineReference != n.Procedure || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != n.Procedure.Address() || n.Usage.Type != n.Procedure.RangeConstructor.Type || !n.Usage.Valid() {
		return fmt.Errorf("invalid generated constructor expression")
	}
	r := n.Procedure.RangeConstructor
	family := r.Type.RangeType()
	if family == nil {
		return fmt.Errorf("missing generated constructor family")
	}
	if _, custom := family.RangeDefinition(); !custom {
		return fmt.Errorf("invalid generated constructor family")
	}
	var expected *sqltype.Descriptor
	count := 0
	switch r.Form {
	case catalog.RangeConstructorTwo, catalog.RangeConstructorThree:
		definition, ok := r.Type.RangeDefinition()
		if !ok {
			return fmt.Errorf("missing range constructor definition")
		}
		expected = definition.Subtype.Type
		count = 2
		if r.Form == catalog.RangeConstructorThree {
			count = 3
		}
	case catalog.MultirangeConstructorZero:
		count = 0
	case catalog.MultirangeConstructorOne:
		expected = r.Type.RangeType()
		count = 1
	case catalog.MultirangeConstructorMany:
		expected = r.Type.RangeType()
		count = len(n.Args)
		if n.Variadic {
			if count != 1 || !n.Args[0].ResultUsage().Type.IsArray() || n.Args[0].ResultUsage().Type.Element() != expected {
				return fmt.Errorf("invalid explicit constructor variadic argument")
			}
			expected = n.Args[0].ResultUsage().Type
		}
	default:
		return fmt.Errorf("invalid range constructor form")
	}
	if len(n.Args) != count {
		return fmt.Errorf("invalid range constructor arity")
	}
	want := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range n.Args {
		if arg == nil {
			return fmt.Errorf("missing range constructor argument")
		}
		t := arg.ResultUsage().Type
		if r.Form == catalog.RangeConstructorThree && i == 2 {
			if t.ID() != sqltype.Text {
				return fmt.Errorf("invalid constructor flags type")
			}
		} else if t != expected {
			return fmt.Errorf("invalid constructor endpoint generation")
		}
		want = expr.MergeProperties(want, arg.Properties())
	}
	if n.Property != want {
		return fmt.Errorf("invalid constructor properties")
	}
	return nil
}
func (n RangeConstructorExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(RangeConstructorExpr)
	if !ok || n.Ref != o.Ref || n.Variadic != o.Variadic || n.Procedure.RangeConstructor != o.Procedure.RangeConstructor || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (n RangeConstructorExpr) Eval(c expr.Context) (sqltype.Value, error) {
	ctx := c.FunctionContext()
	if ctx == nil {
		return sqltype.Value{}, fmt.Errorf("missing constructor evaluation context")
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	r := n.Procedure.RangeConstructor
	if r.Form == catalog.MultirangeConstructorMany && !n.Variadic {
		return sqltype.MultirangeFromSource(ctx, r.Type, len(n.Args), func(i int) (sqltype.Value, error) { return n.Args[i].Eval(c) })
	}
	var small [3]sqltype.Value
	values := small[:len(n.Args)]
	for i, arg := range n.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	// Strictness belongs to the declared array, not its expanded members.
	if n.Procedure.Strict && (r.Form != catalog.MultirangeConstructorMany || n.Variadic) {
		for _, v := range values {
			if v.IsNull() {
				return sqltype.Null(n.Usage.Type), nil
			}
		}
	}
	switch r.Form {
	case catalog.RangeConstructorTwo, catalog.RangeConstructorThree:
		flags := "[)"
		if len(values) == 3 {
			if values[2].IsNull() {
				return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22000", Message: "range constructor flags argument must not be null"}
			}
			var err error
			flags, err = values[2].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		return sqltype.RangeFromValues(ctx, r.Type, values[0], values[1], flags)
	case catalog.MultirangeConstructorMany:
		if n.Variadic {
			return sqltype.MultirangeFromArray(ctx, r.Type, values[0])
		}
	}
	return sqltype.MultirangeFromValues(ctx, r.Type, values)
}
func (RangeConstructorExpr) SourceAdmission() (bool, bool) { return true, false }
func (n RangeConstructorExpr) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n RangeConstructorExpr) ScalarSourceOutName() string { return n.Procedure.Name() }
