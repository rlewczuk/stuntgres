package analyze

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Capture only consulted scalar pairs, including absence. Domain declarations
// are ignored by selection; their underlying pair owns reanalysis authority.
func (b *Binder) selectCoercion(source, target sqltype.Usage, mode sqltype.CoercionContext) (*catalog.Cast, error) {
	view := b.BindingCatalog()
	selected, err := catalog.SelectCoercion(view, source, target, mode)
	if source != target && !source.Type.IsDomain() && !target.Type.IsDomain() && !b.Params.NoParameters && !b.Consumer.frozenDefinition {
		if b.Prepared.Casts == nil {
			b.Prepared.Casts = make(map[catalog.CastPair]*catalog.Cast)
		}
		pair := catalog.CastPair{Source: source.Type.ID(), Target: target.Type.ID()}
		observed := selected
		if err != nil {
			observed, _ = view.Cast(pair.Source, pair.Target)
		}
		b.Prepared.Casts[pair] = observed
	}
	return selected, err
}

// declaredCoercion constructs only the winning declaration. No routine is bound,
// checked for EXECUTE or evaluated during candidate feasibility.
func (b *Binder) declaredCoercion(child expr.Expr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position, raw parser.Expr) (expr.Expr, bool, error) {
	// Exact identity never selects a declared edge, even if a same-type
	// typmod cast exists. It needs neither a catalog read nor an absence stamp.
	if child.ResultUsage() == target {
		return nil, false, nil
	}
	if child.ResultUsage().Type.IsDomain() || target.Type.IsDomain() {
		baseSource, baseTarget := child.ResultUsage().BaseUsage(), target.BaseUsage()
		if d, e := b.selectCoercion(baseSource, baseTarget, mode); d != nil || e != nil {
			if e != nil {
				return nil, true, e
			}
			base, err := b.CoerceSelected(child, baseSource, mode, pos, nil)
			if err != nil {
				return nil, true, err
			}
			converted, _, err := b.declaredCoercion(base, baseTarget, mode, pos, raw)
			if err != nil {
				return nil, true, err
			}
			result, err := b.CoerceSelected(converted, target, mode, pos, nil)
			return result, true, err
		}
	}
	view := b.BindingCatalog()
	d, err := b.selectCoercion(child.ResultUsage(), target, mode)
	if err != nil {
		return nil, true, err
	}
	if d == nil {
		source := child.ResultUsage()
		if source.Type.IsArray() && target.Type.IsArray() {
			from, to := source.Type.Element().Usage(), target.Type.Element().Usage()
			from.Typmod, to.Typmod = source.Typmod, target.Typmod
			from.Collation, to.Collation = source.Collation, target.Collation
			if err := catalog.CanCoerce(view, from, to, mode); err != nil {
				if mode == sqltype.Explicit {
					return nil, true, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to %s", b.DiagnosticTypeName(source.Type), b.DiagnosticTypeName(target.Type)), pos)
				}
				return nil, true, err
			}
			if selected, selectionErr := catalog.SelectCoercion(view, from.BaseUsage(), to.BaseUsage(), mode); selected != nil || selectionErr != nil {
				leaf := plan.ArrayCastElementExpr{Info: b.Info(from, pos, expr.Properties{})}
				element, e := b.CoerceSelected(leaf, to, mode, pos, nil)
				if e != nil {
					return nil, true, e
				}
				info := b.Info(target, pos, expr.MergeProperties(child.Properties(), element.Properties()))
				return plan.ArrayCastExpr{Info: info, Child: child, Element: element, Mode: mode, Context: selected.Context}, true, nil
			}
		}
		return nil, false, nil
	}
	info, err := b.coercionInfo(b.Info(target, pos, child.Properties()), child)
	if err != nil {
		return nil, true, err
	}
	if d.Method != "f" {
		n, e := plan.NewDatumCast(info, child, d.Method, mode)
		n.Context = d.Context
		return n, true, e
	}
	p, ok := view.Procedure(d.Function)
	if !ok {
		return nil, true, fmt.Errorf("missing cast routine")
	}
	var types []sqltype.Usage
	for _, word := range strings.Split(string(p.Signature), ",") {
		id, e := strconv.ParseUint(word, 10, 32)
		if e != nil {
			return nil, true, e
		}
		typ, ok := view.LookupType(sqltype.ID(id))
		if !ok {
			return nil, true, fmt.Errorf("missing cast formal")
		}
		types = append(types, typ.Usage())
	}
	args := make([]expr.Expr, len(types))
	args[0] = child
	if child.ResultUsage().Type != types[0].Type {
		if child.ResultUsage().Type.IsDomain() && child.ResultUsage().Type.BaseType() == types[0].Type {
			p, e := sqltype.PlanCoercion(child.ResultUsage(), types[0], sqltype.Implicit)
			if e != nil {
				return nil, true, e
			}
			args[0] = plan.CoercionExpr{Info: b.Info(types[0], pos, CoercionProperties(child, p)), Child: child, Plan: p, Mode: sqltype.Implicit}
		} else {
			adapter, e := plan.NewDatumCast(b.Info(types[0], pos, child.Properties()), child, "b", sqltype.Implicit)
			if e != nil {
				return nil, true, e
			}
			adapter.Context = "i"
			args[0] = adapter
		}
	}
	if args[0].ResultUsage() != types[0] {
		// This same-type formal normalization is not another selection of
		// the user cast edge. The routine assembly retains exact usages.
		input := args[0]
		fixed, e := sqltype.PlanCoercion(input.ResultUsage(), types[0], sqltype.Implicit)
		if e != nil {
			return nil, true, e
		}
		args[0] = plan.CoercionExpr{Info: b.Info(types[0], pos, CoercionProperties(input, fixed)), Child: input, Plan: fixed, Mode: sqltype.Implicit}
	}
	rawArgs := []parser.Expr{raw}
	if raw != nil && child.ResultUsage().Type != types[0].Type && !child.ResultUsage().Type.IsDomain() {
		formal := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].Type.ID())})
		schema := parser.Identifier{Text: view.Get(formal.Namespace()).Name()}
		rawArgs[0] = parser.CastExpr{Child: raw, TypeName: parser.TypeName{Name: formal.Name(), Schema: &schema}, Syntax: parser.StandardCast, Pos: pos, FrozenMethod: "b", FrozenContext: "i"}
	}
	if len(types) > 1 {
		v, e := sqltype.Integer(types[1].Type, int64(target.Typmod))
		if e != nil {
			return nil, true, e
		}
		args[1] = plan.LiteralExpr{Info: b.Info(types[1], pos, expr.Properties{}), Value: v}
		rawArgs = append(rawArgs, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: int64(target.Typmod), Pos: pos}})
	}
	if len(types) > 2 {
		v := sqltype.Boolean(types[2].Type, mode == sqltype.Explicit)
		args[2] = plan.LiteralExpr{Info: b.Info(types[2], pos, expr.Properties{}), Value: v}
		rawArgs = append(rawArgs, parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: mode == sqltype.Explicit, Pos: pos}})
	}
	var result expr.Expr
	if p.SQL != nil {
		ref := b.Host.CatalogOwner().Domain.Ref(view.Epoch(), p.Address())
		b.dependency(ref, p.Revision())
		r, _ := view.LookupType(sqltype.ID(p.Result))
		properties := expr.Properties{Volatility: p.Volatility}
		for _, arg := range args {
			properties = expr.MergeProperties(properties, arg.Properties())
		}
		ri := b.Info(r.Usage(), pos, properties)
		ri.RoutineReference = p
		assembly := make([]plan.RoutineInput, len(args))
		for slot := range assembly {
			assembly[slot] = plan.RoutineInput{Start: slot, Count: 1}
		}
		node := plan.SQLRoutineExpr{Info: ri, Procedure: p, Ref: ref, Args: args, WrittenCount: len(args), InputMap: assembly, Signature: proc.InvocationSignature{Inputs: p.SQL.Inputs, Result: ri.Usage}}
		node, err = node.Collate(b.CollatedInfo)
		if err != nil {
			return nil, true, err
		}
		result, err = node.AssembleArguments()
		if err != nil {
			return nil, true, err
		}
	} else {
		result, err = b.BindSelectedOperatorRoutine(p, args, pos)
		if err != nil {
			return nil, true, err
		}
	}
	if raw != nil && (b.Params.NoParameters || b.Consumer.frozenDefinition) {
		ns := view.Get(p.Namespace()).Name()
		frozen := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: ns}, {Text: p.Name()}}}, Args: rawArgs, Pos: pos, StoredSQLRoutine: p.SQL != nil || p.RangeConstructor != nil}
		if b.Stored.Syntax == nil {
			b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
		}
		b.Stored.Syntax[StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: pos}] = frozen
	}
	if result.ResultUsage().Type != target.Type {
		info.Property = result.Properties()
		adapted, e := plan.NewDatumCast(info, result, "b", mode)
		if e != nil {
			return nil, true, e
		}
		adapted.Context = "i"
		result = adapted
	}
	if result.ResultUsage() != target {
		p, e := sqltype.PlanCoercion(result.ResultUsage(), target, mode)
		if e != nil {
			return nil, true, e
		}
		info.Property = CoercionProperties(result, p)
		result = plan.CoercionExpr{Info: info, Child: result, Plan: p, Mode: mode}
	}
	return result, true, nil
}

func (b *Binder) CoerceSelected(node expr.Expr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position, raw parser.Expr) (expr.Expr, error) {
	if selected, handled, err := b.declaredCoercion(node, target, mode, pos, raw); handled {
		return selected, err
	}
	p, err := sqltype.PlanCoercion(node.ResultUsage(), target, mode)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.Info(target, pos, CoercionProperties(node, p)), node)
	if err != nil {
		return nil, err
	}
	// Collation derivation can change the destination Usage (notably NAME's
	// declared C collation). Retain the selected operation, but bind its plan
	// to the same effective usages carried by the executable expression.
	if info.Usage != target {
		p, err = p.Rebind(node.ResultUsage(), info.Usage, mode)
		if err != nil {
			return nil, err
		}
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: p, Mode: mode}, nil
}

func (b *Binder) CanCoerce(source, target sqltype.Usage, mode sqltype.CoercionContext) bool {
	return catalog.CanCoerce(b.BindingCatalog(), source, target, mode) == nil
}
