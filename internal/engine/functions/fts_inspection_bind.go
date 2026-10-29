package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func namedFTSInspection(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	n := FTSInspection{Stat: raw.Name.Parts[len(raw.Name.Parts)-1].Text == "ts_stat", Count: len(declared)}
	if n.Count < 1 || n.Count > 2 || raw.Variadic {
		return nil, nil, false
	}
	for i, d := range declared {
		if d.Type == nil || d.Type.ID() != n.formal(i) {
			return nil, nil, false
		}
	}
	return fixedNamedInputs(b, raw, args, declared, n.labels(), n.Count)
}
func bindFTSInspection(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	n := FTSInspection{Stat: raw.Name.Parts[len(raw.Name.Parts)-1].Text == "ts_stat", Count: len(raw.Args)}
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		a, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = a
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	valid := visible && !raw.Variadic && !raw.ResolutionOnly && n.Count >= 1 && n.Count <= 2
	var slots [7]int
	if valid {
		slots, valid = namedSlots(raw, n.labels(), n.Count)
	}
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, failure := sqltype.PlanCoercion(arg.Usage, b.Type(n.formal(slots[i])).Usage(), sqltype.Implicit); failure != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", n.name()+"(*) specified, but "+n.name()+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: n.IntrinsicVolatility()}
	formals := make([]sqltype.ID, n.Count)
	for i, arg := range operands {
		n.Order[i] = slots[i]
		formals[slots[i]] = n.formal(slots[i])
		n.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(n.formal(slots[i])).Usage(), Label: n.name()})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, n.Args[i].Properties())
	}
	names, ids := inspectionOutputs(n.Stat)
	members := make([]sqltype.CompositeMember, len(names))
	for i, name := range names {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(ids[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	n.Info, err = b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, properties), FTSInspectionRoutineProvider, n.name(), formals...)
	if err != nil {
		return nil, err
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return n, n.Validate()
}
