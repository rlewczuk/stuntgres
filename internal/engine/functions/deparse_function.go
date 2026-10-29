package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type DeparseSignature uint8

const (
	DeparseConstraint DeparseSignature = iota
	DeparseConstraintPretty
	DeparseExpr
	DeparseExprPretty
	DeparseIndex
	DeparseIndexColumn
	DeparseViewText
	DeparseViewOID
	DeparseViewTextPretty
	DeparseViewOIDPretty
	DeparseViewWrap
	DeparseSerialSequence
	DeparseTrigger
	DeparseTriggerPretty
	DeparseRule
	DeparseRulePretty
	DeparseFunction
	DeparseFunctionArguments
	DeparseFunctionIdentityArguments
	DeparseFunctionResult
)

type deparseSignatureDefinition struct {
	name string
	Args []sqltype.ID
}

var deparseSignatures = [...]deparseSignatureDefinition{
	{"pg_get_constraintdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_constraintdef", []sqltype.ID{sqltype.Oid, sqltype.Bool}},
	{"pg_get_expr", []sqltype.ID{sqltype.PgNodeTree, sqltype.Oid}},
	{"pg_get_expr", []sqltype.ID{sqltype.PgNodeTree, sqltype.Oid, sqltype.Bool}},
	{"pg_get_indexdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_indexdef", []sqltype.ID{sqltype.Oid, sqltype.Int4, sqltype.Bool}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Text}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Text, sqltype.Bool}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Oid, sqltype.Bool}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Oid, sqltype.Int4}},
	{"pg_get_serial_sequence", []sqltype.ID{sqltype.Text, sqltype.Text}},
	{"pg_get_triggerdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_triggerdef", []sqltype.ID{sqltype.Oid, sqltype.Bool}},
	{"pg_get_ruledef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_ruledef", []sqltype.ID{sqltype.Oid, sqltype.Bool}},
	{"pg_get_functiondef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_function_arguments", []sqltype.ID{sqltype.Oid}},
	{"pg_get_function_identity_arguments", []sqltype.ID{sqltype.Oid}},
	{"pg_get_function_result", []sqltype.ID{sqltype.Oid}},
}

type Deparse struct {
	expr.Info
	Signature DeparseSignature
	Args      []expr.Expr
}

func (Deparse) ResultNullability(site expr.ResultSite) (nullable, known bool) {
	if site == expr.ResultSelect {
		return true, true
	}
	return false, false
}

func bindDeparse(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	var args []expr.Expr
	var unknown []bool
	if len(raw.Args) <= 100 {
		args = make([]expr.Expr, len(raw.Args))
		unknown = make([]bool, len(raw.Args))
	}
	for i, child := range raw.Args {
		if literal, ok := child.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			if args != nil {
				unknown[i] = true
				args[i] = nil
			}
			continue
		}
		analyzed, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: BareProjectionArgument})
		arg := analyzed.Expression
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = arg
			unknown[i] = arg.ResultUsage().Type == nil
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	var candidates [len(deparseSignatures)]DeparseSignature
	count, best := 0, -1
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	for index, signature := range deparseSignatures {
		if !visible || raw.Variadic || signature.name != name || len(signature.Args) != len(args) {
			continue
		}
		score, viable := 0, true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			if _, err := sqltype.PlanCoercion(arg.ResultUsage(), b.Type(signature.Args[i]).Usage(), sqltype.Implicit); err != nil {
				viable = false
				break
			}
			if arg.ResultUsage().Type.BaseType().ID() == signature.Args[i] {
				score++
			}
		}
		if !viable || score < best {
			continue
		}
		if score > best {
			count, best = 0, score
		}
		candidates[count] = DeparseSignature(index)
		count++
	}
	// Unknown category selection is all-or-nothing: a conflicting BOOL/numeric
	// slot prevents the string bias in another slot from resolving ambiguity.
	if count > 1 {
		var categories [3]sqltype.ID
		consistent := true
		for i, u := range unknown {
			if !u {
				continue
			}
			for _, candidate := range candidates[:count] {
				id := deparseSignatures[candidate].Args[i]
				if id == sqltype.Text {
					categories[i] = sqltype.Text
					break
				}
				if categories[i] == 0 {
					categories[i] = id
				} else if categories[i] != id {
					consistent = false
				}
			}
		}
		if consistent {
			n := 0
			for _, candidate := range candidates[:count] {
				keep := true
				for i, u := range unknown {
					if u && deparseSignatures[candidate].Args[i] != categories[i] {
						keep = false
					}
				}
				if keep {
					candidates[n] = candidate
					n++
				}
			}
			count = n
		}
	}
	if count > 1 {
		var known sqltype.ID
		homogeneous := true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.BaseType().ID()
			if known != 0 && known != id {
				homogeneous = false
			}
			known = id
		}
		if homogeneous && known != 0 {
			n := 0
			for _, candidate := range candidates[:count] {
				keep := true
				for i, u := range unknown {
					if u {
						if _, err := sqltype.PlanCoercion(b.Type(known).Usage(), b.Type(deparseSignatures[candidate].Args[i]).Usage(), sqltype.Implicit); err != nil {
							keep = false
						}
					}
				}
				if keep {
					candidates[n] = candidate
					n++
				}
			}
			if n > 0 {
				count = n
			}
		}
	}
	if count != 1 {
		return nil, DirectResolutionError(b, raw, args, unknown, count > 1)
	}
	signature := candidates[0]
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range args {
		u := b.Type(deparseSignatures[signature].Args[i]).Usage()
		if unknown[i] {
			var err error
			args[i], err = b.CoerceArgument(Argument{Raw: raw.Args[i]}, CoercionRequest{Intent: DirectFormalArgument, Target: u})
			if err != nil {
				return nil, err
			}
		} else if arg.ResultUsage() != u {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), u, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			args[i], err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: u, Plan: plan, Planned: true, PreserveProperties: true})
			if err != nil {
				return nil, err
			}
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, deparseSignatures[signature].Args...)
	if err != nil {
		return nil, err
	}
	return Deparse{info, signature, args}, nil
}
func (e Deparse) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := ctx
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := e.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != arg.ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid deparse argument")
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	runtime, err := catalogInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.DeparseInquiry(e.Signature, values, e.Usage.Type)
}
func (e Deparse) Validate() error {
	if int(e.Signature) >= len(deparseSignatures) || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid stored deparse signature")
	}
	definition := deparseSignatures[e.Signature]
	if len(e.Args) != len(definition.Args) {
		return fmt.Errorf("stuntgres: invalid stored deparse signature")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range e.Args {
		id := definition.Args[i]
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid stored deparse argument")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if e.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored deparse properties")
	}
	return nil
}

// PrettyIndex names the optional BOOL slot without duplicating signature tables
// in the renderer runtime. It is used only after the node's signature validates.
func (s DeparseSignature) PrettyIndex() int {
	args := deparseSignatures[s].Args
	if args[len(args)-1] == sqltype.Bool {
		return len(args) - 1
	}
	return -1
}
func (s DeparseSignature) Name() string { return deparseSignatures[s].name }

const DeparseSignatureCount = len(deparseSignatures)
