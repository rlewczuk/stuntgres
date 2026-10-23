package stored

import (
	"context"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// These operation-local records describe source topology, not executable
// providers. Empty, SQL NULL and independently unbounded sides stay distinct.
type aliasInterval struct {
	lower, upper                   catalog.AliasOrderEndpoint
	lowerFinite, upperFinite       bool
	lowerInclusive, upperInclusive bool
	empty                          bool
}
type aliasIntervalDatum struct {
	intervals   []aliasInterval
	points      []catalog.AliasOrderEndpoint // includes hidden overlap boundaries
	multi, null bool
}

func aliasIntervalBounds(next *catalog.State, b sqltype.RangeBounds) (aliasInterval, error) {
	r := aliasInterval{empty: b.Empty, lowerFinite: !b.LowerUnbounded, upperFinite: !b.UpperUnbounded, lowerInclusive: b.LowerInclusive && !b.LowerUnbounded, upperInclusive: b.UpperInclusive && !b.UpperUnbounded}
	if r.empty {
		return r, nil
	}
	var err error
	if r.lowerFinite {
		r.lower, err = aliasRangeEndpoint(next, b.Lower)
		if err != nil {
			return r, err
		}
	}
	if r.upperFinite {
		r.upper, err = aliasRangeEndpoint(next, b.Upper)
		if err != nil {
			return r, err
		}
	}
	return r, nil
}

func aliasLiteralIntervals(ctx context.Context, next *catalog.State, v sqltype.Value) (aliasIntervalDatum, bool, error) {
	d := aliasIntervalDatum{null: v.IsNull(), multi: v.Type().BaseType().IsMultirange()}
	if v.Type().BaseType().RangeType() == nil {
		return d, false, nil
	}
	if d.null {
		return d, true, nil
	}
	appendRange := func(value sqltype.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		bounds, err := value.RangeBounds()
		if err != nil {
			return err
		}
		r, err := aliasIntervalBounds(next, bounds)
		if err != nil {
			return err
		}
		if !r.empty {
			d.intervals = append(d.intervals, r)
		}
		return nil
	}
	if !d.multi {
		if err := appendRange(v); err != nil {
			return d, false, err
		}
	} else {
		for i := range v.MultirangeLen() {
			if err := appendRange(v.MultirangeMember(i)); err != nil {
				return d, false, err
			}
		}
	}
	return d, true, ctx.Err()
}

// finishAliasDatum records the complete endpoint preorder of this one datum
// before structurally normalizing its source intervals. Written argument order
// cannot hide a member gap or discard a comparison inside overlapping members.
func finishAliasDatum(ctx context.Context, d aliasIntervalDatum) (aliasIntervalDatum, bool, error) {
	for _, r := range d.intervals {
		if err := ctx.Err(); err != nil {
			return d, false, err
		}
		if r.empty {
			continue
		}
		if r.lowerFinite {
			d.points = append(d.points, r.lower)
		}
		if r.upperFinite {
			d.points = append(d.points, r.upper)
		}
	}
	if !d.multi {
		return d, true, ctx.Err()
	}
	sort.Slice(d.points, func(i, j int) bool {
		a, b := d.points[i], d.points[j]
		if a.OID != b.OID {
			return a.OID < b.OID
		}
		if a.Object.ClassID != b.Object.ClassID {
			return a.Object.ClassID < b.Object.ClassID
		}
		if a.Object.ObjectID != b.Object.ObjectID {
			return a.Object.ObjectID < b.Object.ObjectID
		}
		return a.Domain < b.Domain
	})
	sort.Slice(d.intervals, func(i, j int) bool {
		a, b := d.intervals[i], d.intervals[j]
		if a.lowerFinite != b.lowerFinite {
			return !a.lowerFinite
		}
		if a.lowerFinite && a.lower.OID != b.lower.OID {
			return a.lower.OID < b.lower.OID
		}
		if a.lowerInclusive != b.lowerInclusive {
			return a.lowerInclusive
		}
		if a.upperFinite != b.upperFinite {
			return a.upperFinite
		}
		if a.upperFinite && a.upper.OID != b.upper.OID {
			return a.upper.OID < b.upper.OID
		}
		return !a.upperInclusive && b.upperInclusive
	})
	n := 0
	for _, r := range d.intervals {
		if err := ctx.Err(); err != nil {
			return d, false, err
		}
		if r.empty {
			continue
		}
		if n == 0 {
			d.intervals[0] = r
			n = 1
			continue
		}
		prior := &d.intervals[n-1]
		connected := !prior.upperFinite || !r.lowerFinite || prior.upper.OID > r.lower.OID || prior.upper.OID == r.lower.OID && (prior.upperInclusive || r.lowerInclusive)
		if !connected {
			d.intervals[n] = r
			n++
			continue
		}
		if !r.upperFinite || prior.upperFinite && r.upper.OID > prior.upper.OID {
			prior.upper, prior.upperFinite, prior.upperInclusive = r.upper, r.upperFinite, r.upperInclusive
		} else if prior.upperFinite && r.upperFinite && prior.upper.OID == r.upper.OID {
			prior.upperInclusive = prior.upperInclusive || r.upperInclusive
		}
	}
	d.intervals = d.intervals[:n]
	return d, true, ctx.Err()
}

func capturedAliasDatum(ctx context.Context, next *catalog.State, node expr.Expr) (aliasIntervalDatum, bool, error) {
	d, known, err := capturedAliasIntervals(ctx, next, node)
	if err != nil || !known {
		return d, known, err
	}
	return finishAliasDatum(ctx, d)
}

func capturedAliasIntervals(ctx context.Context, next *catalog.State, node expr.Expr) (aliasIntervalDatum, bool, error) {
	if err := ctx.Err(); err != nil {
		return aliasIntervalDatum{}, false, err
	}
	switch n := node.(type) {
	case plan.LiteralExpr:
		return aliasLiteralIntervals(ctx, next, n.Value)
	case plan.CoercionExpr:
		if n.Child.ResultUsage().Type.BaseType().ID() == n.ResultUsage().Type.BaseType().ID() {
			return capturedAliasIntervals(ctx, next, n.Child)
		}
	case plan.CollationExpr:
		return capturedAliasIntervals(ctx, next, n.Child)
	case plan.RangeConstructorExpr:
		if n.Procedure == nil || n.Procedure.RangeConstructor == nil {
			return aliasIntervalDatum{}, false, nil
		}
		form := n.Procedure.RangeConstructor.Form
		if form == catalog.RangeConstructorTwo || form == catalog.RangeConstructorThree {
			if len(n.Args) < 2 {
				return aliasIntervalDatum{}, false, nil
			}
			lo, ok := capturedAliasLiteral(n.Args[0])
			if !ok {
				return aliasIntervalDatum{}, false, nil
			}
			hi, ok := capturedAliasLiteral(n.Args[1])
			if !ok {
				return aliasIntervalDatum{}, false, nil
			}
			flags := "[)"
			if form == catalog.RangeConstructorThree {
				if len(n.Args) != 3 {
					return aliasIntervalDatum{}, false, nil
				}
				value, ok := capturedAliasLiteral(n.Args[2])
				if !ok || value.IsNull() {
					return aliasIntervalDatum{}, false, nil
				}
				physical, err := value.BaseValue()
				if err != nil {
					return aliasIntervalDatum{}, false, err
				}
				flags, err = physical.Text()
				if err != nil {
					return aliasIntervalDatum{}, false, err
				}
				if len(flags) != 2 || (flags[0] != '[' && flags[0] != '(') || (flags[1] != ']' && flags[1] != ')') {
					return aliasIntervalDatum{}, false, nil
				}
			}
			r, err := aliasIntervalBounds(next, sqltype.RangeBounds{Lower: lo, Upper: hi, LowerInclusive: flags[0] == '[', UpperInclusive: flags[1] == ']', LowerUnbounded: lo.IsNull(), UpperUnbounded: hi.IsNull()})
			if err != nil {
				return aliasIntervalDatum{}, false, err
			}
			if r.lowerFinite && r.upperFinite {
				if r.lower.OID > r.upper.OID {
					return aliasIntervalDatum{}, false, nil
				} // preserve ordinary invalid-source execution authority
				r.empty = r.lower.OID == r.upper.OID && !(r.lowerInclusive && r.upperInclusive)
			}
			d := aliasIntervalDatum{}
			if !r.empty {
				d.intervals = []aliasInterval{r}
			}
			return d, true, ctx.Err()
		}
		d := aliasIntervalDatum{multi: true}
		switch form {
		case catalog.MultirangeConstructorZero:
			return d, true, ctx.Err()
		case catalog.MultirangeConstructorOne:
			if len(n.Args) != 1 {
				return d, false, nil
			}
			member, known, err := capturedAliasIntervals(ctx, next, n.Args[0])
			if err != nil || !known {
				return d, false, err
			}
			if member.null {
				d.null = true
				return d, true, nil
			}
			if member.multi {
				return d, false, nil
			}
			d.intervals = member.intervals
		case catalog.MultirangeConstructorMany:
			if n.Variadic {
				if len(n.Args) != 1 {
					return d, false, nil
				}
				intervals, null, known, err := capturedAliasArray(ctx, next, n.Args[0])
				if err != nil || !known {
					return d, false, err
				}
				d.intervals, d.null = intervals, null
			} else {
				for _, arg := range n.Args {
					member, known, err := capturedAliasIntervals(ctx, next, arg)
					if err != nil || !known {
						return d, false, err
					}
					// Expanded NULL members are constructor errors, not strict NULL arrays.
					if member.null || member.multi {
						return d, false, nil
					}
					d.intervals = append(d.intervals, member.intervals...)
				}
			}
		default:
			return d, false, nil
		}
		return d, true, ctx.Err()
	}
	return aliasIntervalDatum{}, false, nil
}

func capturedAliasArray(ctx context.Context, next *catalog.State, node expr.Expr) ([]aliasInterval, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, false, err
	}
	switch n := node.(type) {
	case plan.LiteralExpr:
		if n.Value.IsNull() {
			return nil, true, true, nil
		}
		if !n.Value.Type().BaseType().IsArray() {
			return nil, false, false, nil
		}
		var intervals []aliasInterval
		for i := range n.Value.ArrayLen() {
			member, known, err := aliasLiteralIntervals(ctx, next, n.Value.ArrayElement(i))
			if err != nil || !known {
				return nil, false, false, err
			}
			if member.null || member.multi {
				return nil, false, false, nil
			}
			intervals = append(intervals, member.intervals...)
		}
		return intervals, false, true, nil
	case plan.ArrayExpr:
		var intervals []aliasInterval
		for _, element := range n.Elements {
			if element.ResultUsage().Type.BaseType().IsArray() {
				nested, null, known, err := capturedAliasArray(ctx, next, element)
				if err != nil || !known {
					return nil, false, false, err
				}
				if null {
					return nil, false, false, nil
				}
				intervals = append(intervals, nested...)
				continue
			}
			member, known, err := capturedAliasIntervals(ctx, next, element)
			if err != nil || !known {
				return nil, false, false, err
			}
			if member.null || member.multi {
				return nil, false, false, nil
			}
			intervals = append(intervals, member.intervals...)
		}
		return intervals, false, true, nil
	case plan.CoercionExpr:
		if n.Child.ResultUsage().Type.BaseType().ID() == n.ResultUsage().Type.BaseType().ID() {
			return capturedAliasArray(ctx, next, n.Child)
		}
	case plan.CollationExpr:
		return capturedAliasArray(ctx, next, n.Child)
	}
	return nil, false, false, nil
}
