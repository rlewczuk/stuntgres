package analyze

import (
	"errors"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindRegclass(raw parser.Expr) (expr.Expr, ids.ObjectRef, error) {
	if err := qualifiedCastChild(raw); err != nil {
		return nil, ids.ObjectRef{}, err
	}
	if cast, ok := raw.(parser.RegclassExpr); ok {
		return b.bindRegclass(cast.Child)
	}
	typ := b.Type(sqltype.Regclass)
	if literal, ok := raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.StringValue {
		ref := ids.ObjectRef{}
		found := false
		var temporary ids.SessionToken
		auth, err := b.BindingAuthorization()
		if err != nil {
			return nil, ref, err
		}
		if b.Expected.Sequences != nil {
			for _, constant := range b.Expected.Sequences.Constants {
				if constant.Offset == literal.Value.Pos.Offset {
					ref, temporary, found = constant.Ref, constant.Temporary, true
					break
				}
			}
		}
		if !found {
			// Resolve only uncaptured literals; held constants retain their exact
			// provider identity rather than following a later name replacement.
			ref, err = b.Host.Regclass(auth, literal.Value.Text)
			if err != nil {
				return nil, ref, SequenceInputError(err, literal.Value.Pos, "", 0)
			}
			if d, ok := b.authorityRelation(auth, ref.Address); ok {
				if ns, ok := b.authorityNamespace(auth, d.Namespace()); ok && ns.Temporary == bindingSessionToken(b.Host) {
					temporary = ns.Temporary
				}
			}
		}
		var stamp ids.DefinitionRevision
		// Capture the provider stamp from the same authority as its name and
		// temporary lifetime, never from a different published catalog.
		d, represented := b.authorityRelation(auth, ref.Address)
		if represented {
			stamp = b.Host.DefinitionStamp(auth, d)
		}
		b.Prepared.SequenceConstants = append(b.Prepared.SequenceConstants, sequence.Constant{Offset: literal.Value.Pos.Offset, Ref: ref, Temporary: temporary, Stamp: stamp, Represented: represented})
		text := strconv.FormatUint(uint64(ref.Address.ObjectID), 10)
		info := b.Info(typ.Usage(), literal.Value.Pos, expr.Properties{})
		if _, ok := b.authorityRelation(auth, ref.Address); ok {
			info.RegclassRef = ref
		}
		return plan.LiteralExpr{Info: info, Value: sqltype.String(typ, text)}, ref, nil
	}
	// This legacy provider also owns explicit REGCLASS annotations and sequence
	// inputs. New contextual inquiry coercions must not widen its public boundary.
	node, err := b.Bind(raw, ExpressionPolicy{Context: FunctionArgumentContext, Expected: typ.Usage(), Mode: sqltype.Implicit, deferAssignment: true})
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	if node.ResultUsage().BaseUsage().Type != typ {
		return nil, ids.ObjectRef{}, sqlError("42804", "incompatible source and destination types")
	}
	if node.ResultUsage() == typ.Usage() {
		return node, ids.ObjectRef{}, nil
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), typ.Usage(), sqltype.Implicit)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	info, err := b.CollatedInfo(b.Info(typ.Usage(), raw.SourcePosition(), CoercionProperties(node, coercionPlan)), node)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, ids.ObjectRef{}, nil
}

// Explicit casts share name input with sequence consumers but use the ordinary
// coercion matrix for typed sources. Keep coercion nodes: they are not stored
// regclass input constants and must not acquire relation dependencies.
func (b *Binder) bindRegclassCast(raw parser.Expr, pos parser.Position) (expr.Expr, error) {
	if err := qualifiedCastChild(raw); err != nil {
		return nil, err
	}
	if literal, ok := raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.StringValue {
		node, _, err := b.bindRegclass(raw)
		return node, err
	}
	if err := b.captureElementInput(); err != nil {
		return nil, err
	}
	target := b.Type(sqltype.Regclass).Usage()
	child, err := b.Bind(raw, ExpressionPolicy{Context: ProjectionContext, Expected: target, deferAssignment: true})
	if err != nil {
		return nil, err
	}
	coercionPlan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Explicit)
	if err != nil {
		return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to regclass", b.DiagnosticTypeName(child.ResultUsage().Type)), pos)
	}
	if child.ResultUsage() == target {
		return child, nil
	}
	return plan.CoercionExpr{Info: b.Info(target, raw.SourcePosition(), CoercionProperties(child, coercionPlan)), Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}, nil
}
func SequenceInputError(err error, pos parser.Position, context string, parameter int) error {
	var d *sqlstate.Error
	if errors.As(err, &d) {
		copy := *d
		copy.Position = ""
		if parameter != 0 {
			copy.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", parameter)
		} else if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return sqlstate.InputDiagnostic(err, &copy)
	}
	return err
}
