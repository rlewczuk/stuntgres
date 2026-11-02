package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// InstalledText is an installed unaccent or daitch_mokotoff call. Dictionary
// is present only for unaccent(regdictionary,text). The engine wrapper owns
// installed authority and catalog-dependent execution; this is not a protocol
// node, so every wrapper switch must preserve both children in source order.
type InstalledText struct {
	expr.Info
	Dictionary expr.Expr
	Arg        expr.Expr
	// Function is "unaccent" or "daitch_mokotoff".
	Function string
}

func (e InstalledText) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	value, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	input, err := value.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	switch e.Function {
	case "daitch_mokotoff":
		codes, err := DaitchMokotoff(ctx, input)
		if err != nil {
			return sqltype.Value{}, err
		}
		if codes == nil {
			return sqltype.Null(e.Usage.Type), nil
		}
		elements := make([]sqltype.Value, len(codes))
		for i, code := range codes {
			elements[i] = sqltype.String(e.Usage.Type.Element(), code)
		}
		return sqltype.OwnedArrayValue(e.Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(codes))}}, elements)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed text function %q", e.Function)
}
