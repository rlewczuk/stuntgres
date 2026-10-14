package functions

import (
	"fmt"
	"strconv"
	"strings"

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

type ArrayInspect[E Expression[C], C Context, P any] struct {
	Info[P]
	Kind      ArrayInspectKind
	Args      []E
	Namespace string
}

func (e ArrayInspect[E, C, P]) Validate() error {
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

func (e ArrayInspect[E, C, P]) Reconstruct(info Info[P], args []E) (ArrayInspect[E, C, P], error) {
	e.Info, e.Args = info, args
	return e, e.Validate()
}

func (e ArrayInspect[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
