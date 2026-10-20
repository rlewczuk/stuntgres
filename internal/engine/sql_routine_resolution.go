package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type sqlCallable struct {
	procedure *catalog.Procedure
	inputs    []sqltype.Usage
	// targets are family-resolved applicability usages, never ranking inputs.
	targets     []sqltype.Usage
	path        int
	variadic    bool
	polymorphic bool
}
type sqlCallableSelection struct {
	sqlCallable
	auth       AuthorizationView
	args       []expr.Expr
	properties expr.Properties
}

func (b *expressionBinder) bindSQLCallable(raw parser.FunctionExpr) (expr.Expr, bool, error) {
	if b.clause.selectedCallReentry {
		b.clause.selectedCallReentry = false
		return nil, false, nil
	}
	if raw.Syntax != parser.OrdinaryFunctionSyntax || raw.StringSyntax != parser.OrdinaryStringSyntax || len(raw.ArgNames) != 0 {
		return nil, false, nil
	}
	selected, handled, err := b.selectSQLCallable(raw, nil)
	if err != nil || !handled {
		return nil, handled, err
	}
	node, err := b.finishSQLCallable(raw, selected, nil, expressionPolicy{context: projectionContext})
	return node, true, err
}

// One callable selector serves ordinary calls and parser-lowered aggregate/cast
// spellings. A cast offers its existing type-owner shortcut only AFTER exact
// routine identity and BEFORE implicit/unknown matching. It never evaluates.
func (b *expressionBinder) selectSQLCallable(raw parser.FunctionExpr, cast *parser.CastExpr) (sqlCallableSelection, bool, error) {
	var selection sqlCallableSelection
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if !b.hasSQLProcedureName(name) {
		return selection, false, nil
	}
	auth, err := b.bindingAuthorization()
	if err != nil {
		return selection, true, err
	}
	path, err := b.engine.lookupNamespaces(auth, raw.Name, functionLookup)
	if err != nil {
		return selection, true, err
	}
	selection.auth = auth
	var candidates []sqlCallable
	seen := map[catalog.ProcedureSignature]bool{}
	hasSQL := false
	for i := range path.count() {
		for d := range auth.definitionProceduresNamed(path.at(i), name) {
			if d.RoutineKind != catalog.FunctionRoutine && d.RoutineKind != catalog.AggregateRoutine && d.RoutineKind != catalog.WindowRoutine || seen[d.Signature] {
				continue
			}
			seen[d.Signature] = true
			if d.SQL != nil {
				hasSQL = true
			}
			if d.SQL == nil && d.Builtin == nil && d.Implementation == nil {
				continue
			}
			candidate, valid := sqlCallableShape(auth, d, raw)
			if valid {
				candidate.path = i
				candidates = append(candidates, candidate)
			}
		}
	}
	if !hasSQL {
		return selection, false, nil
	}
	operands := make([]comparisonOperand, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		operands[i], err = b.comparisonOperand(arg)
		if err != nil {
			return selection, true, err
		}
		names[i] = "unknown"
		if t := operands[i].descriptor(); t != nil {
			names[i] = b.diagnosticTypeName(t)
		}
	}
	var familyStorage [4]functions.Argument
	familyArgs := familyStorage[:min(len(operands), len(familyStorage))]
	if len(operands) > len(familyStorage) {
		familyArgs = make([]functions.Argument, len(operands))
	}
	for i, o := range operands {
		familyArgs[i] = functions.Argument{Expression: o.node, Raw: o.raw}
		if o.descriptor() != nil {
			familyArgs[i].Usage = o.node.ResultUsage()
		}
	}
	viable := candidates[:0]
	for _, candidate := range candidates {
		if candidate.polymorphic {
			candidate.targets = make([]sqltype.Usage, len(candidate.inputs))
			copy(candidate.targets, candidate.inputs)
			if !b.probeSQLCallable(raw, familyArgs, &candidate) {
				continue
			}
		}
		viable = appendSQLCallable(viable, candidate)
	}
	candidates = viable
	// Domain identities participate in exact lookup before base-type ranking.
	exact := -1
	for i, candidate := range candidates {
		match := true
		for j, o := range operands {
			if o.descriptor() != candidate.inputs[j].Type {
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
		selection.sqlCallable = candidates[exact]
	} else {
		if cast != nil && b.sqlFunctionCastShortcut(*cast, operands[0]) {
			selection.args = []expr.Expr{operands[0].node}
			return selection, true, nil
		}
		assumed := false
		candidates = selectSQLCallables(candidates, operands, func(candidate sqlCallable, known *sqltype.Descriptor) bool {
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
		selection.sqlCallable = candidates[0]
	}
	selection.args = make([]expr.Expr, len(operands))
	selection.properties = expr.Properties{Volatility: expr.Volatile}
	for i, o := range operands {
		if selection.procedure.SQL != nil {
			selection.args[i], err = b.coerceConditional(o, selection.inputs[i], "function")
			if err != nil {
				return selection, true, err
			}
			selection.properties = expr.MergeProperties(selection.properties, selection.args[i].Properties())
		} else if o.descriptor() != nil {
			// Unknown literals and parameters retain their raw occurrence for
			// the winning family. Known operands are never analyzed twice.
			selection.args[i] = o.node
		}
	}
	return selection, true, nil
}

func selectSQLCallables(candidates []sqlCallable, operands []comparisonOperand, assumeKnown func(sqlCallable, *sqltype.Descriptor) bool) []sqlCallable {
	viable := candidates[:0]
	for _, candidate := range candidates {
		valid := true
		for i, o := range operands {
			target := candidate.inputs[i]
			if candidate.targets != nil {
				target = candidate.targets[i]
			}
			if o.descriptor() != nil && target.Type != nil && target.Type.Concrete() {
				if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), target, sqltype.Implicit); err != nil {
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
					target := candidate.inputs[i].Type
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
				facts, _ := candidate.inputs[i].Type.PGTypeMetadata()
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
			survives := func(candidate sqlCallable) bool {
				for i, o := range operands {
					if o.descriptor() == nil {
						facts, _ := candidate.inputs[i].Type.PGTypeMetadata()
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
							if _, err := sqltype.PlanCoercion(known.Usage(), candidate.inputs[i], sqltype.Implicit); err != nil {
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

func (b *expressionBinder) finishSQLCallable(raw parser.FunctionExpr, selected sqlCallableSelection, original parser.Expr, policy expressionPolicy) (expr.Expr, error) {
	d := selected.procedure
	if d != nil && d.SQL != nil {
		modifier := ""
		if raw.Distinct {
			modifier = "DISTINCT"
		} else if raw.Filter != nil {
			modifier = "FILTER"
		}
		if modifier != "" {
			return nil, expr.ErrorAt("42809", modifier+" specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
		}
		if raw.Window != nil {
			return nil, expr.ErrorAt("42809", "OVER specified, but "+objectNameText(raw.Name)+" is not a window function nor an aggregate function", raw.Pos)
		}
		if raw.Star || len(raw.Order) != 0 {
			return nil, expr.ErrorAt("0A000", "SQL scalar function call modifiers are unsupported", raw.Pos)
		}
		ref := b.engine.catalog.Domain.Ref(selected.auth.Catalog.Epoch(), d.Address())
		b.dependency(ref, d.Revision())
		result, _ := selected.auth.definitionType(sqltype.ID(d.Result))
		info := b.info(result.Usage(), raw.Pos, selected.properties)
		info.RoutineReference = d
		if b.params.noParameters || b.consumer.materializedDefinition {
			namespace, ok := selected.auth.definitionNamespace(d.Namespace())
			if !ok {
				return nil, fmt.Errorf("missing SQL routine namespace")
			}
			frozen := parser.FunctionExpr{
				Name: parser.ObjectName{Parts: []parser.Identifier{{Text: namespace.Name()}, {Text: d.Name()}}},
				Args: raw.Args, Pos: raw.Pos, StoredSQLRoutine: true,
			}
			if b.stored.syntax == nil {
				b.stored.syntax = make(map[storedExpressionSyntaxKey]parser.Expr)
			}
			b.stored.syntax[storedExpressionSyntaxKey{frame: b.frameID(), position: raw.SourcePosition()}] = frozen
		}
		return plan.SQLRoutineExpr{Info: info, Procedure: d, Ref: ref, Args: selected.args}, nil
	}
	previous, previousSyntax, previousArguments := b.clause.selectedOperatorArgs, b.clause.selectedOperatorSyntax, b.clause.selectedCallArguments
	previousReentry, previousCallee := b.clause.selectedCallReentry, b.clause.selectedWindowCallee
	b.clause.selectedOperatorArgs, b.clause.selectedOperatorSyntax, b.clause.selectedCallArguments = selected.args, raw.Args, true
	// The winner's immediate gateway consumes this bit before its provider
	// analyzes FILTER, ORDER BY or other previously unanalyzed children.
	b.clause.selectedCallReentry = true
	b.clause.selectedWindowCallee = parser.ObjectName{}
	if d != nil && d.RoutineKind == catalog.WindowRoutine {
		b.clause.selectedWindowCallee = raw.Name
	}
	defer func() {
		b.clause.selectedOperatorArgs, b.clause.selectedOperatorSyntax, b.clause.selectedCallArguments = previous, previousSyntax, previousArguments
		b.clause.selectedCallReentry = previousReentry
		b.clause.selectedWindowCallee = previousCallee
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
			namespace, _ := selected.auth.definitionNamespace(d.Namespace())
			cast.TypeName.Schema = &parser.Identifier{Text: namespace.Name()}
		}
		return b.bind(cast, policy)
	}
	if aggregate, ok := original.(parser.AggregateExpr); ok {
		namespace, _ := selected.auth.definitionNamespace(d.Namespace())
		aggregate.Schema = &parser.Identifier{Text: namespace.Name()}
		aggregate.Args = args
		return b.bind(aggregate, policy)
	}
	if d.RoutineKind == catalog.AggregateRoutine || d.RoutineKind == catalog.WindowRoutine {
		namespace, _ := selected.auth.definitionNamespace(d.Namespace())
		return b.bind(parser.AggregateExpr{
			Name: d.Name(), Schema: &parser.Identifier{Text: namespace.Name()}, Args: args, Pos: raw.Pos,
			Star: raw.Star, Distinct: raw.Distinct, Variadic: raw.Variadic, Order: raw.Order,
			Filter: raw.Filter, Window: raw.Window, Within: raw.Within, WithinPos: raw.WithinPos,
		}, policy)
	}
	namespace, _ := selected.auth.definitionNamespace(d.Namespace())
	raw.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: namespace.Name()}, {Text: d.Name()}}}
	raw.Args = args
	return b.bindFunctionProvider(raw)
}
