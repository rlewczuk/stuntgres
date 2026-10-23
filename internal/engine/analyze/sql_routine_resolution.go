package analyze

import (
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type SQLCallable struct {
	Procedure *catalog.Procedure
	Inputs    []sqltype.Usage
	// targets are family-resolved applicability usages, never ranking inputs.
	Targets          []sqltype.Usage
	Path             int
	Variadic         bool
	polymorphic      bool
	InputMap         []plan.RoutineInput
	NamedDeclaration []sqltype.Usage
}
type sqlCallableSelection struct {
	SQLCallable
	auth       Authority
	args       []expr.Expr
	properties expr.Properties
	// Ordinary selected typename calls retain their target and analyzed operand,
	// not a synthetic parser FunctionCast or a speculative routine winner.
	typeNameTarget  sqltype.Usage
	typeNamePlan    sqltype.CoercionPlan
	typeNameOperand ComparisonOperand
}

func (b *Binder) BindSQLCallable(raw parser.FunctionExpr) (expr.Expr, bool, error) {
	if b.Clause.selectedCallReentry {
		b.Clause.selectedCallReentry = false
		return nil, false, nil
	}
	if raw.Syntax != parser.OrdinaryFunctionSyntax || raw.StringSyntax != parser.OrdinaryStringSyntax {
		return nil, false, nil
	}
	selected, handled, err := b.selectSQLCallable(raw, nil)
	if err != nil || !handled {
		return nil, handled, err
	}
	node, err := b.finishSQLCallable(raw, selected, nil, ExpressionPolicy{Context: ProjectionContext})
	return node, true, err
}

// One callable selector serves ordinary calls and parser-lowered aggregate/cast
// spellings. A cast offers its existing type-owner shortcut only AFTER exact
// routine identity and BEFORE implicit/unknown matching. It never evaluates.
func (b *Binder) selectSQLCallable(raw parser.FunctionExpr, cast *parser.CastExpr) (sqlCallableSelection, bool, error) {
	var selection sqlCallableSelection
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !b.hasDynamicProcedureName(name) {
		return selection, false, nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return selection, true, err
	}
	path, err := b.Host.LookupNamespaces(auth, raw.Name, FunctionLookup)
	if err != nil {
		return selection, true, err
	}
	selection.auth = auth
	var candidates []SQLCallable
	var seen map[catalog.ProcedureSignature]bool
	if len(raw.ArgNames) == 0 {
		seen = make(map[catalog.ProcedureSignature]bool)
	}
	hasSQL := false
	for i := range path.Count() {
		for d := range catalog.DefinitionProceduresNamedAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), path.At(i), name) {
			if d.RoutineKind != catalog.FunctionRoutine && d.RoutineKind != catalog.ProcedureRoutine && d.RoutineKind != catalog.AggregateRoutine && d.RoutineKind != catalog.WindowRoutine || len(raw.ArgNames) == 0 && seen[d.Signature] {
				continue
			}
			if seen != nil {
				seen[d.Signature] = true
			}
			if d.DynamicCallable() {
				hasSQL = true
			}
			if !d.DynamicCallable() && d.Builtin == nil && d.Implementation == nil {
				continue
			}
			candidate, valid := SQLCallableShape(auth.ReadCatalog(), b.Host.DefinitionOf(auth), d, raw)
			if valid {
				candidate.Path = i
				candidates = append(candidates, candidate)
			}
		}
	}
	if !hasSQL {
		return selection, false, nil
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return selection, true, fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return selection, true, expr.ErrorAt("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return selection, true, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	operands := make([]ComparisonOperand, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		operands[i], err = b.comparisonOperand(arg)
		if err != nil {
			return selection, true, err
		}
		names[i] = "unknown"
		if t := operands[i].descriptor(); t != nil {
			names[i] = b.DiagnosticTypeName(t)
		}
		if len(raw.ArgNames) > i && raw.ArgNames[i].Text != "" {
			names[i] = raw.ArgNames[i].Text + " => " + names[i]
		}
	}
	var familyStorage [4]functions.Argument
	familyArgs := familyStorage[:min(len(operands), len(familyStorage))]
	if len(operands) > len(familyStorage) {
		familyArgs = make([]functions.Argument, len(operands))
	}
	for i, o := range operands {
		familyArgs[i] = functions.Argument{Expression: o.Node, Raw: o.raw}
		if o.descriptor() != nil {
			familyArgs[i].Usage = o.Node.ResultUsage()
		}
	}
	viable := candidates[:0]
	for _, candidate := range candidates {
		if candidate.NamedDeclaration != nil || candidate.polymorphic {
			if candidate.NamedDeclaration == nil {
				candidate.Targets = make([]sqltype.Usage, len(candidate.Inputs))
				copy(candidate.Targets, candidate.Inputs)
			}
			if !b.ProbeSQLCallable(raw, familyArgs, &candidate) {
				continue
			}
		}
		viable = AppendSQLCallable(viable, candidate)
	}
	candidates = viable
	// Domain identities participate in exact lookup before base-type ranking.
	exact := -1
	for i, candidate := range candidates {
		match := true
		for j, o := range operands {
			if o.descriptor() != candidate.Inputs[j].Type {
				match = false
				break
			}
		}
		if match {
			if exact >= 0 {
				return selection, true, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, true)
			}
			exact = i
		}
	}
	if exact >= 0 {
		selection.SQLCallable = candidates[exact]
	} else {
		if cast != nil {
			if _, _, ok := b.sqlFunctionCastTarget(cast.TypeName, operands[0]); ok {
				selection.args = []expr.Expr{operands[0].Node}
				return selection, true, nil
			}
		} else if len(operands) == 1 && (name == "box" || name == "circle" || name == "lseg" || name == "path" || name == "point") && len(raw.ArgNames) == 0 && !raw.Star && !raw.Variadic {
			annotation := parser.TypeName{Name: name, Pos: raw.Pos}
			if len(raw.Name.Parts) == 2 {
				annotation.Schema = &raw.Name.Parts[0]
			}
			if target, coercion, ok := b.sqlFunctionCastTarget(annotation, operands[0]); ok {
				selection.typeNameTarget, selection.typeNamePlan, selection.typeNameOperand = target, coercion, operands[0]
				return selection, true, nil
			}
		}
		assumed := false
		candidates = SelectSQLCallables(candidates, operands, func(candidate SQLCallable, known *sqltype.Descriptor) bool {
			if !assumed {
				for i, o := range operands {
					if o.descriptor() == nil {
						familyArgs[i].Usage = known.Usage()
					}
				}
				assumed = true
			}
			return b.sqlCallableAssumesKnown(raw, familyArgs, candidate)
		})
		if len(candidates) != 1 {
			return selection, true, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, len(candidates) > 1)
		}
		selection.SQLCallable = candidates[0]
	}
	selection.args = make([]expr.Expr, len(operands))
	selection.properties = expr.Properties{Volatility: expr.Volatile}
	for i, o := range operands {
		if selection.Procedure.DynamicCallable() {
			selection.args[i], err = b.CoerceConditional(o, selection.Inputs[i], "function")
			if err != nil {
				return selection, true, err
			}
			selection.properties = expr.MergeProperties(selection.properties, selection.args[i].Properties())
		} else if o.descriptor() != nil {
			// Unknown literals and parameters retain their raw occurrence for
			// the winning family. Known operands are never analyzed twice.
			selection.args[i] = o.Node
		}
	}
	return selection, true, nil
}

