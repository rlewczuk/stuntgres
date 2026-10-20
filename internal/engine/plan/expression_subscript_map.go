package plan

import (
	"stuntgres/internal/engine/expr"
)

// MapSubscript maps a subscript's base and bounds with rewrite. Only changed
// child lists are copied; shared plans remain immutable.
func MapSubscript(n SubscriptExpr, rewrite func(expr.Expr) (expr.Expr, bool, error)) (SubscriptExpr, bool, error) {
	base, changed, err := rewrite(n.Base)
	if err != nil {
		return n, false, err
	}
	n.Base = base
	var subs []BoundArraySubscript
	for i, s := range n.Subscripts {
		lower, did, err := rewrite(s.Lower)
		if err != nil {
			return n, false, err
		}
		upper, more, err := rewrite(s.Upper)
		if err != nil {
			return n, false, err
		}
		if did || more {
			if subs == nil {
				subs = append([]BoundArraySubscript(nil), n.Subscripts...)
			}
			s.Lower, s.Upper = lower, upper
			subs[i] = s
			changed = true
		}
	}
	if subs != nil {
		n.Subscripts = subs
	}
	return n, changed, nil
}
