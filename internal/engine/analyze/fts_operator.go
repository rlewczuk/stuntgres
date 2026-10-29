package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) ftsOperator(raw parser.OperatorExpr, args []expr.Expr) (expr.Expr, bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, false, nil
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var op sqltype.FTSOperator
	if name == "@@" && len(args) == 2 {
		left, right := args[0].ResultUsage().Type.ID(), args[1].ResultUsage().Type.ID()
		switch {
		case left == sqltype.TSVector && right == sqltype.TSQuery:
			op = sqltype.FTSMatchVQ
		case left == sqltype.TSQuery && right == sqltype.TSVector:
			op = sqltype.FTSMatchQV
		case left == sqltype.Text && right == sqltype.Text:
			op = sqltype.FTSMatchTT
		case left == sqltype.Text && right == sqltype.TSQuery:
			op = sqltype.FTSMatchTQ
		default:
			return nil, false, nil
		}
	} else {
		vector := args[0].ResultUsage().Type.ID() == sqltype.TSVector
		if !vector && args[0].ResultUsage().Type.ID() != sqltype.TSQuery {
			return nil, false, nil
		}
		if len(args) == 2 && args[1].ResultUsage().Type != args[0].ResultUsage().Type {
			return nil, false, nil
		}
		var ok bool
		op, ok = sqltype.FTSKind(name, len(args), vector)
		if !ok || vector && op != sqltype.FTSConcat {
			return nil, false, nil
		}
	}
	var children [3]expr.Expr
	copy(children[:], args)
	properties := expr.Properties{Volatility: expr.Immutable}
	if op == sqltype.FTSMatchTT || op == sqltype.FTSMatchTQ {
		properties.Volatility = expr.Stable
	}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	left, right := sqltype.ID(0), op.Formal(0)
	if len(args) == 2 {
		left, right = op.Formal(0), op.Formal(1)
	}
	info, err := b.builtinOperatorRoutineInfo(b.Info(b.Type(op.ResultID()).Usage(), raw.Pos, properties), name, left, right)
	if err != nil {
		return nil, true, err
	}
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	node, err := functions.NewFTS(info, op, children, namespace, false)
	return node, true, err
}
