package functions

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type ArrayInspectKind uint8

const (
	ArrayDims ArrayInspectKind = iota + 1
	ArrayLength
	ArrayLower
	ArrayUpper
	ArrayNDims
	ArrayCardinality
)

func ArrayInspectFunction(name string) ArrayInspectKind {
	switch name {
	case "array_dims":
		return ArrayDims
	case "array_length":
		return ArrayLength
	case "array_lower":
		return ArrayLower
	case "array_upper":
		return ArrayUpper
	case "array_ndims":
		return ArrayNDims
	case "cardinality":
		return ArrayCardinality
	}
	return 0
}
func (k ArrayInspectKind) Name() string {
	switch k {
	case ArrayDims:
		return "array_dims"
	case ArrayLength:
		return "array_length"
	case ArrayLower:
		return "array_lower"
	case ArrayUpper:
		return "array_upper"
	case ArrayNDims:
		return "array_ndims"
	case ArrayCardinality:
		return "cardinality"
	}
	return ""
}
func (k ArrayInspectKind) Arity() int {
	if k >= ArrayLength && k <= ArrayUpper {
		return 2
	}
	if k.Name() != "" {
		return 1
	}
	return 0
}
func (k ArrayInspectKind) Result() sqltype.ID {
	if k == ArrayDims {
		return sqltype.Text
	}
	return sqltype.Int4
}

type ArrayInspect struct {
	expr.Info
	Kind      ArrayInspectKind
	Args      []expr.Expr
	Namespace string
}

func (e ArrayInspect) Validate() error {
	if e.Kind.Arity() == 0 || len(e.Args) != e.Kind.Arity() || e.Usage.Type == nil || e.Usage.Type.ID() != e.Kind.Result() || !e.Usage.Type.Concrete() || e.Namespace != "" && e.Namespace != "pg_catalog" {
		return fmt.Errorf("stuntgres: invalid stored array inspection provider")
	}
	for _, arg := range e.Args {
		if any(arg) == nil {
			return fmt.Errorf("stuntgres: missing stored array inspection argument")
		}
	}
	u := e.Args[0].ResultUsage()
	if u.Type == nil || !u.CatalogNullUsage() && (!u.Type.Concrete() || !u.Type.IsArray()) {
		return fmt.Errorf("stuntgres: invalid stored array inspection input")
	}
	if len(e.Args) == 2 && e.Args[1].ResultUsage().Type != e.Usage.Type {
		return fmt.Errorf("stuntgres: invalid stored array inspection dimension")
	}
	return nil
}

func (e ArrayInspect) Reconstruct(info expr.Info, args []expr.Expr) (ArrayInspect, error) {
	e.Info, e.Args = info, args
	return e, e.Validate()
}

// ArrayInspect implements expr.Composite.
var _ expr.Composite = ArrayInspect{}

func (e ArrayInspect) Children() []expr.Expr { return e.Args }
func (e ArrayInspect) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ArrayInspect", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}

func (e ArrayInspect) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for _, v := range values[:len(e.Args)] {
		if v.IsNull() {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	a := values[0]
	if e.Kind == ArrayCardinality {
		return sqltype.Integer(e.Usage.Type, int64(a.ArrayLen()))
	}
	rank := a.ArrayRank()
	if rank == 0 {
		return sqltype.Null(e.Usage.Type), nil
	}
	if e.Kind == ArrayNDims {
		return sqltype.Integer(e.Usage.Type, int64(rank))
	}
	if e.Kind == ArrayDims {
		var text strings.Builder
		text.Grow(rank * 26)
		var scratch [20]byte
		for i := range rank {
			d := a.ArrayDimension(i)
			text.WriteByte('[')
			text.Write(strconv.AppendInt(scratch[:0], int64(d.Lower), 10))
			text.WriteByte(':')
			text.Write(strconv.AppendInt(scratch[:0], int64(d.Lower)+int64(d.Length)-1, 10))
			text.WriteByte(']')
		}
		return sqltype.String(e.Usage.Type, text.String()), nil
	}
	dimension, err := values[1].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	if dimension <= 0 || dimension > int64(rank) {
		return sqltype.Null(e.Usage.Type), nil
	}
	d := a.ArrayDimension(int(dimension) - 1)
	n := int64(d.Length)
	if e.Kind == ArrayLower {
		n = int64(d.Lower)
	}
	if e.Kind == ArrayUpper {
		n = int64(d.Lower) + int64(d.Length) - 1
	}
	return sqltype.Integer(e.Usage.Type, n)
}

func (e ArrayInspect) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e ArrayInspect) ValidateStoredProvider() error { return e.Validate() }

var _ expr.Renderable = ArrayInspect{}

// Render implements expr.Renderable.
func (e ArrayInspect) Render() (expr.Rendering, error) {
	spec := callRendering("ArrayInspect "+e.Kind.Name()+" namespace "+e.Namespace, e.Args, e.Kind.Name(), e.Args)
	spec.SimpleCast = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e ArrayInspect) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(ArrayInspect)
	// Kind identifies the resolved builtin; lexical pg_catalog qualification
	// is presentation provenance, not a different grouping expression.
	if !ok || e.Kind != o.Kind || len(e.Args) != len(o.Args) {
		return false
	}
	for i := range e.Args {
		if !same(e.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e ArrayInspect) LooksLikeFunction() bool { return true }

// Substitute implements expr.Substitutable.
func (e ArrayInspect) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ArrayInspect", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e ArrayInspect) ClosureTransparent() {}
