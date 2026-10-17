package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement can retain TEMP sequence builtin generations. Rebind persisted
// scalar trees to those types without evaluating them or changing captured
// values, positions or expression properties.
func retagBoundTypes(view CatalogRead, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) (BoundExpr, bool, error) {
	retainedType = recordTypeMapper(retainedType, collations...)
	return retagBoundTypesWithQuery(view, node, retainedType, func(root LogicalNode) (LogicalNode, error) {
		if (view.state == nil || view.state.aclItemImport == nil) && (len(collations) == 0 || collations[0] == nil) && !queryDescriptorsChanged(root, retainedType) && !queryRoutineCarriersChanged(view, root) {
			return root, nil
		}
		return remapViewQueryMapped(view, root, func(ref ObjectRef) ObjectRef { return ref }, retainedType, collations...)
	}, collations...)
}

func retagBoundTypesWithQuery(view CatalogRead, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), collations ...collationRemapper) (BoundExpr, bool, error) {
	return mapBoundExpression(view, node, retainedType, queryMapper, nil, nil, collations...)
}

// mapBoundExpression shares the complete copy-on-change traversal with catalog
// retagging. A leaf rewrite runs before children, without evaluating expressions.
func mapBoundExpression(view CatalogRead, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), recipe *relationDescriptor, leaf func(BoundExpr) (BoundExpr, bool, error), collations ...collationRemapper) (BoundExpr, bool, error) {
	return mapBoundExpressionReader(view, nil, node, retainedType, queryMapper, recipe, leaf, collations...)
}
func mapBoundExpressionReader(view CatalogRead, reader definitionReader, node BoundExpr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), recipe *relationDescriptor, leaf func(BoundExpr) (BoundExpr, bool, error), collations ...collationRemapper) (BoundExpr, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	if leaf != nil {
		next, changed, err := leaf(node)
		if changed || err != nil {
			return next, changed, err
		}
	}
	if unresolved, ok := node.(unknownOutput); ok {
		// Retained contextual literal analysis owns no descriptor or value.
		// Executable query validation still rejects unresolved output nodes.
		literal, ok := unresolved.raw.(parser.LiteralExpr)
		if !ok || unresolved.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
			return nil, false, fmt.Errorf("stuntgres: invalid contextual view output")
		}
		return node, false, nil
	}
	info := boundExpressionInfo(node)
	if leaf != nil {
		info.properties = rewrittenIntrinsicProperties(node)
	}
	typ := retainedType(info.usage.Type)
	if typ == nil && info.usage.Type != nil {
		return nil, false, fmt.Errorf("stuntgres: invalid remapped expression type")
	}
	changed := typ != info.usage.Type
	info.usage.Type = typ
	if _, failure := expressionRecipeDescriptor(node); failure != nil {
		return nil, false, failure
	}
	if recipe != nil && info.routineRecipe != recipe {
		info.routineRecipe = recipe
		changed = true
	}
	var err error
	if info.routineReference != nil {
		captured, failure := expressionRoutineDescriptor(node)
		if failure != nil {
			return nil, false, failure
		}
		if view.state != nil {
			address := captured.address
			switch installed := node.(type) {
			case installedFunctionExpression:
				address = installed.ref.Address
			case installedCryptoFunctionExpression:
				address = installed.ref.Address
			case installedUUIDOsspFunctionExpression:
				address = installed.ref.Address
			case installedMetaphoneFunctionExpression:
				address = installed.ref.Address
			case installedLevenshteinFunctionExpression:
				address = installed.ref.Address
			case installedSoundexFunctionExpression:
				address = installed.ref.Address
			case unnestExpression:
				if installed.procedure != nil {
					address = installed.ref.Address
				}
			}
			descriptor, _ := definitionDescriptorAt(view, reader, address)
			current, ok := descriptor.(*procedureDescriptor)
			if !ok || current.name != captured.name || current.signature != captured.signature || current.routineKind != captured.routineKind || current.builtin != captured.builtin {
				return nil, false, fmt.Errorf("stuntgres: missing remapped routine declaration")
			}
			changed = changed || current != captured
			info.routineReference = current
		}
		// A catalog-free structural rewrite preserves this already-owned
		// identity. It neither relocates the callable nor admits its execution.
	}
	if len(collations) != 0 && collations[0] != nil {
		info.usage.Collation, err = collations[0](info.usage.Collation)
		if err != nil {
			return nil, false, err
		}
		changed = changed || info.usage.Collation != node.ResultUsage().Collation
		info.collationLeft, err = collations[0](info.collationLeft)
		if err != nil {
			return nil, false, err
		}
		info.collationRight, err = collations[0](info.collationRight)
		if err != nil {
			return nil, false, err
		}
		oldInfo := boundExpressionInfo(node)
		changed = changed || info.collationLeft != oldInfo.collationLeft || info.collationRight != oldInfo.collationRight
	}
	comparisonCollation := func(ref sqltype.CollationRef) sqltype.CollationRef {
		if err != nil || len(collations) == 0 || collations[0] == nil {
			return ref
		}
		next, failure := collations[0](ref)
		err = failure
		changed = changed || next != ref
		return next
	}
	mapChild := func(old BoundExpr) (BoundExpr, bool, error) {
		next, replaced, failure := mapBoundExpressionReader(view, reader, old, retainedType, queryMapper, recipe, leaf, collations...)
		changed = changed || replaced
		if next != nil && (leaf != nil || replaced) {
			info.properties = mergeExpressionProperties(info.properties, next.Properties())
		}
		return next, replaced, failure
	}
	child := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		var next BoundExpr
		next, _, err = mapChild(old)
		return next
	}
	children := func(old []BoundExpr) []BoundExpr {
		var args []BoundExpr
		for i, arg := range old {
			next, replaced, failure := mapChild(arg)
			if failure != nil {
				err = failure
				return old
			}
			if replaced {
				if args == nil {
					args = append([]BoundExpr(nil), old...)
				}
				args[i] = next
			}
		}
		if args != nil {
			return args
		}
		return old
	}
	var rebound BoundExpr
	switch n := node.(type) {
	case assignmentValueExpression:
		n.base, n.value = child(n.base), child(n.value)
		targets, failure := mapRuleAssignmentTargets([]*arrayAssignmentTarget{n.target}, mapChild, func(usage sqltype.Usage) (sqltype.Usage, error) {
			usage.Type = retainedType(usage.Type)
			usage.Collation = comparisonCollation(usage.Collation)
			return usage, err
		})
		if failure != nil {
			return nil, false, failure
		}
		changed = changed || targets[0] != n.target
		n.target = targets[0]
		n.expressionInfo = info
		rebound = n
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		n.RegexpCall, err = mapRegexpChildren(n.RegexpCall, view, info, child)
		n.Info = functionInfo(info)
		rebound = n
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		n.RegexpCall, err = mapRegexpChildren(n.RegexpCall, view, info, child)
		n.Info = functionInfo(info)
		rebound = n
	case seriesExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		rebound = n
	case subscriptsExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		rebound = n
	case unnestExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		if n.procedure != nil {
			descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
			proc, ok := descriptor.(*procedureDescriptor)
			if !ok || proc.intagg != stockIntagg {
				return nil, false, fmt.Errorf("stuntgres: missing remapped intagg enumerator")
			}
			changed = changed || n.procedure != proc
			n.procedure = proc
			if err := validateIntaggIdentity(view, proc, n.ref, "int_array_enum"); err != nil {
				return nil, false, err
			}
		}
		rebound = n
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if changed {
			n, err = n.Reconstruct(functionInfo(info), n.Args)
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.JSONBRecord[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if changed {
			n, err = n.Reconstruct(functionInfo(info), n.Args)
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.JSONRecord[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if changed {
			n, err = n.Reconstruct(functionInfo(info), n.Args)
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if changed {
			n, err = n.Reconstruct(functionInfo(info), n.Args)
		}
		n.Info = functionInfo(info)
		rebound = n
	case installedSoundexFunctionExpression:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.fuzzy != stockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Soundex function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Soundex, err = n.Reconstruct(functionInfo(info), n.Args)
		if err == nil {
			err = n.validate(view)
		}
		rebound = n
	case installedMetaphoneFunctionExpression:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.fuzzy != stockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Metaphone function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Metaphone, err = n.Reconstruct(functionInfo(info), n.Args)
		if err == nil {
			err = n.validate(view)
		}
		rebound = n
	case installedLevenshteinFunctionExpression:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.fuzzy != stockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Levenshtein function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Levenshtein, err = n.Reconstruct(functionInfo(info), n.Args)
		if err == nil {
			err = n.validate(view)
		}
		rebound = n
	case installedUUIDOsspFunctionExpression:
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.uuidOssp != stockUUIDOssp {
			return nil, false, fmt.Errorf("stuntgres: missing remapped uuid-ossp function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Info = functionInfo(info)
		err = n.validate(view)
		rebound = n
	case installedCryptoFunctionExpression:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.crypto != stockPgcrypto {
			return nil, false, fmt.Errorf("stuntgres: missing remapped crypto function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Pgcrypto, err = n.Reconstruct(functionInfo(info), n.Args)
		if err == nil {
			err = n.validate(view)
		}
		rebound = n
	case installedFunctionExpression:
		n.arg = child(n.arg)
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped installed function")
		}
		var dict *tsDictionaryDescriptor
		var tmpl *tsTemplateDescriptor
		if proc.provider != nil {
			descriptor, _ = definitionDescriptorAt(view, reader, proc.dictionary)
			dict, ok = descriptor.(*tsDictionaryDescriptor)
			if !ok {
				return nil, false, fmt.Errorf("stuntgres: missing remapped installed dictionary")
			}
			descriptor, _ = definitionDescriptorAt(view, reader, dict.template)
			tmpl, ok = descriptor.(*tsTemplateDescriptor)
			if !ok {
				return nil, false, fmt.Errorf("stuntgres: missing remapped installed template")
			}
		}
		changed = changed || n.procedure != proc || n.dictionary != dict || n.template != tmpl
		n.procedure, n.dictionary, n.template = proc, dict, tmpl
		n.expressionInfo = info
		if !n.validProvider() {
			return nil, false, fmt.Errorf("stuntgres: invalid remapped installed function")
		}
		rebound = n
	case viewQualificationExpression:
		mapped, replaced, failure := mapViewQualification(n, mapChild)
		if failure != nil {
			return nil, false, failure
		}
		changed = changed || replaced
		n = mapped.(viewQualificationExpression)
		n.expressionInfo = info
		rebound = n
	case rowExpression:
		var replaced bool
		n, replaced, err = mapRowAnalysis(n, mapChild)
		changed = changed || replaced
		n.expressionInfo = info
		rebound = n
	case fieldExpression:
		n.base = child(n.base)
		if err != nil {
			return nil, false, err
		}
		index, ok := n.base.ResultUsage().Type.BaseType().CompositeAttributeIndex(n.attribute)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: retired remapped field")
		}
		changed = changed || index != n.index
		n.index = index
		n.expressionInfo = info
		rebound = n
	case arrayExpression:
		var elements []BoundExpr
		for i, old := range n.elements {
			next, replaced, failure := mapChild(old)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if elements == nil {
					elements = append([]BoundExpr(nil), n.elements...)
				}
				elements[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if elements != nil {
			n.elements = elements
		}
		n.expressionInfo = info
		rebound = n
	case subscriptExpression:
		var replaced bool
		n, replaced, err = mapSubscript(n, mapChild)
		if err != nil {
			return nil, false, err
		}
		changed = changed || replaced
		n.expressionInfo = info
		rebound = n
	case tableOIDExpression:
		n.expressionInfo = info
		rebound = n
	case indexPropertyExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return nil, false, err
		}
		n.expressionInfo = info
		if n.kind != accessMethodProperty {
			n.oidUsage.Type = retainedType(n.oidUsage.Type)
			n.oidPlan, err = sqltype.PlanCoercion(n.args[0].ResultUsage(), n.oidUsage, sqltype.Implicit)
			if err != nil {
				return nil, false, err
			}
		}
		rebound = n
	case subqueryExpression:
		if queryMapper != nil {
			root, failure := queryMapper(n.queryPlan.Root)
			if failure != nil {
				return nil, false, failure
			}
			if root != n.queryPlan.Root {
				copy := *n.queryPlan
				copy.Root = root
				n.queryPlan = &copy
				changed = true
				if leaf != nil {
					visitCTEQuery(root, func(LogicalNode) {}, func(expr BoundExpr) {
						properties := expr.Properties()
						info.properties.Volatility = max(info.properties.Volatility, properties.Volatility)
						info.properties.ParameterDependent = info.properties.ParameterDependent || properties.ParameterDependent
						info.properties.DomainDependent = info.properties.DomainDependent || properties.DomainDependent
						info.properties.RuntimeDomainConversion = info.properties.RuntimeDomainConversion || properties.RuntimeDomainConversion
					}, true)
				}
			}
		}
		var comparisons []comparisonExpression
		for i, old := range n.comparisons {
			next := child(old)
			if err != nil {
				return nil, false, err
			}
			if changed && comparisons == nil {
				comparisons = append([]comparisonExpression(nil), n.comparisons...)
			}
			if comparisons != nil {
				comparisons[i] = next.(comparisonExpression)
			}
		}
		var expansionChanged bool
		n.leftExpansions, expansionChanged, err = mapRowComparisonExpansions(n.leftExpansions, mapChild)
		if err != nil {
			return nil, false, err
		}
		changed = changed || expansionChanged
		if n.leftConstructor != nil {
			n.leftConstructor = child(n.leftConstructor)
			if err != nil {
				return nil, false, err
			}
		}
		if n.jsonArray != nil {
			copy := *n.jsonArray
			copy.input = child(copy.input)
			if err != nil {
				return nil, false, err
			}
			copy.plan, err = sqltype.BindJSONArrayAggregate(copy.input.ResultUsage(), info.usage, true)
			if err != nil {
				return nil, false, err
			}
			n.jsonArray = &copy
		}
		if !changed {
			return node, false, nil
		}
		if comparisons != nil {
			n.comparisons = comparisons
		}
		if len(n.ordering) != 0 {
			n.ordering = make([]sqltype.Comparison, len(n.comparisons))
			for i, c := range n.comparisons {
				ref, conflict := c.predicate.InputCollation()
				n.ordering[i], err = sqltype.BindComparison(c.left.ResultUsage().Type, c.right.ResultUsage().Type, ref, conflict)
				if err != nil {
					return nil, false, err
				}
			}
		}
		if len(n.membership) != 0 {
			n.membership = make([]sqltype.Grouping, len(n.comparisons))
			for i, pair := range n.comparisons {
				n.membership[i], err = pair.predicate.EqualityGrouping()
				if err != nil {
					return nil, false, err
				}
			}
		}
		n.expressionInfo = info
		rebound = n
	case deparseExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		rebound = n
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		if len(n.Args) != 0 {
			next := child(n.Args[0])
			if changed {
				n.Args = []BoundExpr{next}
			}
			nextType := retainedType(n.Timestamp)
			changed = changed || nextType != n.Timestamp
			n.Timestamp = nextType
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Args, n.Timestamp)
			}
		} else {
			n.Info = functionInfo(info)
		}
		rebound = n
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		n.Info = functionInfo(info)
		rebound = n
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		n.Arg = child(n.Arg)
		clockType := retainedType(n.ClockUsage.Type)
		changed = changed || clockType != n.ClockUsage.Type
		n.ClockUsage.Type = clockType
		if err == nil && changed {
			n, err = n.Reconstruct(functionInfo(info), n.Arg, n.ClockUsage)
		}
		n.Info = functionInfo(info)
		rebound = n
	case advisoryExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		rebound = n
	case parameterExpression:
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case columnExpression:
		n.virtual = child(n.virtual)
		n.expressionInfo = info
		rebound = n
	case returningColumnExpression:
		if n.value != nil {
			n.value = child(n.value)
			n.expressionInfo = info
			rebound = n
			break
		}
		next := child(n.columnExpression)
		column, ok := next.(columnExpression)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: invalid RETURNING column remap")
		}
		n.columnExpression = column
		rebound = n
	case returningWholeRowExpression:
		var members []BoundExpr
		for i, member := range n.members {
			next := child(member)
			if changed && members == nil {
				members = append([]BoundExpr(nil), n.members...)
			}
			if members != nil {
				members[i] = next
			}
		}
		if members != nil {
			n.members = members
		}
		n.expressionInfo = info
		rebound = n
	case mergeActionExpression:
		n.expressionInfo = info
		rebound = n
	case groupExpression:
		n.expressionInfo = info
		rebound = n
	case aggregateExpression:
		n.expressionInfo = info
		rebound = n
	case groupingExpression:
		var args []BoundExpr
		for i, arg := range n.args {
			next, replaced, failure := mapChild(arg)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if args == nil {
					args = append([]BoundExpr(nil), n.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			n.args = args
		}
		n.expressionInfo = info
		rebound = n
	case *windowExpression:
		if !changed {
			return node, false, nil
		}
		copy := *n
		copy.expressionInfo = info
		rebound = &copy
	case cachedOrderExpression:
		n.expressionInfo = info
		rebound = n
	case currentSchemasExpression:
		n.arg = child(n.arg)
		n.expressionInfo = info
		rebound = n
	case objectAddressExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case aclValueExpression:
		for i := range n.arguments() {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case objectACLExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case accessInquiryExpression:
		for i := range n.count {
			n.args[i] = child(n.args[i])
		}
		n.expressionInfo = info
		rebound = n
	case namespaceInquiryExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		n.expressionInfo = info
		rebound = n
	case blockingInquiryExpression:
		n.arg = child(n.arg)
		n.expressionInfo = info
		rebound = n
	case sequenceCallExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		rebound = n
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		for _, arg := range n.Args {
			if arg == nil {
				return node, false, fmt.Errorf("stuntgres: invalid remapped binary argument")
			}
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		n.Arg = child(n.Arg)
		if err != nil {
			return node, false, err
		}
		mapped, planChanged, failure := n.Reconstruct(functionInfo(info), n.Arg, retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || planChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		mapped, planChanged, failure := n.Reconstruct(functionInfo(info), n.Args, retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || planChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		mapped, planChanged, failure := n.Reconstruct(functionInfo(info), n.Args, retainedType, sqltype.Value.Retag)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || planChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		mapped, planChanged, failure := n.Reconstruct(functionInfo(info), n.Args, retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || planChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		mapped, planChanged, failure := n.Reconstruct(functionInfo(info), n.Args, retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || planChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		mapped, providerChanged, failure := n.Reconstruct(functionInfo(info), n.Args)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || providerChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		mapped, planChanged, failure := n.Reconstruct(functionInfo(info), n.Args, retainedType)
		if failure != nil {
			return node, false, failure
		}
		changed = changed || planChanged
		if !changed {
			return node, false, nil
		}
		rebound = mapped
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		var did bool
		n, did, err = mapJSONExtractChildren(n, mapChild)
		if err != nil {
			return node, false, err
		}
		if !changed && !did {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Arguments() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		for _, arg := range n.Args {
			if arg == nil {
				return node, false, fmt.Errorf("stuntgres: invalid remapped string argument")
			}
		}
		text := n.Plan.TextUsage()
		text.Type = retainedType(text.Type)
		if len(collations) != 0 && collations[0] != nil {
			text.Collation, err = collations[0](text.Collation)
			if err != nil {
				return nil, false, err
			}
		}
		if !changed && text == n.Plan.TextUsage() {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args, text)
		rebound = n
	case functions.ArrayRandom[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.ArrayTransform[BoundExpr, *EvalContext, ExprProperties]:
		ref, conflict := n.Sort.InputCollation()
		ref = comparisonCollation(ref)
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args, ref, conflict)
		rebound = n
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		ref, conflict := n.Plan.InputCollation()
		ref = comparisonCollation(ref)
		n.Args = children(n.Args)
		array, integer := retainedType(n.Plan.Array()), retainedType(n.Plan.Integer())
		changed = changed || array != n.Plan.Array() || integer != n.Plan.Integer()
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args, array, info.usage.Type, integer, ref, conflict)
		rebound = n
	case catalogUserNameExpression:
		n.arg = child(n.arg)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case catalogForeignKeysExpression:
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		n.metadata, err = newCatalogForeignKeyValues(info.usage.Type)
		if err != nil {
			return node, false, err
		}
		rebound = n
	case transactionIdentityExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case commitTimestampExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		n.Arg = child(n.Arg)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Arg)
		rebound = n
	case functions.NetworkFormat[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Kind.Arity() {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.RangeOperator[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		for _, arg := range n.Args {
			if arg == nil {
				return node, false, fmt.Errorf("stuntgres: invalid remapped range inspection argument")
			}
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		for _, arg := range n.Args {
			if arg == nil {
				return node, false, fmt.Errorf("stuntgres: invalid remapped enum argument")
			}
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args)
		rebound = n
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		ref, conflict := n.Plan.InputCollation()
		ref = comparisonCollation(ref)
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		for _, arg := range n.Args {
			if arg == nil {
				return node, false, fmt.Errorf("stuntgres: invalid remapped math argument")
			}
		}
		n, err = n.Reconstruct(functionInfo(info), n.Args, ref, conflict)
		if err == nil {
			n.WrapperContext = mathWrapperContext(n.Plan.Name(), n.Args)
		}
		rebound = n
	case visibilityInquiryExpression:
		n.arg = child(n.arg)
		n.expressionInfo = info
		rebound = n
	case commentInquiryExpression:
		n.args = children(n.args)
		n.expressionInfo = info
		rebound = n
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		n.Info = functionInfo(info)
		rebound = n
	case sqlJSONExpression:
		n.args = children(n.args)
		if err == nil {
			rebound, err = n.reconstruct(info, n.args)
		}
	case jsonPredicateExpression:
		n.child = child(n.child)
		n.expressionInfo = info
		rebound = n
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		if err == nil {
			n, err = n.Reconstruct(functionInfo(info), n.Args)
		}
		rebound = n
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		n.Args = append([]BoundExpr(nil), n.Args...)
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if err == nil {
			n, err = n.Reconstruct(functionInfo(info), n.Args)
		}
		rebound = n
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		n.Args = children(n.Args)
		int4 := retainedType(n.Int4)
		changed = changed || int4 != n.Int4
		n.Int4 = int4
		n.Info = functionInfo(info)
		rebound = n
	case caseExpression:
		n.test, n.otherwise = child(n.test), child(n.otherwise)
		var arms []caseBranch
		for i, old := range n.arms {
			arm := old
			arm.condition, arm.result = child(arm.condition), child(arm.result)
			if arm.routine != nil && view.state != nil {
				current, ok := view.state.descriptors[arm.routine.address].(*procedureDescriptor)
				if !ok || !sameRoutineDeclaration(current, arm.routine) {
					return nil, false, fmt.Errorf("stuntgres: missing remapped CASE equality declaration")
				}
				changed = changed || arm.routine != current
				arm.routine = current
			}
			ref, conflict := arm.equality.InputCollation()
			ref = comparisonCollation(ref)
			if n.test != nil && err == nil {
				arm.testUsage.Type = retainedType(arm.testUsage.Type)
				if len(collations) != 0 && collations[0] != nil {
					arm.testUsage.Collation, err = collations[0](arm.testUsage.Collation)
					if err != nil {
						return nil, false, err
					}
				}
				arm.testPlan, err = sqltype.PlanCoercion(n.test.ResultUsage(), arm.testUsage, sqltype.Implicit)
				if err == nil {
					arm.equality, err = sqltype.BindComparison(arm.testUsage.Type, arm.condition.ResultUsage().Type, ref, conflict)
				}
			}
			if changed && arms == nil {
				arms = append([]caseBranch(nil), n.arms...)
			}
			if arms != nil {
				arms[i] = arm
			}
		}
		if !changed {
			return node, false, err
		}
		if arms != nil {
			n.arms = arms
		}
		n.expressionInfo = info
		rebound = n
	case conditionalExpression:
		ref, conflict := n.comparison.InputCollation()
		ref = comparisonCollation(ref)
		var args []BoundExpr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]BoundExpr(nil), n.args...)
			}
			if args != nil {
				args[i] = next
			}
		}
		if !changed {
			return node, false, err
		}
		if args != nil {
			n.args = args
		}
		if err == nil && n.kind != parser.Coalesce {
			left, right := typ, typ
			if n.kind == parser.NullIf {
				left, right = n.args[0].ResultUsage().Type, n.args[1].ResultUsage().Type
			}
			if n.kind == parser.Greatest || n.kind == parser.Least {
				n.comparison, n.missingOrdering, err = bindExtremaComparison(info.usage, conflict)
			} else {
				n.comparison, err = sqltype.BindComparison(left, right, ref, conflict)
			}
		}
		n.expressionInfo = info
		rebound = n
	case identityExpression:
		n.expressionInfo = info
		rebound = n
	case functions.CurrentDateTime[*EvalContext, ExprProperties]:
		n.Info = functionInfo(info)
		rebound = n
	case functions.VersionInformation[*EvalContext, ExprProperties]:
		n.Info = functionInfo(info)
		rebound = n
	case functions.TransactionTime[*EvalContext, ExprProperties]:
		n.Info = functionInfo(info)
		rebound = n
	case domainValueExpression:
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case literalExpression:
		info.unknownNull = n.unknownNull
		info.regclassRef = n.regclassRef
		if !changed && (view.state == nil || view.state.aclItemImport == nil || !n.value.Type().ContainsACLItem()) {
			return node, false, nil
		}
		var collation collationRemapper
		if len(collations) != 0 {
			collation = collations[0]
		}
		previous := n.value
		n.value, err = reownDomainDatum(n.value, typ, collation, view.state.scalarImports, view.state.aclItemImport)
		if err != nil {
			return nil, false, err
		}
		changed = changed || n.value != previous
		if !changed {
			return node, false, nil
		}
		n.expressionInfo = info
		rebound = n
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		n.Value = child(n.Value)
		if !n.Local {
			n.Zone = child(n.Zone)
		}
		if !changed {
			return node, false, err
		}
		n.Info = functionInfo(info)
		rebound = n
	case collationExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case coercionExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		// The bound expression already distinguishes semantic authority from
		// storage/formal destinations. Map that captured Usage and structure;
		// rederiving from the child would erase the destination contract.
		if changed && err == nil {
			n.plan, err = n.plan.Rebind(n.child.ResultUsage(), info.usage, n.mode)
		}
		n.expressionInfo = info
		rebound = n
	case bitwiseExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rt *sqltype.Descriptor
			if n.right != nil {
				rt = n.right.ResultUsage().Type
			}
			n.provider, n.bits, n.network = sqltype.Bitwise{}, sqltype.BitStringBitwise{}, sqltype.NetworkBitwise{}
			if info.usage.Type.ID() == sqltype.Inet {
				n.network, err = sqltype.BindNetworkBitwise(n.operator, n.left.ResultUsage().Type, rt)
			} else if info.usage.Type.ID() == sqltype.Bit {
				n.bits, err = sqltype.BindBitStringBitwise(n.operator, n.left.ResultUsage().Type, rt)
			} else {
				n.provider, err = sqltype.BindBitwise(n.operator, n.left.ResultUsage().Type, rt)
			}
		}
		n.expressionInfo = info
		rebound = n
	case selectedOperatorExpression:
		n.provider = child(n.provider)
		if view.state != nil {
			if current, ok := view.state.descriptors[n.ref.Address].(*operatorDescriptor); ok {
				changed = changed || current != n.operator
				n.operator = current
			}
		}
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case arithmeticExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var rightType *sqltype.Descriptor
			if n.right != nil {
				rightType = n.right.ResultUsage().Type
			}
			n.provider, err = sqltype.BindArithmetic(n.operator, n.left.ResultUsage().Type, rightType, info.usage.Type)
		}
		n.expressionInfo = info
		rebound = n
	case likeExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expressionInfo
			collation, err = collatedInfo(view, expressionInfo{usage: n.left.ResultUsage(), pos: info.pos}, n.left, n.right)
			if err == nil {
				n.provider, err = sqltype.BindLike(n.operator, n.left.ResultUsage(), n.right.ResultUsage(), info.usage, collation.collationConflict)
			}
		}
		n.expressionInfo = info
		rebound = n
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		n.Pattern, n.Escape = child(n.Pattern), child(n.Escape)
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.Pattern, n.Escape)
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Pattern, n.Escape)
			}
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expressionInfo
			collation, err = collatedInfo(view, expressionInfo{usage: n.Left.ResultUsage(), pos: info.pos}, n.Left, n.Right)
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Left, n.Right, collation.usage.Collation, collation.collationConflict)
			}
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		n.Left, n.Right = child(n.Left), child(n.Right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expressionInfo
			collation, err = collatedInfo(view, expressionInfo{usage: n.Left.ResultUsage(), pos: info.pos}, n.Left, n.Right)
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Left, n.Right, collation.usage.Collation, collation.collationConflict)
			}
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		n.Pattern, n.Escape = child(n.Pattern), child(n.Escape)
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.Pattern, n.Escape)
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Pattern, n.Escape)
			}
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.Args[:]...)
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Args)
			}
		}
		n.Info = functionInfo(info)
		rebound = n
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for i := range n.Args {
			n.Args[i] = child(n.Args[i])
		}
		if !changed {
			return node, false, err
		}
		if err == nil {
			info, err = collatedInfo(view, info, n.Args[:]...)
			if err == nil {
				n, err = n.Reconstruct(functionInfo(info), n.Args)
			}
		}
		n.Info = functionInfo(info)
		rebound = n
	case rowComparisonExpression:
		var pairs []comparisonExpression
		for i, pair := range n.comparisons {
			next := child(pair)
			if err != nil {
				return nil, false, err
			}
			if changed && pairs == nil {
				pairs = append([]comparisonExpression(nil), n.comparisons...)
			}
			if pairs != nil {
				pairs[i] = next.(comparisonExpression)
			}
		}
		for side, expansions := range n.expansions {
			var replaced bool
			n.expansions[side], replaced, err = mapRowComparisonExpansions(expansions, mapChild)
			if err != nil {
				return nil, false, err
			}
			changed = changed || replaced
		}
		if !changed {
			return node, false, nil
		}
		if pairs != nil {
			n.comparisons = pairs
		}
		if len(n.ordering) != 0 {
			n.ordering = make([]sqltype.Comparison, len(n.comparisons))
			for i, pair := range n.comparisons {
				ref, conflict := pair.predicate.InputCollation()
				n.ordering[i], err = sqltype.BindComparison(pair.left.ResultUsage().Type, pair.right.ResultUsage().Type, ref, conflict)
				if err != nil {
					return nil, false, err
				}
			}
		}
		n.expressionInfo = info
		rebound = n
	case scalarArrayComparisonExpression:
		ref, conflict := n.predicate.InputCollation()
		ref = comparisonCollation(ref)
		var replaced bool
		n, replaced, err = mapScalarArrayChildren(n, mapChild)
		changed = changed || replaced
		if !changed {
			return node, false, err
		}
		if err == nil {
			operator, ok := comparisonOperator(n.operator)
			if !ok {
				return nil, false, fmt.Errorf("stuntgres: invalid array comparison operator")
			}
			n.predicate, err = sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.elementType(), typ, ref, conflict)
		}
		n.expressionInfo = info
		rebound = n
	case comparisonExpression:
		ref, conflict := n.predicate.InputCollation()
		ref = comparisonCollation(ref)
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			var operator sqltype.ComparisonOperator
			switch n.operator {
			case "=":
				operator = sqltype.CompareEqual
			case "<>":
				operator = sqltype.CompareNotEqual
			case "<":
				operator = sqltype.CompareLess
			case "<=":
				operator = sqltype.CompareLessEqual
			case ">":
				operator = sqltype.CompareGreater
			case ">=":
				operator = sqltype.CompareGreaterEqual
			default:
				return nil, false, fmt.Errorf("stuntgres: invalid default comparison")
			}
			n.predicate, err = sqltype.BindComparisonPredicate(operator, n.left.ResultUsage().Type, n.right.ResultUsage().Type, typ, ref, conflict)
		}
		n.expressionInfo = info
		rebound = n
	case nullTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case truthTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	case distinctExpression:
		ref, conflict := n.equality.InputCollation()
		ref = comparisonCollation(ref)
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if changed && err == nil {
			n.equality, err = sqltype.BindComparisonPredicate(sqltype.CompareEqual, n.left.ResultUsage().Type, n.right.ResultUsage().Type, typ, ref, conflict)
		}
		n.expressionInfo = info
		rebound = n
	case logicalExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		n.expressionInfo = info
		rebound = n
	default:
		return nil, false, fmt.Errorf("stuntgres: unsupported stored default node")
	}
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return node, false, nil
	}
	return rebound, true, nil
}
