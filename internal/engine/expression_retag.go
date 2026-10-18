package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement can retain TEMP sequence builtin generations. Rebind persisted
// scalar trees to those types without evaluating them or changing captured
// values, positions or expression properties.
func retagBoundTypes(view CatalogRead, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) (expr.Expr, bool, error) {
	retainedType = recordTypeMapper(retainedType, collations...)
	return retagBoundTypesWithQuery(view, node, retainedType, func(root LogicalNode) (LogicalNode, error) {
		if (view.state == nil || view.state.aclItemImport == nil) && (len(collations) == 0 || collations[0] == nil) && !queryDescriptorsChanged(root, retainedType) && !queryRoutineCarriersChanged(view, root) {
			return root, nil
		}
		return remapViewQueryMapped(view, root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, retainedType, collations...)
	}, collations...)
}

func retagBoundTypesWithQuery(view CatalogRead, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), collations ...collationRemapper) (expr.Expr, bool, error) {
	return mapBoundExpression(view, node, retainedType, queryMapper, nil, nil, collations...)
}

// mapBoundExpression shares the complete copy-on-change traversal with catalog
// retagging. A leaf rewrite runs before children, without evaluating expressions.
func mapBoundExpression(view CatalogRead, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), recipe *relationDescriptor, leaf func(expr.Expr) (expr.Expr, bool, error), collations ...collationRemapper) (expr.Expr, bool, error) {
	return mapBoundExpressionReader(view, nil, node, retainedType, queryMapper, recipe, leaf, collations...)
}
func mapBoundExpressionReader(view CatalogRead, reader definitionReader, node expr.Expr, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, queryMapper func(LogicalNode) (LogicalNode, error), recipe *relationDescriptor, leaf func(expr.Expr) (expr.Expr, bool, error), collations ...collationRemapper) (expr.Expr, bool, error) {
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
	info := expr.InfoOf(node)
	if leaf != nil {
		info.Property = rewrittenIntrinsicProperties(node)
	}
	typ := retainedType(info.Usage.Type)
	if typ == nil && info.Usage.Type != nil {
		return nil, false, fmt.Errorf("stuntgres: invalid remapped expression type")
	}
	changed := typ != info.Usage.Type
	info.Usage.Type = typ
	if _, failure := expressionRecipeDescriptor(node); failure != nil {
		return nil, false, failure
	}
	if recipe != nil && info.RoutineRecipeReference != recipe {
		info.RoutineRecipeReference = recipe
		changed = true
	}
	var err error
	if info.RoutineReference != nil {
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
			info.RoutineReference = current
		}
		// A catalog-free structural rewrite preserves this already-owned
		// identity. It neither relocates the callable nor admits its execution.
	}
	if len(collations) != 0 && collations[0] != nil {
		info.Usage.Collation, err = collations[0](info.Usage.Collation)
		if err != nil {
			return nil, false, err
		}
		changed = changed || info.Usage.Collation != node.ResultUsage().Collation
		info.CollationLeft, err = collations[0](info.CollationLeft)
		if err != nil {
			return nil, false, err
		}
		info.CollationRight, err = collations[0](info.CollationRight)
		if err != nil {
			return nil, false, err
		}
		oldInfo := expr.InfoOf(node)
		changed = changed || info.CollationLeft != oldInfo.CollationLeft || info.CollationRight != oldInfo.CollationRight
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
	mapChild := func(old expr.Expr) (expr.Expr, bool, error) {
		next, replaced, failure := mapBoundExpressionReader(view, reader, old, retainedType, queryMapper, recipe, leaf, collations...)
		changed = changed || replaced
		if next != nil && (leaf != nil || replaced) {
			info.Property = expr.MergeProperties(info.Property, next.Properties())
		}
		return next, replaced, failure
	}
	child := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		var next expr.Expr
		next, _, err = mapChild(old)
		return next
	}
	children := func(old []expr.Expr) []expr.Expr {
		var args []expr.Expr
		for i, arg := range old {
			next, replaced, failure := mapChild(arg)
			if failure != nil {
				err = failure
				return old
			}
			if replaced {
				if args == nil {
					args = append([]expr.Expr(nil), old...)
				}
				args[i] = next
			}
		}
		if args != nil {
			return args
		}
		return old
	}
	var rebound expr.Expr
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
		n.Info = info
		rebound = n
	case seriesExpression:
		n.args = children(n.args)
		n.Info = info
		rebound = n
	case subscriptsExpression:
		n.args = children(n.args)
		n.Info = info
		rebound = n
	case unnestExpression:
		n.args = children(n.args)
		n.Info = info
		if n.procedure != nil {
			descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
			proc, ok := descriptor.(*procedureDescriptor)
			if !ok || proc.intaggProvider() != stockIntagg {
				return nil, false, fmt.Errorf("stuntgres: missing remapped intagg enumerator")
			}
			changed = changed || n.procedure != proc
			n.procedure = proc
			if err := validateIntaggIdentity(view, proc, n.ref, "int_array_enum"); err != nil {
				return nil, false, err
			}
		}
		rebound = n
	case installedSoundexFunctionExpression:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.fuzzystrmatchProvider() != stockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Soundex function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Soundex, err = n.Reconstruct(info, n.Args)
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
		if !ok || proc.fuzzystrmatchProvider() != stockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Metaphone function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Metaphone, err = n.Reconstruct(info, n.Args)
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
		if !ok || proc.fuzzystrmatchProvider() != stockFuzzystrmatch {
			return nil, false, fmt.Errorf("stuntgres: missing remapped Levenshtein function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Levenshtein, err = n.Reconstruct(info, n.Args)
		if err == nil {
			err = n.validate(view)
		}
		rebound = n
	case installedUUIDOsspFunctionExpression:
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.uuidOsspProvider() != stockUUIDOssp {
			return nil, false, fmt.Errorf("stuntgres: missing remapped uuid-ossp function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Info = info
		err = n.validate(view)
		rebound = n
	case installedCryptoFunctionExpression:
		n.Args = children(n.Args)
		if err != nil {
			return node, false, err
		}
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok || proc.pgcryptoProvider() != stockPgcrypto {
			return nil, false, fmt.Errorf("stuntgres: missing remapped crypto function")
		}
		changed = changed || n.procedure != proc
		n.procedure = proc
		n.Pgcrypto, err = n.Reconstruct(info, n.Args)
		if err == nil {
			err = n.validate(view)
		}
		rebound = n
	case installedFunctionExpression:
		n.Arg = child(n.Arg)
		descriptor, _ := definitionDescriptorAt(view, reader, n.ref.Address)
		proc, ok := descriptor.(*procedureDescriptor)
		if !ok {
			return nil, false, fmt.Errorf("stuntgres: missing remapped installed function")
		}
		var dict *tsDictionaryDescriptor
		var tmpl *tsTemplateDescriptor
		if proc.unaccentProvider() != nil {
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
		n.Info = info
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
		n.Info = info
		rebound = n
	case rowExpression:
		var replaced bool
		n, replaced, err = mapRowAnalysis(n, mapChild)
		changed = changed || replaced
		n.Info = info
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
		n.Info = info
		rebound = n
	case arrayExpression:
		var elements []expr.Expr
		for i, old := range n.elements {
			next, replaced, failure := mapChild(old)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if elements == nil {
					elements = append([]expr.Expr(nil), n.elements...)
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
		n.Info = info
		rebound = n
	case subscriptExpression:
		var replaced bool
		n, replaced, err = mapSubscript(n, mapChild)
		if err != nil {
			return nil, false, err
		}
		changed = changed || replaced
		n.Info = info
		rebound = n
	case tableOIDExpression:
		n.Info = info
		rebound = n
	case indexPropertyExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		if err != nil {
			return nil, false, err
		}
		n.Info = info
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
					visitCTEQuery(root, func(LogicalNode) {}, func(expr expr.Expr) {
						properties := expr.Properties()
						info.Property.Volatility = max(info.Property.Volatility, properties.Volatility)
						info.Property.ParameterDependent = info.Property.ParameterDependent || properties.ParameterDependent
						info.Property.DomainDependent = info.Property.DomainDependent || properties.DomainDependent
						info.Property.RuntimeDomainConversion = info.Property.RuntimeDomainConversion || properties.RuntimeDomainConversion
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
			copy.plan, err = sqltype.BindJSONArrayAggregate(copy.input.ResultUsage(), info.Usage, true)
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
		n.Info = info
		rebound = n
	case deparseExpression:
		n.args = children(n.args)
		n.Info = info
		rebound = n
	case advisoryExpression:
		n.args = children(n.args)
		n.Info = info
		rebound = n
	case parameterExpression:
		if !changed {
			return node, false, nil
		}
		n.Info = info
		rebound = n
	case columnExpression:
		n.virtual = child(n.virtual)
		n.Info = info
		rebound = n
	case returningColumnExpression:
		if n.value != nil {
			n.value = child(n.value)
			n.Info = info
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
		var members []expr.Expr
		for i, member := range n.members {
			next := child(member)
			if changed && members == nil {
				members = append([]expr.Expr(nil), n.members...)
			}
			if members != nil {
				members[i] = next
			}
		}
		if members != nil {
			n.members = members
		}
		n.Info = info
		rebound = n
	case mergeActionExpression:
		n.Info = info
		rebound = n
	case groupExpression:
		n.Info = info
		rebound = n
	case aggregateExpression:
		n.Info = info
		rebound = n
	case groupingExpression:
		var args []expr.Expr
		for i, arg := range n.args {
			next, replaced, failure := mapChild(arg)
			if failure != nil {
				return nil, false, failure
			}
			if replaced {
				changed = true
				if args == nil {
					args = append([]expr.Expr(nil), n.args...)
				}
				args[i] = next
			}
		}
		if args != nil {
			n.args = args
		}
		n.Info = info
		rebound = n
	case *windowExpression:
		if !changed {
			return node, false, nil
		}
		copy := *n
		copy.Info = info
		rebound = &copy
	case cachedOrderExpression:
		n.Info = info
		rebound = n
	case currentSchemasExpression:
		n.arg = child(n.arg)
		n.Info = info
		rebound = n
	case objectAddressExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.Info = info
		rebound = n
	case aclValueExpression:
		for i := range n.arguments() {
			n.args[i] = child(n.args[i])
		}
		n.Info = info
		rebound = n
	case objectACLExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		n.Info = info
		rebound = n
	case accessInquiryExpression:
		for i := range n.count {
			n.args[i] = child(n.args[i])
		}
		n.Info = info
		rebound = n
	case namespaceInquiryExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		n.Info = info
		rebound = n
	case blockingInquiryExpression:
		n.arg = child(n.arg)
		n.Info = info
		rebound = n
	case sequenceCallExpression:
		n.args = children(n.args)
		n.Info = info
		rebound = n
	case catalogUserNameExpression:
		n.arg = child(n.arg)
		if err != nil {
			return node, false, err
		}
		if !changed {
			return node, false, nil
		}
		n.Info = info
		rebound = n
	case catalogForeignKeysExpression:
		if !changed {
			return node, false, nil
		}
		n.Info = info
		n.metadata, err = newCatalogForeignKeyValues(info.Usage.Type)
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
		n.Info = info
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
		n.Info = info
		rebound = n
	case visibilityInquiryExpression:
		n.arg = child(n.arg)
		n.Info = info
		rebound = n
	case commentInquiryExpression:
		n.args = children(n.args)
		n.Info = info
		rebound = n
	case caseExpression:
		n.test, n.otherwise = child(n.test), child(n.otherwise)
		var arms []caseBranch
		for i, old := range n.arms {
			arm := old
			arm.condition, arm.result = child(arm.condition), child(arm.result)
			if arm.routine != nil && view.state != nil {
				current, ok := view.Procedure(arm.routine.address)
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
		n.Info = info
		rebound = n
	case conditionalExpression:
		ref, conflict := n.comparison.InputCollation()
		ref = comparisonCollation(ref)
		var args []expr.Expr
		for i, old := range n.args {
			next := child(old)
			if changed && args == nil {
				args = append([]expr.Expr(nil), n.args...)
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
				n.comparison, n.missingOrdering, err = bindExtremaComparison(info.Usage, conflict)
			} else {
				n.comparison, err = sqltype.BindComparison(left, right, ref, conflict)
			}
		}
		n.Info = info
		rebound = n
	case identityExpression:
		n.Info = info
		rebound = n
	case domainValueExpression:
		if !changed {
			return node, false, nil
		}
		n.Info = info
		rebound = n
	case literalExpression:
		info.UnknownNull = n.UnknownNull
		info.RegclassRef = n.RegclassRef
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
		n.Info = info
		rebound = n
	case collationExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.Info = info
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
			n.plan, err = n.plan.Rebind(n.child.ResultUsage(), info.Usage, n.mode)
		}
		n.Info = info
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
			if info.Usage.Type.ID() == sqltype.Inet {
				n.network, err = sqltype.BindNetworkBitwise(n.operator, n.left.ResultUsage().Type, rt)
			} else if info.Usage.Type.ID() == sqltype.Bit {
				n.bits, err = sqltype.BindBitStringBitwise(n.operator, n.left.ResultUsage().Type, rt)
			} else {
				n.provider, err = sqltype.BindBitwise(n.operator, n.left.ResultUsage().Type, rt)
			}
		}
		n.Info = info
		rebound = n
	case selectedOperatorExpression:
		n.provider = child(n.provider)
		if view.state != nil {
			if current, ok := view.Operator(n.ref.Address); ok {
				changed = changed || current != n.operator
				n.operator = current
			}
		}
		if !changed {
			return node, false, err
		}
		n.Info = info
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
			n.provider, err = sqltype.BindArithmetic(n.operator, n.left.ResultUsage().Type, rightType, info.Usage.Type)
		}
		n.Info = info
		rebound = n
	case likeExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		if err == nil {
			var collation expr.Info
			collation, err = collatedInfo(view, expr.Info{Usage: n.left.ResultUsage(), Pos: info.Pos}, n.left, n.right)
			if err == nil {
				n.provider, err = sqltype.BindLike(n.operator, n.left.ResultUsage(), n.right.ResultUsage(), info.Usage, collation.CollationConflict)
			}
		}
		n.Info = info
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
		n.Info = info
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
		n.Info = info
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
		n.Info = info
		rebound = n
	case nullTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case truthTestExpression:
		n.child = child(n.child)
		if !changed {
			return node, false, err
		}
		n.Info = info
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
		n.Info = info
		rebound = n
	case logicalExpression:
		n.left, n.right = child(n.left), child(n.right)
		if !changed {
			return node, false, err
		}
		n.Info = info
		rebound = n
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		var mapped []expr.Expr
		mapped, err = mapCompositeChildren(n, mapChild)
		if err != nil {
			return node, false, err
		}
		if hook, ok := n.(expr.Retaggable); ok {
			rebound, changed, err = hook.Retag(info, mapped, changed, retagEnvironment(view, retainedType, collations))
		} else if changed {
			rebound, err = n.Rebuild(info, mapped)
		}
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

// mapCompositeChildren maps a protocol node's children, copying the slice only
// when a child is replaced.
func mapCompositeChildren(node expr.Composite, mapChild func(expr.Expr) (expr.Expr, bool, error)) ([]expr.Expr, error) {
	children := node.Children()
	var mapped []expr.Expr
	for i, old := range children {
		next, replaced, err := mapChild(old)
		if err != nil {
			return nil, err
		}
		if replaced {
			if mapped == nil {
				mapped = append([]expr.Expr(nil), children...)
			}
			mapped[i] = next
		}
	}
	if mapped == nil {
		return children, nil
	}
	return mapped, nil
}

// retagEnvironment gives Retaggable nodes the retained descriptors, the stored
// collation remap and collation derivation over the catalog view.
func retagEnvironment(view CatalogRead, retainedType func(*sqltype.Descriptor) *sqltype.Descriptor, collations []collationRemapper) expr.Retag {
	remap := func(ref sqltype.CollationRef) (sqltype.CollationRef, error) { return ref, nil }
	if len(collations) != 0 && collations[0] != nil {
		remap = collations[0]
	}
	return expr.Retag{
		Type:      retainedType,
		Collation: remap,
		Collated: func(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
			return collatedInfo(view, info, inputs...)
		},
	}
}
