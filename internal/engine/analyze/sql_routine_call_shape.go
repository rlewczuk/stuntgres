package analyze

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func SQLCallableShape(view catalog.Read, reader catalog.DefinitionReader, d *catalog.Procedure, raw parser.FunctionExpr) (SQLCallable, bool) {
	if d.SQL != nil {
		return sqlRoutineParameterShape(d, raw)
	}
	candidate := SQLCallable{Procedure: d}
	var declared []sqltype.Usage
	if d.Signature != "" {
		for part := range strings.SplitSeq(string(d.Signature), ",") {
			id, err := strconv.ParseUint(part, 10, 32)
			t, ok := catalog.DefinitionTypeAt(view, reader, sqltype.ID(id))
			if err != nil || !ok {
				return candidate, false
			}
			declared = append(declared, t.Usage())
			candidate.polymorphic = candidate.polymorphic || !t.Concrete()
		}
	}
	if len(raw.ArgNames) > 0 {
		if len(declared) == 0 {
			return candidate, false
		}
		symbol, err := callableProviderSymbol(view, d)
		if err != nil {
			return candidate, false
		}
		family, ok := functions.Lookup(symbol)
		if !ok || family.NamedCall == nil {
			return candidate, false
		}
		candidate.NamedDeclaration = declared
		return candidate, true
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
		candidate.Inputs = declared
		return candidate, true
	}
	if variadic != 0 {
		fixed := len(declared) - 1
		if n < fixed {
			return candidate, false
		}
		element, ok := catalog.DefinitionTypeAt(view, reader, variadic)
		if !ok {
			return candidate, false
		}
		candidate.Inputs = make([]sqltype.Usage, n)
		copy(candidate.Inputs, declared[:fixed])
		for i := fixed; i < n; i++ {
			candidate.Inputs[i] = element.Usage()
		}
		candidate.Variadic = true
		return candidate, true
	}
	if n > len(declared) || n < len(declared)-defaults {
		return candidate, false
	}
	candidate.Inputs = declared[:n]
	return candidate, true
}

// Expanded signatures mask only identical supplied-input shapes. Within one
// namespace a nonvariadic declaration wins over a variadic expansion; distinct
// defaultable declarations with the same supplied inputs remain ambiguous.
func AppendSQLCallable(candidates []SQLCallable, candidate SQLCallable) []SQLCallable {
	for i, old := range candidates {
		if len(old.Inputs) != len(candidate.Inputs) {
			continue
		}
		same := true
		for j := range old.Inputs {
			if old.Inputs[j].Type != candidate.Inputs[j].Type {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		if old.Path != candidate.Path {
			return candidates
		}
		if old.Variadic != candidate.Variadic {
			if old.Variadic {
				candidates[i] = candidate
			}
			return candidates
		}
	}
	return append(candidates, candidate)
}

func (b *Binder) ProbeSQLCallable(raw parser.FunctionExpr, operands []functions.Argument, candidate *SQLCallable) bool {
	if candidate.NamedDeclaration != nil {
		symbol, err := callableProviderSymbol(b.BindingCatalog(), candidate.Procedure)
		if err != nil {
			return false
		}
		family, ok := functions.Lookup(symbol)
		if !ok || family.NamedCall == nil {
			return false
		}
		raw = providerFunctionSyntax(raw, symbol)
		var valid bool
		candidate.Inputs, candidate.Targets, valid = family.NamedCall(b, raw, operands, candidate.NamedDeclaration)
		return valid
	}
	// Concrete formals admit catalog edges before a polymorphic family's shape
	// probe. Probe sees the selected formal usage, but ranking retains the
	// original operands and declarations. No expression is constructed here.
	var small [8]functions.Argument
	probe := small[:min(len(operands), len(small))]
	if len(operands) > len(small) {
		probe = make([]functions.Argument, len(operands))
	}
	copy(probe, operands)
	for i, formal := range candidate.Inputs {
		if formal.Type != nil && formal.Type.Concrete() && probe[i].Usage.Type != nil {
			if !b.CanCoerce(probe[i].Usage, formal, sqltype.Implicit) {
				return false
			}
			probe[i].Usage = formal
		}
	}
	operands = probe
	name, err := callableProviderSymbol(b.BindingCatalog(), candidate.Procedure)
	if err != nil {
		return false
	}
	raw = providerFunctionSyntax(raw, name)
	if candidate.Procedure.RoutineKind == catalog.WindowRoutine {
		if family, ok := functions.LookupWindowFunction(name); ok && family.ProbeCall != nil {
			return family.ProbeCall(b, raw, operands, candidate.Inputs, candidate.Targets)
		}
	} else if candidate.Procedure.RoutineKind == catalog.AggregateRoutine {
		if family, ok := functions.LookupAggregate(name); ok && family.ProbeCall != nil {
			return family.ProbeCall(b, raw, operands, candidate.Inputs, candidate.Targets)
		}
	} else if family, ok := functions.Lookup(name); ok && family.ProbeCall != nil {
		return family.ProbeCall(b, raw, operands, candidate.Inputs, candidate.Targets)
	}
	return b.probeLegacySQLCallable(raw, operands, candidate.Inputs, candidate.Targets)
}

// These families remain engine-owned in the existing dispatch chain.
func (b *Binder) probeLegacySQLCallable(raw parser.FunctionExpr, operands []functions.Argument, declared, targets []sqltype.Usage) bool {
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
		input, result := plan.UnnestInputTypes(operands[0].Usage)
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

func (b *Binder) sqlCallableAssumesKnown(raw parser.FunctionExpr, operands []functions.Argument, candidate SQLCallable) bool {
	if candidate.Targets != nil {
		var small [4]sqltype.Usage
		targets := small[:min(len(operands), len(small))]
		if len(operands) > len(small) {
			targets = make([]sqltype.Usage, len(operands))
		}
		copy(targets, candidate.Inputs)
		candidate.Targets = targets
		if !b.ProbeSQLCallable(raw, operands, &candidate) {
			return false
		}
	}
	for i, operand := range operands {
		target := candidate.Inputs[i]
		if candidate.Targets != nil {
			target = candidate.Targets[i]
		}
		if target.Type != nil && target.Type.Concrete() {
			if err := catalog.CanCoerce(b.BindingCatalog(), operand.Usage, target, sqltype.Implicit); err != nil {
				return false
			}
		}
	}
	return true
}
