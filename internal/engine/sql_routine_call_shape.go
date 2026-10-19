package engine

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sqlCallableShape(auth AuthorizationView, d *catalog.Procedure, raw parser.FunctionExpr) (sqlCallable, bool) {
	candidate := sqlCallable{procedure: d}
	var declared []sqltype.Usage
	if d.Signature != "" {
		for part := range strings.SplitSeq(string(d.Signature), ",") {
			id, err := strconv.ParseUint(part, 10, 32)
			t, ok := auth.definitionType(sqltype.ID(id))
			if err != nil || !ok {
				return candidate, false
			}
			declared = append(declared, t.Usage())
			candidate.polymorphic = candidate.polymorphic || !t.Concrete()
		}
	}
	var defaults int
	variadic := sqltype.ID(0)
	if d.Builtin != nil {
		defaults = int(d.Builtin.Defaults)
		variadic = sqltype.ID(d.Builtin.Variadic)
	}
	n := len(raw.Args)
	if raw.Variadic {
		if variadic == 0 || n != len(declared) {
			return candidate, false
		}
		candidate.inputs = declared
		return candidate, true
	}
	if variadic != 0 {
		fixed := len(declared) - 1
		if n < fixed {
			return candidate, false
		}
		element, ok := auth.definitionType(variadic)
		if !ok {
			return candidate, false
		}
		candidate.inputs = make([]sqltype.Usage, n)
		copy(candidate.inputs, declared[:fixed])
		for i := fixed; i < n; i++ {
			candidate.inputs[i] = element.Usage()
		}
		candidate.variadic = true
		return candidate, true
	}
	if n > len(declared) || n < len(declared)-defaults {
		return candidate, false
	}
	candidate.inputs = declared[:n]
	return candidate, true
}

// Expanded signatures mask only identical supplied-input shapes. Within one
// namespace a nonvariadic declaration wins over a variadic expansion; distinct
// defaultable declarations with the same supplied inputs remain ambiguous.
func appendSQLCallable(candidates []sqlCallable, candidate sqlCallable) []sqlCallable {
	for i, old := range candidates {
		if len(old.inputs) != len(candidate.inputs) {
			continue
		}
		same := true
		for j := range old.inputs {
			if old.inputs[j].Type != candidate.inputs[j].Type {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		if old.path != candidate.path {
			return candidates
		}
		if old.variadic != candidate.variadic {
			if old.variadic {
				candidates[i] = candidate
			}
			return candidates
		}
	}
	return append(candidates, candidate)
}

func (b *expressionBinder) probeSQLCallable(raw parser.FunctionExpr, operands []functions.Argument, candidate *sqlCallable) bool {
	name := candidate.procedure.Name()
	if candidate.procedure.RoutineKind == catalog.WindowRoutine {
		if family, ok := functions.LookupWindowFunction(name); ok && family.ProbeCall != nil {
			return family.ProbeCall(b, raw, operands, candidate.inputs, candidate.targets)
		}
	} else if candidate.procedure.RoutineKind == catalog.AggregateRoutine {
		if family, ok := functions.LookupAggregate(name); ok && family.ProbeCall != nil {
			return family.ProbeCall(b, raw, operands, candidate.inputs, candidate.targets)
		}
	} else if family, ok := functions.Lookup(name); ok && family.ProbeCall != nil {
		return family.ProbeCall(b, raw, operands, candidate.inputs, candidate.targets)
	}
	return b.probeLegacySQLCallable(raw, operands, candidate.inputs, candidate.targets)
}

// These families remain engine-owned in the existing dispatch chain.
func (b *expressionBinder) probeLegacySQLCallable(raw parser.FunctionExpr, operands []functions.Argument, declared, targets []sqltype.Usage) bool {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	switch name {
	case "enum_first", "enum_last", "enum_range":
		selection, resolution := functions.ResolveEnum(name, true, operands)
		if resolution != functions.Resolved {
			// Unknown enum inputs remain candidates until the winning family
			// produces its established polymorphic diagnostic.
			for _, o := range operands {
				if o.Usage.Type != nil {
					return false
				}
			}
			for i := range targets {
				targets[i] = sqltype.Usage{}
			}
			return true
		}
		for i := range targets {
			targets[i] = selection.Descriptor.Usage()
		}
		return true
	case "generate_subscripts":
		var ok bool
		targets[0], ok = subscriptInputTarget(operands[0].Usage.Type)
		return ok
	case "unnest":
		if operands[0].Usage.Type == nil {
			targets[0] = sqltype.Usage{}
			return true
		}
		input, result := unnestInputTypes(operands[0].Usage)
		if result == nil || input.IsArray() != (declared[0].Type.ID() == 2277) {
			return false
		}
		targets[0] = input.Usage()
		return true
	default:
		if functions.NullCountFunctionKind(name) != 0 && functions.NullCountAllowed(true, raw.Variadic, len(operands)) {
			for i := range targets {
				targets[i] = operands[i].Usage
			}
			return true
		}
	}
	return false
}

func (b *expressionBinder) sqlCallableAssumesKnown(raw parser.FunctionExpr, operands []functions.Argument, candidate sqlCallable) bool {
	if candidate.targets != nil {
		var small [4]sqltype.Usage
		targets := small[:min(len(operands), len(small))]
		if len(operands) > len(small) {
			targets = make([]sqltype.Usage, len(operands))
		}
		copy(targets, candidate.inputs)
		candidate.targets = targets
		if !b.probeSQLCallable(raw, operands, &candidate) {
			return false
		}
	}
	for i, operand := range operands {
		target := candidate.inputs[i]
		if candidate.targets != nil {
			target = candidate.targets[i]
		}
		if target.Type != nil && target.Type.Concrete() {
			if _, err := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); err != nil {
				return false
			}
		}
	}
	return true
}
