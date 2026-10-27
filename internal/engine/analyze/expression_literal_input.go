package analyze

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ExpressionName derives the original target label from typed syntax and its
// bound expression without rebinding or consulting current catalog names.
// Strong column/function names survive standard casts; weak literal names do not.
func ExpressionName(raw parser.Expr, usage sqltype.Usage, bound expr.Expr) (string, bool) {
	for {
		coercion, ok := bound.(plan.CoercionExpr)
		if !ok {
			break
		}
		bound = coercion.Child
	}
	switch r := raw.(type) {
	case parser.RowExpr:
		return "row", false
	case parser.FieldExpr:
		return r.Field.Text, true
	case parser.StarExpr:
		if len(r.Qualifier.Parts) != 0 {
			return r.Qualifier.Parts[len(r.Qualifier.Parts)-1].Text, true
		}
		return "?column?", false
	case parser.SubqueryExpr:
		if r.Kind == parser.SubqueryExists {
			return "exists", true
		}
		if r.Kind == parser.SubqueryScalar {
			if child, ok := bound.(plan.SubqueryExpr); ok && len(child.QueryPlan.Root.Schema()) == 1 {
				name := child.QueryPlan.Root.Schema()[0].Name
				return name, name != "?column?" || len(r.Query.Labels) > 0 && r.Query.Labels[0].Text != ""
			}
		}
		return "?column?", false
	case parser.IdentityExpr:
		return r.Name, true
	case parser.CurrentDateTimeExpr:
		return r.Kind.Name(), true
	case parser.GroupingExpr:
		return "grouping", true
	case parser.AggregateExpr:
		return r.Name, true
	case parser.ConditionalExpr:
		return conditionalName(r), true
	case parser.CaseExpr:
		if r.Else != nil {
			var otherwise expr.Expr
			if expr, ok := bound.(plan.CaseExpr); ok {
				otherwise = expr.Otherwise
			}
			if name, strong := ExpressionName(r.Else, usage, otherwise); strong {
				return name, true
			}
		}
		return "case", false
	case parser.ColumnExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.MergeActionExpr:
		return "merge_action", true
	case parser.SQLJSONQueryExpr:
		return r.Kind, true
	case parser.SQLJSONExpr:
		return r.Kind, true
	case parser.JSONPredicateExpr:
		return "?column?", false
	case parser.FunctionExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.CallExpr:
		return r.Name.Parts[len(r.Name.Parts)-1].Text, true
	case parser.RegclassExpr:
		if name, strong := ExpressionName(r.Child, usage, bound); strong {
			return name, true
		}
		return "regclass", false
	case parser.ArrayExpr:
		return "array", false
	case parser.SubscriptExpr:
		return ExpressionName(r.Base, usage, bound)
	case parser.TimeZoneExpr:
		return "timezone", true
	case parser.CollateExpr:
		if expr, ok := bound.(plan.CollationExpr); ok {
			bound = expr.Child
		}
		return ExpressionName(r.Child, usage, bound)
	case parser.CastExpr:
		if r.Syntax == parser.FunctionCast {
			return r.Callee, true
		}
		if name, strong := ExpressionName(r.Child, usage, bound); strong {
			return name, true
		}
		if usage.Type == nil {
			// UNKNOWN-preserving casts can precede parameter finalization.
			return r.TypeName.Name, false
		}
		return usage.Type.Name(), false
	case parser.TypedLiteralExpr:
		return usage.Type.Name(), false
	default:
		return "?column?", false
	}
}
func literalInputError(err error, raw parser.LiteralExpr) error {
	return InputErrorAt(err, raw.Value.Pos)
}

// Full analysis invalidation recollects temporal scalar/array captures while the
// candidate still supplies fixed signatures and immutable native logical values.
func (b *Binder) collectTemporalConstants() bool {
	if !b.Expected.Held || b.Expected.RefreshTemporals {
		return true
	}
	owner, generation, ok := b.Host.PlanOwner()
	return ok && b.Expected.PlanOwner == owner && b.Expected.PlanGeneration != generation
}

// Explicit typed literals use this only to discover their frozen modifier
// before resolving the raw declaration again. Scalar reconstruction must use
// captured with the complete contextual usage.
func (b *Binder) CaptureAt(offset int) (TemporalConstant, bool, error) {
	if b.Expected.Held {
		captures := b.Expected.Temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].Offset >= offset })
		if i < len(captures) && captures[i].Offset == offset {
			if b.Expected.RefreshTemporals && RefreshableTemporal(captures[i]) {
				return TemporalConstant{}, false, nil
			}
			saved := captures[i]
			for i++; i < len(captures) && captures[i].Offset == offset; i++ {
				if captures[i].TypeID != saved.TypeID || captures[i].Typmod != saved.Typmod ||
					captures[i].Typed != saved.Typed || captures[i].TypedTypmod != saved.TypedTypmod {
					return TemporalConstant{}, false, fmt.Errorf("stuntgres: ambiguous captured temporal metadata")
				}
			}
			return saved, true, nil
		}
	}
	return TemporalConstant{}, false, nil
}

