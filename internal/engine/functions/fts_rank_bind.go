package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// selectFTSForm applies ordinary exact/implicit, preferred-category and unknown
// rules to this family's fixed real signatures. Three arguments are not a form.
func selectFTSForm(b Binder, raw parser.FunctionExpr, forms [4][4]sqltype.ID) ([]Argument, int, error) {
	var small [4]Argument
	operands := small[:0]
	if len(raw.Args) <= 4 {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: CallableArgument})
		if err != nil {
			return nil, -1, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, -1, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, -1, err
	}
	var viable [4]bool
	bestExact, bestPreferred := -1, -1
	if visible && len(raw.ArgNames) == 0 && !raw.Variadic && len(operands) <= 4 {
		for j, form := range forms {
			count := 0
			for _, id := range form {
				if id != 0 {
					count++
				}
			}
			if count != len(operands) {
				continue
			}
			ok, exact, preferred := true, 0, 0
			for i, arg := range operands {
				if arg.Usage.Type == nil {
					continue
				}
				source, target := arg.Usage.Type.BaseType(), b.Type(form[i])
				if source.ID() == target.ID() {
					exact++
					continue
				}
				if _, err := sqltype.PlanCoercion(arg.Usage, target.Usage(), sqltype.Implicit); err != nil {
					ok = false
					break
				}
				sf, _ := source.PGTypeMetadata()
				tf, _ := target.PGTypeMetadata()
				if sf.Category == tf.Category && tf.Preferred {
					preferred++
				}
			}
			if !ok {
				continue
			}
			if exact > bestExact || exact == bestExact && preferred > bestPreferred {
				viable = [4]bool{}
				bestExact, bestPreferred = exact, preferred
			}
			if exact == bestExact && preferred == bestPreferred {
				viable[j] = true
			}
		}
	}
	count, selected := 0, -1
	for j, ok := range viable {
		if ok {
			count++
			selected = j
		}
	}
	if count > 1 {
		// Resolve every unknown slot against the same candidate set before
		// pruning any candidate. Sequential pruning changes later categories.
		var categories [4]string
		var preferred [4]bool
		resolved := false
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				continue
			}
			resolved = true
			conflict := false
			for j, form := range forms {
				if !viable[j] {
					continue
				}
				facts, _ := b.Type(form[i]).PGTypeMetadata()
				switch {
				case categories[i] == "":
					categories[i], preferred[i] = facts.Category, facts.Preferred
				case categories[i] == facts.Category:
					preferred[i] = preferred[i] || facts.Preferred
				case facts.Category == "S":
					categories[i], preferred[i] = facts.Category, facts.Preferred
				default:
					conflict = true
				}
			}
			if conflict && categories[i] != "S" {
				resolved = false
				break
			}
		}
		if resolved {
			filtered := viable
			matches, choice := 0, -1
			for j, form := range forms {
				if !viable[j] {
					continue
				}
				for i, arg := range operands {
					if arg.Usage.Type != nil {
						continue
					}
					facts, _ := b.Type(form[i]).PGTypeMetadata()
					if facts.Category != categories[i] || preferred[i] && !facts.Preferred {
						filtered[j] = false
						break
					}
				}
				if filtered[j] {
					matches++
					choice = j
				}
			}
			// Released resolution keeps the original set if all candidates
			// were removed, then tries the homogeneous-known-type heuristic.
			if matches > 0 {
				viable, count, selected = filtered, matches, choice
			}
		}
	}
	if count > 1 {
		known := sqltype.ID(0)
		for _, arg := range operands {
			if arg.Usage.Type != nil {
				id := arg.Usage.Type.BaseType().ID()
				if known != 0 && known != id {
					known = 0
					break
				}
				known = id
			}
		}
		if known != 0 {
			matches, choice := 0, -1
			for j, form := range forms {
				if !viable[j] {
					continue
				}
				ok := true
				for i, arg := range operands {
					if arg.Usage.Type == nil {
						if _, err := sqltype.PlanCoercion(b.Type(known).Usage(), b.Type(form[i]).Usage(), sqltype.Implicit); err != nil {
							ok = false
							break
						}
					}
				}
				if ok {
					matches++
					choice = j
				}
			}
			if matches == 1 {
				count, selected = 1, choice
			}
		}
	}
	if count != 1 {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, -1, ResolutionError(raw.Name, raw.Pos, names, count > 1)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, -1, err
	}
	return operands, selected, nil
}

func bindFTSRank(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	forms := [4][4]sqltype.ID{{sqltype.TSVector, sqltype.TSQuery}, {sqltype.TSVector, sqltype.TSQuery, sqltype.Int4}, {1021, sqltype.TSVector, sqltype.TSQuery}, {1021, sqltype.TSVector, sqltype.TSQuery, sqltype.Int4}}
	operands, selected, err := selectFTSForm(b, raw, forms)
	if err != nil {
		return nil, err
	}
	op := sqltype.FTSRankFunction{Density: raw.Name.Parts[len(raw.Name.Parts)-1].Text == "ts_rank_cd", Weighted: selected >= 2, Normalized: selected == 1 || selected == 3}
	var args [4]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(forms[selected][i]).Usage(), Label: op.Name()})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Float4).Usage(), raw.Pos, properties), FTSRankRoutineProvider, op.Name(), forms[selected][:op.Arity()]...)
	if err != nil {
		return nil, err
	}
	return NewFTSRank(info, op, args)
}
