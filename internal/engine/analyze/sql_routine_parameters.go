package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Candidate maps are private and producer-free. Only supplied usages rank;
// missing defaults are appended after the unique winner has been selected.
func sqlRoutineParameterShape(view catalog.Read, reader catalog.DefinitionReader, d *catalog.Procedure, raw parser.FunctionExpr) (SQLCallable, bool) {
	candidate := SQLCallable{Procedure: d, Inputs: make([]sqltype.Usage, len(raw.Args)), InputMap: make([]plan.RoutineInput, len(d.SQL.Inputs))}
	for _, input := range d.SQL.Inputs {
		candidate.polymorphic = candidate.polymorphic || sqltype.PolymorphicFamily(input.Type.ID()) != 0 || input.Type.ID() == 2249
	}
	for i := range candidate.InputMap {
		candidate.InputMap[i].Start = -1
	}
	// CALL projects declaration positions for every kind before selecting a
	// winner. Execution still receives only genuine input slots.
	count := len(d.SQL.InputFormals)
	if raw.ProcedureCall {
		count = len(d.SQL.Formals)
	}
	formalAt := func(position int) int {
		if raw.ProcedureCall {
			return position
		}
		return d.SQL.InputFormals[position]
	}
	sources := candidate.InputMap
	if count != len(sources) {
		sources = make([]plan.RoutineInput, count)
		for i := range sources {
			sources[i].Start = -1
		}
	}
	variadic := -1
	var variadicElement sqltype.Usage
	for position := range count {
		formal := d.SQL.Formals[formalAt(position)]
		if formal.Mode == "v" {
			variadic = position
			switch formal.Usage.Type.ID() {
			case 2277, 5078:
				id := sqltype.ID(2283)
				if formal.Usage.Type.ID() == 5078 {
					id = 5077
				}
				element, ok := catalog.DefinitionTypeAt(view, reader, id)
				if !ok {
					return candidate, false
				}
				variadicElement = element.Usage()
			default:
				variadicElement = formal.Usage.Type.Element().Usage()
			}
		}
	}
	if raw.ProcedureCall && variadic >= 0 {
		// Released candidate expansion uses the last full formal position,
		// even for functions whose legal OUT follows the VARIADIC input.
		// Kind rejection occurs after matching and before any execution.
		variadic = count - 1
	}
	if variadic >= 0 && len(raw.ArgNames) != 0 && !raw.Variadic {
		return candidate, false
	}
	for written := range raw.Args {
		slot := written
		label := ""
		if len(raw.ArgNames) > written {
			label = raw.ArgNames[written].Text
		}
		if label != "" {
			slot = -1
			for position := range count {
				if d.SQL.Formals[formalAt(position)].Name == label {
					slot = position
					break
				}
			}
			if slot < 0 {
				return candidate, false
			}
		} else if variadic >= 0 && written >= variadic && !raw.Variadic {
			slot = variadic
		}
		if slot < 0 || slot >= count {
			return candidate, false
		}
		source := &sources[slot]
		usage := d.SQL.Formals[formalAt(slot)].Usage
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
			usage = variadicElement
		} else {
			if source.Start >= 0 {
				return candidate, false
			}
			*source = plan.RoutineInput{Start: written, Count: 1}
		}
		candidate.Inputs[written] = usage
	}
	if raw.Variadic && variadic >= 0 {
		source := sources[variadic]
		if source.Start != len(raw.Args)-1 || source.Count != 1 {
			return candidate, false
		}
	}
	for position, source := range sources {
		formal := formalAt(position)
		if source.Start < 0 && !d.SQL.Formals[formal].HasDefault {
			return candidate, false
		}
		if input := d.SQL.FormalInputs[formal]; input >= 0 {
			candidate.InputMap[input] = source
		}
	}
	if raw.ProcedureCall {
		candidate.OutputMap = make([]plan.RoutineInput, len(d.SQL.OutputFormals))
		for i, formal := range d.SQL.OutputFormals {
			candidate.OutputMap[i] = sources[formal]
		}
	}
	return candidate, true
}
