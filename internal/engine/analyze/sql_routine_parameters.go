package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Candidate maps are private and producer-free. Only supplied usages rank;
// missing defaults are appended after the unique winner has been selected.
func sqlRoutineParameterShape(d *catalog.Procedure, raw parser.FunctionExpr) (SQLCallable, bool) {
	candidate := SQLCallable{Procedure: d, Inputs: make([]sqltype.Usage, len(raw.Args)), InputMap: make([]plan.RoutineInput, len(d.SQL.Inputs))}
	for i := range candidate.InputMap {
		candidate.InputMap[i].Start = -1
	}
	variadic := -1
	if len(d.SQL.InputFormals) == len(d.SQL.Inputs) && len(d.SQL.Inputs) > 0 {
		i := len(d.SQL.Inputs) - 1
		if d.SQL.Formals[d.SQL.InputFormals[i]].Mode == "v" {
			variadic = i
		}
	}
	for written := range raw.Args {
		slot := written
		label := ""
		if len(raw.ArgNames) > written {
			label = raw.ArgNames[written].Text
		}
		if label != "" {
			slot = -1
			for input, name := range d.SQL.Names {
				if name == label {
					slot = input
					break
				}
			}
			if slot < 0 {
				return candidate, false
			}
		} else if variadic >= 0 && written >= variadic && !raw.Variadic {
			slot = variadic
		}
		if slot < 0 || slot >= len(d.SQL.Inputs) {
			return candidate, false
		}
		source := &candidate.InputMap[slot]
		usage := d.SQL.Inputs[slot]
		if slot == variadic && !raw.Variadic {
			if label != "" {
				return candidate, false
			}
			if source.Start < 0 {
				*source = plan.RoutineInput{Start: written, Expanded: true}
			}
			if source.Start+source.Count != written {
				return candidate, false
			}
			source.Count++
			candidate.Variadic = true
			usage = usage.Type.Element().Usage()
		} else {
			if source.Start >= 0 {
				return candidate, false
			}
			*source = plan.RoutineInput{Start: written, Count: 1}
		}
		candidate.Inputs[written] = usage
	}
	if raw.Variadic && variadic >= 0 {
		source := candidate.InputMap[variadic]
		if source.Start != len(raw.Args)-1 || source.Count != 1 {
			return candidate, false
		}
	}
	for slot, source := range candidate.InputMap {
		if source.Start >= 0 {
			continue
		}
		if !d.SQL.HasDefault(slot) {
			return candidate, false
		}
	}
	return candidate, true
}
