package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// InstalledText is a call of an installed one-TEXT-argument routine:
// unaccent(text) or fuzzystrmatch's daitch_mokotoff(text). The engine wraps it
// with the installed procedure (and unaccent's dictionary) identity; it is not
// a protocol node, so the wrapper's own switch cases keep applying.
type InstalledText struct {
	expr.Info
	Arg expr.Expr
	// Function is "unaccent" or "daitch_mokotoff".
	Function string
	// Matcher is the installed unaccent dictionary's rules; nil for
	// daitch_mokotoff.
	Matcher *UnaccentMatcher
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
	case "unaccent":
		if e.Matcher == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing unaccent rules")
		}
		result, err := e.Matcher.Text(ctx, input)
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(e.Usage.Type, result), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed text function %q", e.Function)
}
