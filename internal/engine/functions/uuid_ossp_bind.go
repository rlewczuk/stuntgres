package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindUUIDOssp owns labels and decorations before the shared gates. Installed
// procedure authority remains with the engine, not the shared UUID kernel.
func bindUUIDOssp(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []Argument
	if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	names := installedArgumentNames(b, raw, operands)
	member, selected := UUIDOsspLookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	routine, err := b.InstalledRoutine(raw.Name, UUIDOsspExtension, member.Signature)
	if err != nil {
		return nil, err
	}
	count := 0
	if member.Signature != "" {
		count = 2
	}
	if !selected || routine == nil || len(operands) != count || raw.Variadic || raw.Star {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var formal [2]Argument
	var seen [2]bool
	for i, o := range operands {
		slot := i
		if i < len(raw.ArgNames) && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, name := range member.ArgumentNames {
				if name == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
		}
		if slot < 0 || slot >= count || seen[slot] {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
		seen[slot], formal[slot] = true, o
	}
	if count == 0 && len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var args []expr.Expr
	properties := expr.Properties{Volatility: member.Volatility}
	if count != 0 {
		targets := [2]sqltype.Usage{b.Type(sqltype.UUID).Usage(), b.Type(sqltype.Text).Usage()}
		for i, o := range formal {
			if o.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(o.Usage, targets[i], sqltype.Implicit); err != nil {
					return nil, b.SignatureError(raw.Name, raw.Pos, names)
				}
			}
		}
		args = make([]expr.Expr, 2)
		// Coerce in lexical order to preserve literal-error ordering and positions
		// even when named operands select the reverse formal order.
		for i := range operands {
			slot := i
			if i < len(raw.ArgNames) && raw.ArgNames[i].Text != "" {
				for j, name := range member.ArgumentNames {
					if name == raw.ArgNames[i].Text {
						slot = j
						break
					}
				}
			}
			args[slot], err = b.CoerceArgument(operands[i], CoercionRequest{Intent: DirectFormalArgument, Target: targets[slot], AtRaw: true})
			if err != nil {
				return nil, err
			}
			properties = expr.MergeProperties(properties, args[slot].Properties())
		}
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	call, err := NewUUIDGeneration(b.Info(b.Type(sqltype.UUID).Usage(), raw.Pos, properties), member.Selector, args, nil)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
}
