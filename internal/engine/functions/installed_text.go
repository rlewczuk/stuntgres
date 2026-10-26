package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// InstalledText is a call of an installed one-TEXT-argument routine:
// unaccent(text) or fuzzystrmatch's daitch_mokotoff(text). The engine wraps it
// with the installed procedure identity and supplies unaccent's catalog-dependent
// execution. It is not a protocol node, so the wrapper's switch cases apply.
type InstalledText struct {
	expr.Info
	Arg expr.Expr
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