func SelectSQLCallables(candidates []SQLCallable, operands []ComparisonOperand, assumeKnown func(SQLCallable, *sqltype.Descriptor) bool) []SQLCallable {
	viable := candidates[:0]
	for _, candidate := range candidates {
		valid := true
		for i, o := range operands {
			target := candidate.Inputs[i]
			if candidate.Targets != nil {
				target = candidate.Targets[i]
			}
			if o.descriptor() != nil && target.Type != nil && target.Type.Concrete() {
				if _, err := sqltype.PlanCoercion(o.Node.ResultUsage(), target, sqltype.Implicit); err != nil {
					valid = false
					break
				}
			}
		}
		if valid {
			viable = append(viable, candidate)
		}
	}
	candidates = viable
	// Exact-count and same-category preferred inputs are independent passes.
	for _, preferred := range []bool{false, true} {
		best := -1
		out := candidates[:0]
		for _, candidate := range candidates {
			score := 0
			for i, o := range operands {
				if t := o.descriptor(); t != nil {
					t = t.BaseType()
					target := candidate.Inputs[i].Type
					if !preferred && t == target {
						score++
					}
					if preferred {
						from, _ := t.PGTypeMetadata()
						to, _ := target.PGTypeMetadata()
						if t == target || from.Category == to.Category && to.Preferred {
							score++
						}
					}
				}
			}
			if score > best {
				out = out[:0]
				best = score
			}
			if score == best {
				out = append(out, candidate)
			}
		}
		candidates = out
	}
	if len(candidates) > 1 {
		// Discover every unknown category against the same candidate set.
		// A conflicting non-string slot aborts this phase, even if another
		// slot would otherwise select one candidate.
		type unknownSlot struct {
			category  string
			preferred bool
		}
		var small [4]unknownSlot
		slots := small[:min(len(operands), len(small))]
		if len(operands) > len(small) {
			slots = make([]unknownSlot, len(operands))
		}
		consistent := true
		for i, o := range operands {
			if o.descriptor() != nil {
				continue
			}
			slot := &slots[i]
			conflict := false
			for _, candidate := range candidates {
				facts, _ := candidate.Inputs[i].Type.PGTypeMetadata()
				if slot.category == "" {
					slot.category, slot.preferred = facts.Category, facts.Preferred
				} else if facts.Category == slot.category {
					slot.preferred = slot.preferred || facts.Preferred
				} else if facts.Category == "S" {
					slot.category, slot.preferred = facts.Category, facts.Preferred
				} else {
					conflict = true
				}
			}
			if conflict && slot.category != "S" {
				consistent = false
			}
		}
		if consistent {
			// Count before compacting: if nothing survives, PostgreSQL keeps
			// the prephase candidate set for its final known-type heuristic.
			survives := func(candidate SQLCallable) bool {
				for i, o := range operands {
					if o.descriptor() == nil {
						facts, _ := candidate.Inputs[i].Type.PGTypeMetadata()
						if facts.Category != slots[i].category || slots[i].preferred && !facts.Preferred {
							return false
						}
					}
				}
				return true
			}
			count := 0
			for _, candidate := range candidates {
				if survives(candidate) {
					count++
				}
			}
			if count != 0 {
				out := candidates[:0]
				for _, candidate := range candidates {
					if survives(candidate) {
						out = append(out, candidate)
					}
				}
				candidates = out
			}
		}
	}
	if len(candidates) > 1 {
		var known *sqltype.Descriptor
		same := true
		for _, o := range operands {
			if t := o.descriptor(); t != nil {
				t = t.BaseType()
				if known != nil && t != known {
					same = false
				}
				known = t
			}
		}
		if same && known != nil {
			out := candidates[:0]
			for _, candidate := range candidates {
				valid := true
				if assumeKnown != nil {
					valid = assumeKnown(candidate, known)
				} else {
					for i, o := range operands {
						if o.descriptor() == nil {
							if _, err := sqltype.PlanCoercion(known.Usage(), candidate.Inputs[i], sqltype.Implicit); err != nil {
								valid = false
								break
							}
						}
					}
				}
				if valid {
					out = append(out, candidate)
				}
			}
			if len(out) != 0 {
				candidates = out
			}
		}
	}
	return candidates
}