func (b *Binder) Captured(offset int, usage sqltype.Usage) (TemporalConstant, bool, error) {
	if b.Expected.Held {
		captures := b.Expected.Temporals
		i := sort.Search(len(captures), func(i int) bool { return captures[i].Offset >= offset })
		if i < len(captures) && captures[i].Offset == offset {
			if b.Expected.RefreshTemporals && RefreshableTemporal(captures[i]) {
				return TemporalConstant{}, false, nil
			}
			for ; i < len(captures) && captures[i].Offset == offset; i++ {
				capture := captures[i]
				if capture.TypeID == usage.Type.ID() && capture.Typmod == usage.Typmod {
					return capture, true, nil
				}
			}
			return TemporalConstant{}, false, fmt.Errorf("stuntgres: captured temporal usage changed")
		}
	}
	return TemporalConstant{}, false, nil
}
func (b *Binder) InputConstant(text string, u sqltype.Usage, pos parser.Position) (sqltype.Value, error) {
	if err := b.CaptureTypeDiagnosticInput(u.Type); err != nil {
		return sqltype.Value{}, err
	}
	if u.Type.ContainsRegObject() {
		return b.regObjectConstant(text, u, pos)
	}
	if u.Type.ContainsRegproc() {
		return b.RoutineConstant(text, u, pos)
	}
	if u.Type.ID() == sqltype.Regtype {
		return b.RegtypeConstant(text, pos)
	}
	if u.Type.ContainsRegTS() {
		return b.RegTSConstant(text, u, pos)
	}
	if sqltype.IsOperatorAlias(u.Type.ID()) {
		return b.OperatorConstant(text, u.Type, pos)
	}
	if u.Type.ContainsOperatorAlias() {
		return b.operatorCompoundConstant(text, u, pos)
	}
	if u.Type.ID() == sqltype.Regclass {
		node, _, err := b.bindRegclass(parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text, Pos: pos}})
		if err != nil {
			return sqltype.Value{}, err
		}
		return node.(plan.LiteralExpr).Value, nil
	}
	if u.Type.BaseType().ID() == sqltype.Polygon || u.Type.BaseType().ID() == sqltype.Circle || sqltype.IsFTS(u.Type.BaseType().ID()) || u.Type.BaseType().IsHstore() || u.Type.BaseType().IsCube() || u.Type.BaseType().IsSeg() || u.Type.BaseType().IsQueryInt() || u.Type.BaseType().IsLquery() || u.Type.BaseType().IsLtree() || u.Type.BaseType().IsCitext() || u.Type.BaseType().IsEan13() || u.Type.BaseType().IsIsbn() || u.Type.BaseType().IsIsbn13() || u.Type.BaseType().IsIsmn() || u.Type.BaseType().IsIsmn13() || u.Type.BaseType().IsIssn() || u.Type.BaseType().IsIssn13() || u.Type.BaseType().IsUpc() || u.Type.ContainsACLItem() || u.Type.ContainsRegrole() || u.Type.BaseType().IsEnum() || u.Type.BaseType().IsArray() || u.Type.BaseType().IsComposite() || u.Type.BaseType().IsRange() || u.Type.BaseType().IsMultirange() {
		if u.Type.BaseType().IsArray() && u.Type.ContainsRegtype() {
			return b.regtypeArrayConstant(text, u, pos)
		}
		c, found, err := b.Captured(pos.Offset, u)
		if err != nil {
			return sqltype.Value{}, err
		}
		if found {
			catalog := b.BindingCatalog()
			if b.Expected.CatalogOwner != catalog.Owner() || b.Expected.CatalogEpoch != catalog.Epoch() {
				// Fence held catalog-owned results/parameters before touching a
				// captured datum from the retired lifetime. Builtin signatures
				// may rebind their written literals, never retag foreign payloads.
				if err := b.Expected.CheckLifetime(catalog.Owner(), catalog.Epoch()); err != nil {
					if hasEnumInput(u.Type) {
						// Enum and constrained-domain inputs retain their original
						// concrete error and literal-position provenance.
						return sqltype.Value{}, err
					}
					return sqltype.Value{}, literalLifetimeError{cause: err}
				}
				if hasEnumInput(u.Type) {
					return sqltype.Value{}, ids.ErrCatalogReplaced
				}
				if c.Value.Type() == u.Type {
					return c.Value, nil
				}
				if b.Expected.CatalogOwner == nil || !b.Expected.RefreshTemporals {
					// Only a full, owner-proven reanalysis may replace captured
					// input. A structural replay keeps the existing retag gate.
					return c.Value.Retag(u.Type)
				}
			} else {
				return c.Value.Retag(u.Type)
			}
		}
		if u.Type.ContainsCatalogIdentifier() {
			if err := b.captureElementInput(); err != nil {
				return sqltype.Value{}, err
			}
		}
		value, err := b.decodeLiteralInput(text, u.Type, -1, b.Input.LiteralInput(pos.Character))
		if err != nil {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && pos.Character > 0 {
				copy := *diagnostic
				copy.Position = strconv.Itoa(pos.Character)
				return sqltype.Value{}, sqlstate.InputDiagnostic(err, &copy)
			}
			return sqltype.Value{}, err
		}
		if b.collectTemporalConstants() {
			i := sort.Search(len(b.Prepared.Temporals), func(i int) bool { return b.Prepared.Temporals[i].Offset >= pos.Offset })
			existing := -1
			for j := i; j < len(b.Prepared.Temporals) && b.Prepared.Temporals[j].Offset == pos.Offset; j++ {
				if b.Prepared.Temporals[j].TypeID == u.Type.ID() && b.Prepared.Temporals[j].Typmod == u.Typmod {
					existing = j
					break
				}
			}
			if existing >= 0 {
				b.Prepared.Temporals[existing].Value = value
			} else {
				b.Prepared.Temporals = append(b.Prepared.Temporals, TemporalConstant{})
				copy(b.Prepared.Temporals[i+1:], b.Prepared.Temporals[i:])
				b.Prepared.Temporals[i] = TemporalConstant{Offset: pos.Offset, Typmod: u.Typmod, TypeID: u.Type.ID(), Value: value}
			}
		}
		return value, nil
	}
	if u.Type.ID() != sqltype.Interval && u.Type.ID() != sqltype.Timestamp && u.Type.ID() != sqltype.Time && u.Type.ID() != sqltype.Date && u.Type.ID() != sqltype.Timestamptz && u.Type.ID() != sqltype.Timetz {
		return b.decodeLiteralInput(text, u.Type, -1, b.Input)
	}
	c, found, err := b.Captured(pos.Offset, u)
	if err != nil {
		return sqltype.Value{}, err
	}
	if found {
		if c.TypeID == sqltype.Timetz {
			return sqltype.TimetzValue(u.Type, c.Scalar, c.West)
		}
		if c.TypeID == sqltype.Interval {
			return sqltype.IntervalValue(u.Type, c.Months, c.Days, c.Scalar)
		}
		if c.TypeID == sqltype.Time {
			return sqltype.TimeValue(u.Type, c.Scalar)
		}
		if c.TypeID == sqltype.Date {
			return sqltype.DateValue(u.Type, c.Scalar)
		}
		return sqltype.TimestampValue(u.Type, c.Scalar)
	}
	inputTypmod := int32(-1)
	if u.Type.ID() == sqltype.Interval {
		inputTypmod = u.Typmod
	}
	v, err := b.decodeLiteralInput(text, u.Type, inputTypmod, b.Input)
	if err == nil && u.Typmod >= 0 {
		var p sqltype.CoercionPlan
		p, err = sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Assignment)
		if err == nil {
			v, err = p.Apply(b.Ctx, v, b.Input, sqltype.OutputEnvironment{Zone: b.Input.Zone, DateStyle: b.Input.DateStyle, IntervalStyle: b.Input.IntervalStyle})
		}
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if b.collectTemporalConstants() {
		var n int64
		var months, days int32
		var err error
		if u.Type.ID() == sqltype.Interval {
			months, days, n, err = v.Interval()
		} else if u.Type.ID() == sqltype.Time || u.Type.ID() == sqltype.Timetz {
			n, err = v.Time()
		} else if u.Type.ID() == sqltype.Date {
			n, err = v.Date()
		} else {
			n, err = v.Timestamp()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		// Symmetric comparison can contextualize an earlier unknown literal
		// after its right operand. Keep captures ordered for rebind lookup.
		i := sort.Search(len(b.Prepared.Temporals), func(i int) bool { return b.Prepared.Temporals[i].Offset >= pos.Offset })
		b.Prepared.Temporals = append(b.Prepared.Temporals, TemporalConstant{})
		copy(b.Prepared.Temporals[i+1:], b.Prepared.Temporals[i:])
		west := int32(0)
		if u.Type.ID() == sqltype.Timetz {
			west, err = v.TimeZoneOffset()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		b.Prepared.Temporals[i] = TemporalConstant{Offset: pos.Offset, Scalar: n, Typmod: u.Typmod, TypeID: u.Type.ID(), West: west, Months: months, Days: days}
	}
	return v, nil
}

func (b *Binder) captureElementInput() error {
	if b.Input.Regclass != nil && b.Input.Regtype != nil && b.Input.Regrole != nil && b.Input.Operator != nil && b.Input.Regproc != nil && b.Input.Regcollation != nil && b.Input.Regnamespace != nil && b.Input.RegTS != nil {
		return nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return err
	}
	return b.Host.CaptureElementInput(auth, &b.Input)
}