func (b *Binder) finishSQLCallable(raw parser.FunctionExpr, selected sqlCallableSelection, original parser.Expr, policy ExpressionPolicy) (expr.Expr, error) {
	if selected.typeNameTarget.Type != nil {
		if err := functions.ValidateCallSyntax(b, raw); err != nil {
			return nil, err
		}
		if err := functions.CallDecorationError(raw); err != nil {
			return nil, err
		}
		operand, target := selected.typeNameOperand, selected.typeNameTarget
		arg := functions.Argument{Expression: operand.Node, Raw: operand.raw}
		if operand.descriptor() != nil {
			arg.Usage = operand.Node.ResultUsage()
		}
		return b.CoerceArgument(arg, functions.CoercionRequest{Intent: functions.TypeNameCastArgument, Target: target, Plan: selected.typeNamePlan, Planned: true, TypeNameCallPosition: raw.Pos})
	}
	d := selected.Procedure
	if d != nil && d.SQL != nil {
		if d.RoutineKind == catalog.ProcedureRoutine {
			diagnostic := expr.ErrorAt("42809", catalog.ObjectNameText(raw.Name)+"("+string(d.Signature)+") is a procedure", raw.Pos).(*sqlstate.Error)
			types := make([]string, len(d.SQL.Inputs))
			for i, input := range d.SQL.Inputs {
				types[i] = input.Type.DiagnosticName()
			}
			diagnostic.Message = catalog.ObjectNameText(raw.Name) + "(" + strings.Join(types, ", ") + ") is a procedure"
			diagnostic.Hint = "To call a procedure, use CALL."
			return nil, diagnostic
		}
		modifier := ""
		if raw.Distinct {
			modifier = "DISTINCT"
		} else if raw.Filter != nil {
			modifier = "FILTER"
		}
		if modifier != "" {
			return nil, expr.ErrorAt("42809", modifier+" specified, but "+catalog.ObjectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
		}
		if raw.Window != nil {
			return nil, expr.ErrorAt("42809", "OVER specified, but "+catalog.ObjectNameText(raw.Name)+" is not a window function nor an aggregate function", raw.Pos)
		}
		if raw.Star || len(raw.Order) != 0 {
			return nil, expr.ErrorAt("0A000", "SQL scalar function call modifiers are unsupported", raw.Pos)
		}
		ref := b.Host.CatalogOwner().Domain.Ref(selected.auth.ReadCatalog().Epoch(), d.Address())
		b.dependency(ref, d.Revision())
		result, _ := b.authorityType(selected.auth, sqltype.ID(d.Result))
		info := b.Info(result.Usage(), raw.Pos, selected.properties)
		info.RoutineReference = d
		if b.Params.NoParameters || b.Consumer.frozenDefinition {
			namespace, ok := b.authorityNamespace(selected.auth, d.Namespace())
			if !ok {
				return nil, fmt.Errorf("missing SQL routine namespace")
			}
			frozen := parser.FunctionExpr{
				Name: parser.ObjectName{Parts: []parser.Identifier{{Text: namespace.Name()}, {Text: d.Name()}}},
				Args: raw.Args, ArgNames: raw.ArgNames, Variadic: raw.Variadic, Pos: raw.Pos, StoredSQLRoutine: true,
			}
			if b.Stored.Syntax == nil {
				b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
			}
			b.Stored.Syntax[b.storedExpressionSyntaxKey(raw)] = frozen
		}
		args := selected.args
		assembly := selected.InputMap
		for slot, source := range assembly {
			if source.Start >= 0 {
				continue
			}
			if d.SQLDefaults == nil {
				return nil, fmt.Errorf("missing selected routine default")
			}
			node, ok := d.SQLDefaults.Default(slot)
			if !ok {
				return nil, fmt.Errorf("missing selected routine default slot")
			}
			assembly[slot] = plan.RoutineInput{Start: len(args), Count: 1}
			args = append(args, node)
			for _, dependency := range d.SQLDefaults.Dependencies() {
				b.dependency(dependency.Ref, dependency.Revision)
			}
			info.Property = expr.MergeProperties(info.Property, node.Properties())
		}
		node := plan.SQLRoutineExpr{Info: info, Procedure: d, Ref: ref, Args: args, WrittenCount: len(raw.Args), ArgNames: raw.ArgNames, Variadic: raw.Variadic, InputMap: assembly}
		node.ChildSlots = make([]int, len(args))
		for slot, source := range assembly {
			for i := source.Start; i < source.Start+source.Count; i++ {
				node.ChildSlots[i] = slot
			}
		}
		if err := node.ValidateArguments(args); err != nil {
			return nil, err
		}
		return node, nil
	}
	previous, previousSyntax, previousArguments := b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax, b.Clause.SelectedCallArguments
	previousReentry, previousCallee := b.Clause.selectedCallReentry, b.Clause.selectedWindowCallee
	previousAggregate := b.Clause.selectedAggregate
	if d != nil && d.Aggregate != nil && d.DynamicCallable() {
		b.Clause.selectedAggregate = d
	}
	b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax, b.Clause.SelectedCallArguments = selected.args, raw.Args, true
	// The winner's immediate gateway consumes this bit before its provider
	// analyzes FILTER, ORDER BY or other previously unanalyzed children.
	b.Clause.selectedCallReentry = true
	b.Clause.selectedWindowCallee = parser.ObjectName{}
	if d != nil && (d.RoutineKind == catalog.WindowRoutine || d.Aggregate != nil && d.DynamicCallable()) {
		b.Clause.selectedWindowCallee = raw.Name
	}
	defer func() {
		b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax, b.Clause.SelectedCallArguments = previous, previousSyntax, previousArguments
		b.Clause.selectedAggregate = previousAggregate
		b.Clause.selectedCallReentry = previousReentry
		b.Clause.selectedWindowCallee = previousCallee
	}()
	args := append([]parser.Expr(nil), raw.Args...)
	for i, arg := range selected.args {
		if arg != nil {
			args[i] = parser.ParameterExpr{Number: -i - 1, Pos: arg.SourcePosition()}
		}
	}
	if cast, ok := original.(parser.CastExpr); ok {
		if legacyFunctionCastPredicate(cast.Child) {
			return nil, expr.ErrorAt("0A000", "functions are unsupported", cast.Pos)
		}
		cast.Child = args[0]
		if d != nil {
			namespace, _ := b.authorityNamespace(selected.auth, d.Namespace())
			cast.TypeName.Schema = &parser.Identifier{Text: namespace.Name()}
		}
		return b.Bind(cast, policy)
	}
	if aggregate, ok := original.(parser.AggregateExpr); ok {
		namespace, _ := b.authorityNamespace(selected.auth, d.Namespace())
		aggregate.Schema = &parser.Identifier{Text: namespace.Name()}
		aggregate.Args = args
		return b.Bind(aggregate, policy)
	}
	if d.RoutineKind == catalog.AggregateRoutine || d.RoutineKind == catalog.WindowRoutine {
		namespace, _ := b.authorityNamespace(selected.auth, d.Namespace())
		return b.Bind(parser.AggregateExpr{
			Name: d.Name(), Schema: &parser.Identifier{Text: namespace.Name()}, Args: args, Pos: raw.Pos,
			Star: raw.Star, Distinct: raw.Distinct, Variadic: raw.Variadic, Order: raw.Order,
			Filter: raw.Filter, Window: raw.Window, Within: raw.Within, WithinPos: raw.WithinPos,
		}, policy)
	}
	namespace, _ := b.authorityNamespace(selected.auth, d.Namespace())
	raw.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: namespace.Name()}, {Text: d.Name()}}}
	raw.Args = args
	return b.BindFunctionProvider(raw)
}
