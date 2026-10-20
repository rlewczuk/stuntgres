package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func rebaseQueryColumns(columns []queryColumn, offset int) []queryColumn {
	out := append([]queryColumn(nil), columns...)
	for i := range out {
		out[i].Slot += offset
	}
	return out
}
func rebaseQueryNamespace(ns queryNamespace, offset int) queryNamespace {
	ns.Columns = rebaseQueryColumns(ns.Columns, offset)
	ns.Qualifiers = append([]queryQualifier(nil), ns.Qualifiers...)
	for i := range ns.Qualifiers {
		ns.Qualifiers[i].Columns = rebaseQueryColumns(ns.Qualifiers[i].Columns, offset)
	}
	return ns
}
func combineQueryNamespace(left, right queryNamespace) (queryNamespace, error) {
	for _, l := range left.Qualifiers {
		for _, r := range right.Qualifiers {
			if l.Name == r.Name && (l.Alias != "" || r.Alias != "" || l.Ref == r.Ref || l.Ref == (ids.ObjectRef{}) || r.Ref == (ids.ObjectRef{})) {
				return queryNamespace{}, sqlError("42712", fmt.Sprintf(`table name "%s" specified more than once`, l.Name))
			}
		}
	}
	return queryNamespace{
		Columns:    append(append([]queryColumn(nil), left.Columns...), right.Columns...),
		Qualifiers: append(append([]queryQualifier(nil), left.Qualifiers...), right.Qualifiers...),
		Frame:      append(append([]catalog.Column(nil), left.Frame...), right.Frame...),
	}, nil
}

func (b *expressionBinder) QueryColumnAt(c queryColumn, pos parser.Position) expr.Expr {
	var origin ids.ObjectRef
	var relation *catalog.Relation
	if c.CTEScan != nil {
		c.CTEScan.Demand[c.Physical] = true
	}
	if c.Leaf == analyze.QueryTargetLeaf {
		origin = b.useColumnOf(b.queryTargetRelation(), c.Physical)
		relation = b.queryTargetRelation()
	} else if c.Leaf >= 0 {
		leaf := &b.query.sources[c.Leaf]
		relation = leaf.Relation
		origin = leaf.Ref
		origin.Address.SubID = leaf.Relation.Attributes[c.Physical]
		b.dependency(origin, leaf.Relation.Revision())
		found := false
		for _, slot := range leaf.Uses {
			if slot == c.Physical {
				found = true
				break
			}
		}
		if !found {
			leaf.Uses = append(leaf.Uses, c.Physical)
		}
	}
	column := plan.ColumnExpr{Info: b.info(c.Descriptor.Usage, pos, expr.Properties{RowDependent: true}), Origin: origin, Scope: 1, Frame: b.FrameID(), Slot: c.Slot}
	column.MergeSource = c.Leaf != analyze.QueryTargetLeaf && b.mergeReturningFrame(b.FrameID())
	if b.target.generatedOwner == nil {
		column = column.WithGeneration(relation, c.Physical, c.Slot-c.Physical)
		if column.Virtual != nil && c.Leaf >= 0 {
			column.QueryOccurrence = c.Leaf + 1
		}
	}
	return column
}

func (b *expressionBinder) queryQualifier(name parser.ObjectName, pos parser.Position) (*queryQualifier, error) {
	label := name.Parts[len(name.Parts)-1].Text
	var match *queryQualifier
	for i := range b.query.namespace.Qualifiers {
		q := &b.query.namespace.Qualifiers[i]
		if q.Name != label || len(name.Parts) == 2 && (q.Alias != "" || q.Schema != name.Parts[0].Text) {
			continue
		}
		if match != nil {
			return nil, expr.ErrorAt("42P09", fmt.Sprintf(`table reference "%s" is ambiguous`, label), pos)
		}
		match = q
	}
	if match != nil {
		if b.clause.aggregateClause == "FROM" && b.clause.stringFromForbidden {
			return nil, stringLateralError(*match, pos, false)
		}
		b.query.resolvedQueryFrame = b.FrameID()
		return match, nil
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		scope := &b.query.scopes[i]
		if !scope.allowed || scope.namespace == nil {
			continue
		}
		for j := range scope.namespace.Qualifiers {
			q := &scope.namespace.Qualifiers[j]
			if q.Name != label || len(name.Parts) == 2 && (q.Alias != "" || q.Schema != name.Parts[0].Text) {
				continue
			}
			if match != nil {
				return nil, expr.ErrorAt("42P09", fmt.Sprintf(`table reference "%s" is ambiguous`, label), pos)
			}
			match = q
		}
		if match != nil {
			if scope.stringFromForbidden {
				return nil, stringLateralError(*match, pos, false)
			}
			b.query.resolvedQueryFrame = scope.frame
			return match, nil
		}
	}
	hidden := append(append([]queryQualifier(nil), b.query.hidden...), b.query.outerQualifiers()...)
	for _, q := range hidden {
		if q.Original != label && q.Name != label {
			continue
		}
		if len(name.Parts) == 2 && q.Schema != name.Parts[0].Text {
			continue
		}
		if b.stringFromContext() && q.LateralForbidden {
			return nil, stringLateralError(q, pos, true)
		}
		err := expr.ErrorAt("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, label), pos).(*sqlstate.Error)
		visibleAlias := false
		if q.Original == label && q.Alias != "" && q.Alias != label {
			visibleAlias = b.queryAliasVisible(q.Alias, q.Ref)
		}
		if visibleAlias {
			err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, q.Alias)
		} else {
			err.Detail = fmt.Sprintf(`There is an entry for table "%s", but it cannot be referenced from this part of the query.`, q.Name)
			if b.queryQualifierNeedsLateral(q) && !q.ExcludeColumnDiagnostics && !q.LateralForbidden && (q.Ref != (ids.ObjectRef{}) || q.Alias != "") {
				err.Hint = "To reference that table, you must mark this subquery with LATERAL."
			}
		}
		return nil, err
	}
	return nil, expr.ErrorAt("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, label), pos)
}
func (b *expressionBinder) queryColumn(raw parser.ColumnExpr) (expr.Expr, error) {
	parts := raw.Name.Parts
	if len(parts) == 0 || len(parts) > 3 {
		return nil, fmt.Errorf("stuntgres: invalid query column name")
	}
	if len(parts) == 2 {
		if scope, newValues := b.outerReturningScope(parser.ObjectName{Parts: parts[:1]}); scope != nil {
			relation := scope.relation
			slot, ok := relation.Index[parts[1].Text]
			if !ok {
				return nil, expr.ErrorAt("42703", fmt.Sprintf("column %s.%s does not exist", parts[0].Text, parts[1].Text), raw.Pos)
			}
			return b.outerReturningColumnAt(scope, slot, raw.Pos, newValues), nil
		}
	}
	name := parts[len(parts)-1].Text
	columns := b.query.namespace.Columns
	frame := b.FrameID()
	if len(parts) > 1 {
		q, err := b.queryQualifier(parser.ObjectName{Parts: parts[:len(parts)-1]}, raw.Pos)
		if err != nil {
			return nil, err
		}
		columns = q.Columns
		frame = b.query.resolvedQueryFrame
	}
	var match *queryColumn
	for i := range columns {
		if columns[i].Descriptor.Name != name {
			continue
		}
		if match != nil {
			return nil, expr.ErrorAt("42702", fmt.Sprintf(`column reference "%s" is ambiguous`, name), raw.Pos)
		}
		match = &columns[i]
		if b.clause.aggregateClause == "FROM" && b.clause.stringFromForbidden {
			return nil, stringForbiddenColumn(*b.query.namespace, match.Slot, raw.Pos)
		}
	}
	if match != nil {
		if demand := b.query.namespace.IdentityValuesOrderDemand; len(demand) != 0 {
			demand[match.Slot] = true
		}
		return b.queryColumnInFrame(*match, raw.Pos, frame), nil
	}
	if b.target.mergeWhen && b.FrameID() == 1 && name != "tableoid" && catalog.IsSystemColumn(name) {
		for _, q := range b.query.namespace.Qualifiers {
			if len(parts) > 1 && (q.Name != parts[len(parts)-2].Text || len(parts) == 3 && q.Schema != parts[0].Text) {
				continue
			}
			if relation, ok := b.authorityRelation(b.query.auth, q.Ref.Address); ok && relation.RelationKind == catalog.TableRelation {
				return nil, expr.ErrorAt("42P10", fmt.Sprintf(`cannot use system column "%s" in MERGE WHEN condition`, name), raw.Pos)
			}
		}
	}
	if len(parts) == 1 {
		for i := len(b.query.scopes) - 1; i >= 0; i-- {
			scope := &b.query.scopes[i]
			if !scope.allowed || scope.namespace == nil {
				continue
			}
			for j := range scope.namespace.Columns {
				c := &scope.namespace.Columns[j]
				if c.Descriptor.Name != name {
					continue
				}
				if match != nil {
					return nil, expr.ErrorAt("42702", fmt.Sprintf(`column reference "%s" is ambiguous`, name), raw.Pos)
				}
				match = c
			}
			if match != nil {
				if scope.stringFromForbidden {
					return nil, stringForbiddenColumn(*scope.namespace, match.Slot, raw.Pos)
				}
				return b.queryColumnInFrame(*match, raw.Pos, scope.frame), nil
			}
		}
		// Only after all visible column scopes fail may an unqualified name
		// denote a relation's whole row. Ambiguity never reaches this fallback.
		if scope, newValues := b.outerReturningScope(raw.Name); scope != nil {
			return b.outerReturningWholeRow(scope, raw.Pos, newValues)
		}
		for _, q := range b.query.namespace.Qualifiers {
			if q.Name == name {
				return b.wholeRow(raw.Name, raw.Pos, true)
			}
		}
		for i := len(b.query.scopes) - 1; i >= 0; i-- {
			scope := b.query.scopes[i]
			if !scope.allowed || scope.namespace == nil {
				continue
			}
			if expr, found, err := b.returningWholeRow(name, scope.relation, scope.frame, scope.returning, raw.Pos); found {
				return expr, err
			}
			for _, q := range scope.namespace.Qualifiers {
				if q.Name == name {
					return b.wholeRow(raw.Name, raw.Pos, true)
				}
			}
		}
	}
	if b.query.namespace.Values && len(parts) == 1 {
		return nil, valuesOrderColumnError(b.query.namespace.Frame, name, raw.Pos)
	}
	qualifier := ""
	if len(parts) > 1 {
		qualifier = parts[len(parts)-2].Text
	}
	hidden := append(append([]queryQualifier(nil), b.query.hidden...), b.query.outerQualifiers()...)
	if b.stringFromContext() {
		for _, q := range hidden {
			if !q.LateralForbidden {
				continue
			}
			for _, column := range q.Columns {
				if column.Descriptor.Name == name && (qualifier == "" || qualifier == q.Name) {
					return nil, stringLateralError(q, raw.Pos, true)
				}
			}
		}
	}
	detail, hint, err := analyze.QueryColumnDiagnostic(hidden, qualifier, name, raw.Pos, b.query.nonLateral)
	if err != nil {
		return nil, err
	}
	var message string
	if len(parts) > 1 {
		message = fmt.Sprintf("column %s.%s does not exist", qualifier, name)
	} else {
		message = fmt.Sprintf(`column "%s" does not exist`, name)
	}
	missing := expr.ErrorAt("42703", message, raw.Pos).(*sqlstate.Error)
	missing.Detail, missing.Hint = detail, hint
	return nil, missing
}

func (b *expressionBinder) BindFrom(item parser.FromItem) (plan.Node, queryNamespace, error) {
	return b.bindFromWithDefinitions(item, nil)
}

func (b *expressionBinder) bindFromWithDefinitions(item parser.FromItem, definitions []parser.FunctionColumnDefinition) (root plan.Node, namespace queryNamespace, failure error) {
	// FROM preorder is shared with the renderer. Capture the namespace after
	// aliases and USING have been applied, before a parent rebases its slots.
	defer func() {
		if failure == nil {
			for i := range namespace.Qualifiers {
				if namespace.Qualifiers[i].SourceRoot == nil {
					namespace.Qualifiers[i].SourceRoot = root
				}
			}
		}
	}()
	ordinal := -1
	if (b.params.noParameters || b.consumer.materializedDefinition) && item != nil {
		ordinal = len(b.capture.Namespaces)
		b.capture.Namespaces = append(b.capture.Namespaces, viewNamespace{})
		defer func() {
			if failure == nil {
				b.capture.Namespaces[ordinal].Frame = b.FrameID()
				b.capture.Namespaces[ordinal].Node, b.capture.Namespaces[ordinal].Ordinal = analyze.FrozenNamespaceNode(root)
				b.capture.Namespaces[ordinal].Columns = b.query.freezeViewColumns(namespace.Columns)
				for _, q := range namespace.Qualifiers {
					b.capture.Namespaces[ordinal].Qualifiers = append(b.capture.Namespaces[ordinal].Qualifiers, viewNamespaceQualifier{Name: q.Name, Columns: b.query.freezeViewColumns(q.Columns)})
				}
			}
		}()
	}
	if err := b.ctx.Err(); err != nil {
		return nil, queryNamespace{}, err
	}
	switch n := item.(type) {
	case nil:
		return &plan.SingletonNode{}, queryNamespace{}, nil
	case *parser.FunctionTable:
		normalized := parser.NormalizedFunctionCalls(n)
		for _, raw := range normalized {
			if len(definitions) != 0 || analyze.SelectedFunctionSourceName(raw.Name.Parts[len(raw.Name.Parts)-1].Text) {
				return b.BindMixedFunctionTable(normalized, n.Ordinality, ordinal, definitions, !n.RowsFrom && len(normalized) > 1)
			}
		}
		return b.bindStringTable(n)
	case *parser.FunctionFrom:
		return b.BindMixedFunctionTable(n.Calls, n.Ordinality, ordinal, definitions, false)
	case *parser.DerivedQuery:
		query, err := b.bindSelectScoped(n.Query, false, true)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		schema := query.Root.Schema()
		columns := make([]queryColumn, len(schema))
		for i, column := range schema {
			columns[i] = queryColumn{Descriptor: column, Slot: i, Leaf: -1}
		}
		b.query.hidden = append(b.query.hidden, queryQualifier{Name: "unnamed_subquery", Columns: columns})
		return &plan.DerivedNode{QuerySchema: schema, Query: query, ScanFrame: b.FrameID()}, queryNamespace{Columns: columns, Frame: schema}, nil
	case *parser.ValuesQuery:
		query, err := b.bindValues(n, false)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		schema := query.Root.Schema()
		columns := make([]queryColumn, len(schema))
		for i, column := range schema {
			columns[i] = queryColumn{Descriptor: column, Slot: i, Leaf: -1, Physical: -1}
		}
		b.query.hidden = append(b.query.hidden, queryQualifier{Name: "unnamed_subquery", Columns: columns})
		return query.Root, queryNamespace{Columns: columns, Frame: schema}, nil
	case *parser.TableReference:
		if len(n.Name.Parts) == 1 {
			for i := len(b.cte.bindings) - 1; i >= 0; i-- {
				binding := b.cte.bindings[i]
				if binding.name == n.Name.Parts[0].Text {
					return b.bindCTEScan(n, binding)
				}
			}
		}
		ref, err := b.host.AdmittedReference(b.query.auth, n.Name, b.query.lockAdmission.TableQueries[n].Ref)
		if err != nil {
			return nil, queryNamespace{}, analyze.FutureCTEError(err, b.query.lockAdmission.FutureCTE[n])
		}
		relation, _ := b.authorityRelation(b.query.auth, ref.Address)
		if relation.RelationKind == catalog.CompositeRelation {
			return nil, queryNamespace{}, typedef.CompositeOpenError(relation, n.Name.Parts[0].Pos)
		}
		if relation.RelationKind == catalog.IndexRelation {
			if b.target.deleteUsingSources {
				err := expr.ErrorAt("42809", fmt.Sprintf(`cannot open relation "%s"`, relation.Name()), n.Name.Parts[0].Pos).(*sqlstate.Error)
				err.Detail = "This operation is not supported for indexes."
				return nil, queryNamespace{}, err
			}
			return nil, queryNamespace{}, sqlError("42809", fmt.Sprintf(`cannot read from index "%s"`, relation.Name()))
		}
		if relation.RelationKind == catalog.ViewRelation {
			if err := b.captureRetainedViewInput(relation); err != nil {
				return nil, queryNamespace{}, err
			}
		}
		leaf := len(b.query.sources)
		b.query.sources = append(b.query.sources, exec.Leaf{Ref: ref, Relation: relation, Only: n.Only, Storage: b.host.AuthorizationStorage(b.query.auth, ref.Address), Mark: b.query.lockAdmission.TableMark(n), Table: n, Query: b.query.lockAdmission.TableQueries[n].Query, DeleteReference: b.target.deleteUsingSources && len(b.query.scopes) == 0})
		columns := make([]queryColumn, len(relation.Columns))
		for i, c := range relation.Columns {
			columns[i] = queryColumn{Descriptor: c, Slot: i, Leaf: leaf, Physical: i}
		}
		namespace, _ := b.authorityDescriptor(b.query.auth, relation.Namespace())
		q := queryQualifier{Name: relation.Name(), Original: relation.Name(), Schema: namespace.Name(), Ref: ref, Columns: columns}
		b.query.hidden = append(b.query.hidden, q)
		return &plan.SourceNode{QuerySchema: relation.Columns, Ref: ref, Occurrence: leaf, RangeName: relation.Name()}, queryNamespace{Columns: columns, Qualifiers: []queryQualifier{q}, Frame: relation.Columns}, nil
	case *parser.AliasReference:
		root, ns, err := b.bindFromWithDefinitions(n.Input, n.ColumnDefinitions)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		if n.Name.Text == "" {
			return root, ns, nil
		}
		aliasWidth := len(ns.Columns)
		switch scan := root.(type) {
		case *plan.WorktableScanNode:
			if scan.CTE.Search != nil || scan.CTE.Cycle != nil {
				aliasWidth = scan.CTE.UserWidth
			}
		case *plan.DerivedNode:
			if scan.CTE != nil && (scan.CTE.Search != nil || scan.CTE.Cycle != nil) {
				aliasWidth = scan.CTE.UserWidth
			}
		}
		if len(n.Columns) > aliasWidth {
			kind := "join expression"
			switch n.Input.(type) {
			case *parser.TableReference, *parser.ValuesQuery, *parser.DerivedQuery, *parser.FunctionTable, *parser.FunctionFrom:
				kind = "table"
			}
			return nil, queryNamespace{}, sqlError("42P10", fmt.Sprintf(`%s "%s" has %d columns available but %d columns specified`, kind, n.Name.Text, aliasWidth, len(n.Columns)))
		}
		ns.Columns = append([]queryColumn(nil), ns.Columns...)
		for i, name := range n.Columns {
			ns.Columns[i].Descriptor.Name = name.Text
		}
		if source, ok := root.(*plan.SourceNode); ok {
			source.RangeName = n.Name.Text
			if len(n.Columns) != 0 {
				source.ColumnAliases = make([]string, len(n.Columns))
				for i, column := range n.Columns {
					source.ColumnAliases[i] = column.Text
				}
			}
		}
		if len(n.Columns) == 0 {
			calls := 0
			switch function := n.Input.(type) {
			case *parser.FunctionTable:
				calls = len(parser.NormalizedFunctionCalls(function))
			case *parser.FunctionFrom:
				calls = len(function.Calls)
			}
			function, composite := root.(*plan.FunctionFromNode)
			composite = composite && len(function.Calls) == 1 && function.Calls[0].ResultUsage().Type.BaseType().IsComposite()
			// A selected scalar OUT name controls the column label, not the range alias.
			hasScalarOut := function != nil && len(function.Calls) == 1 && expr.SourceOutName(function.Calls[0]) != ""
			if calls == 1 && !composite && !hasScalarOut {
				ns.Columns[0].Descriptor.Name = n.Name.Text
			}
		}
		q := queryQualifier{Name: n.Name.Text, Alias: n.Name.Text, Columns: ns.Columns}
		if _, table := n.Input.(*parser.TableReference); table {
			q.Ref, q.Schema, q.Original = ns.Qualifiers[0].Ref, ns.Qualifiers[0].Schema, ns.Qualifiers[0].Original
			b.query.hidden[len(b.query.hidden)-1] = q
		} else if _, values := n.Input.(*parser.ValuesQuery); values {
			b.query.hidden[len(b.query.hidden)-1] = q
		} else if _, derived := n.Input.(*parser.DerivedQuery); derived {
			b.query.hidden[len(b.query.hidden)-1] = q
		} else if _, function := n.Input.(*parser.FunctionTable); function {
			q.ScalarAlias = ns.Qualifiers[0].ScalarAlias
			b.query.hidden[len(b.query.hidden)-1] = q
		} else if _, function := n.Input.(*parser.FunctionFrom); function {
			q.ScalarAlias = ns.Qualifiers[0].ScalarAlias
			b.query.hidden[len(b.query.hidden)-1] = q
		} else {
			q.ExcludeColumnDiagnostics = true
			b.query.hidden = append(b.query.hidden, q)
		}
		ns.Qualifiers = []queryQualifier{q}
		return root, ns, nil
	case *parser.JoinReference:
		joinOrdinal := b.stored.joinCount
		inherited := b.query.namespace
		b.stored.joinCount++
		left, lns, err := b.BindFrom(n.Left)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		oldStringForbidden := b.clause.stringFromForbidden
		oldNamespace := b.query.namespace
		rightNamespace := lns
		prefix := 0
		if inherited != nil {
			prefix = len(inherited.Frame)
			rightNamespace, err = combineQueryNamespace(*inherited, rebaseQueryNamespace(lns, prefix))
			if err != nil {
				return nil, queryNamespace{}, err
			}
		}
		b.clause.stringFromForbidden = oldStringForbidden || n.Kind == parser.JoinRight || n.Kind == parser.JoinFull
		b.query.namespace = &rightNamespace
		right, rns, err := b.BindFrom(n.Right)
		b.query.namespace = oldNamespace
		b.clause.stringFromForbidden = oldStringForbidden
		if err != nil {
			return nil, queryNamespace{}, err
		}
		rns = rebaseQueryNamespace(rns, len(lns.Frame))
		ns, err := combineQueryNamespace(lns, rns)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		// Only captures of this join's local left operand forbid RIGHT/FULL.
		// Ancestor-prefix captures remain free for an enclosing boundary; a
		// descendant's internally satisfied correlation is not exported here.
		dependent, inheritedDemand := false, false
		var forbidden plan.FunctionCorrelation
		captures := append([]plan.FunctionCorrelation(nil), functionFromCaptures(left)...)
		for _, capture := range functionFromCaptures(right) {
			if capture.Frame != b.FrameID() || capture.Slot < prefix {
				captures = append(captures, capture)
				inheritedDemand = inheritedDemand || capture.Frame == b.FrameID()
				continue
			}
			if capture.Slot >= prefix+len(lns.Frame) {
				return nil, queryNamespace{}, fmt.Errorf("stuntgres: invalid function correlation slot")
			}
			if !dependent || capture.Pos.Offset < forbidden.Pos.Offset {
				forbidden = capture
			}
			dependent = true
		}
		if dependent && (n.Kind == parser.JoinRight || n.Kind == parser.JoinFull) {
			e := expr.ErrorAt("42P10", "invalid reference to FROM-clause entry for table "+catalog.QuoteIdentifier(forbidden.Name), forbidden.Pos).(*sqlstate.Error)
			e.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
			return nil, queryNamespace{}, e
		}
		if prefix != 0 && !inheritedDemand {
			right, err = RebaseFunctionCorrelation(b.bindingCatalog(), right, b.FrameID(), prefix)
			if err != nil {
				return nil, queryNamespace{}, err
			}
			prefix = 0
		}
		join := &plan.JoinNode{QuerySchema: ns.Frame, Kind: n.Kind, Left: left, Right: right, LeftWidth: len(lns.Frame), RightWidth: len(rns.Frame), ViewOrdinal: joinOrdinal, CorrelatedRight: dependent, Frame: b.FrameID(), CorrelationPrefix: prefix, Captures: captures}
		old := b.query.namespace
		b.query.namespace = &ns
		defer func() { b.query.namespace = old }()
		if n.On != nil {
			b.clause.aggregateClause = "JOIN conditions"
			join.Predicate, err = b.bindBoolean(n.On, filterContext, "JOIN/ON")
			if err != nil {
				return nil, queryNamespace{}, err
			}
			if b.params.noParameters || b.consumer.materializedDefinition {
				if err := b.captureViewLiterals(n.On, join.Predicate, analyze.ViewJoinLiteral, joinOrdinal); err != nil {
					return nil, queryNamespace{}, err
				}
			}
		}
		keys := n.Using
		if n.Natural {
			for _, l := range lns.Columns {
				for _, r := range rns.Columns {
					if l.Descriptor.Name == r.Descriptor.Name {
						keys = append(keys, parser.Identifier{Text: l.Descriptor.Name})
						break
					}
				}
			}
		}
		if ordinal >= 0 {
			for _, key := range keys {
				b.capture.Namespaces[ordinal].Keys = append(b.capture.Namespaces[ordinal].Keys, key.Text)
			}
		}
		if len(keys) > 0 {
			merged := make([]queryColumn, 0, len(ns.Columns))
			for i, key := range keys {
				for _, prior := range keys[:i] {
					if prior.Text == key.Text {
						return nil, queryNamespace{}, sqlError("42701", fmt.Sprintf(`column name "%s" appears more than once in USING clause`, key.Text))
					}
				}
				l, err := usingQueryColumn(lns.Columns, key.Text, "left")
				if err != nil {
					return nil, queryNamespace{}, err
				}
				r, err := usingQueryColumn(rns.Columns, key.Text, "right")
				if err != nil {
					return nil, queryNamespace{}, err
				}
				pos := parser.Position{Offset: -1}
				le, re := b.QueryColumnAt(l, pos), b.QueryColumnAt(r, pos)
				raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "="}}}, Pos: pos}
				eq, err := b.bindComparisonOperands(raw, sqltype.CompareEqual, comparisonOperand{node: le}, comparisonOperand{node: re})
				if err != nil {
					return nil, queryNamespace{}, err
				}
				if join.Predicate == nil {
					join.Predicate = eq
				} else {
					join.Predicate = plan.LogicalExpr{Info: b.info(b.typ(sqltype.Bool).Usage(), pos, expr.Properties{RowDependent: true}), Op: parser.LogicalAnd, Left: join.Predicate, Right: eq}
				}
				operands := []comparisonOperand{{node: le}, {node: re}}
				usage, err := commonExpressionUsage(operands, b.typ(sqltype.Text).Usage(), "JOIN/USING")
				if err != nil {
					return nil, queryNamespace{}, err
				}
				column := l
				if n.Kind == parser.JoinRight || n.Kind == parser.JoinInner && l.Descriptor.Usage != usage && r.Descriptor.Usage == usage {
					column = r
				}
				chosen := le
				if column.Slot == r.Slot {
					chosen = re
				}
				if n.Kind == parser.JoinFull || column.Descriptor.Usage != usage {
					var value expr.Expr
					if n.Kind == parser.JoinFull {
						lv, err := b.coerceConditional(comparisonOperand{node: le}, usage, "JOIN/USING")
						if err != nil {
							return nil, queryNamespace{}, err
						}
						rv, err := b.coerceConditional(comparisonOperand{node: re}, usage, "JOIN/USING")
						if err != nil {
							return nil, queryNamespace{}, err
						}
						value = plan.ConditionalExpr{Info: b.info(usage, pos, expr.Properties{RowDependent: true}), Kind: parser.Coalesce, Args: []expr.Expr{lv, rv}}
					} else {
						value, err = b.coerceConditional(comparisonOperand{node: chosen}, usage, "JOIN/USING")
						if err != nil {
							return nil, queryNamespace{}, err
						}
					}
					if value.ResultUsage() != usage {
						// A same-type common key can still lose its typmod.
						// Publish that relabel in the recipe as well as the slot.
						input, annotations := analyze.DomainCoercionInput(value, usage)
						info, err := b.coercionInfo(b.info(usage, pos, input.Properties()), input)
						if err != nil {
							return nil, queryNamespace{}, err
						}
						coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Implicit)
						if err != nil {
							return nil, queryNamespace{}, err
						}
						info.Property = analyze.CoercionProperties(input, coercionPlan)
						value = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Implicit})
					}
					column = queryColumn{Descriptor: catalog.Column{Name: key.Text, Usage: usage}, Slot: len(ns.Frame), Leaf: -1, Physical: -1, HideStar: column.HideStar}
					join.Merges = append(join.Merges, value)
					ns.Frame = append(ns.Frame, column.Descriptor)
				}
				column.Descriptor.Name = key.Text
				merged = append(merged, column)
			}
			if n.UsingAlias != nil {
				q := queryQualifier{Name: n.UsingAlias.Text, Alias: n.UsingAlias.Text, Columns: append([]queryColumn(nil), merged...), ExcludeColumnDiagnostics: true}
				for _, prior := range ns.Qualifiers {
					if prior.Name == q.Name {
						return nil, queryNamespace{}, sqlError("42712", fmt.Sprintf(`table name "%s" specified more than once`, q.Name))
					}
				}
				ns.Qualifiers = append(ns.Qualifiers, q)
			}
			for _, c := range ns.Columns {
				key := false
				for _, k := range keys {
					if k.Text == c.Descriptor.Name {
						key = true
						break
					}
				}
				if !key {
					merged = append(merged, c)
				}
			}
			ns.Columns = merged
		}
		if n.Kind != parser.JoinInner {
			// ON and USING bind against the original operands. Only the outward
			// namespace acquires nullable proofs; child/catalog descriptors stay immutable.
			nullable := func(slot int) bool {
				return n.Kind == parser.JoinFull && slot < join.LeftWidth+join.RightWidth ||
					n.Kind == parser.JoinLeft && slot >= join.LeftWidth && slot < join.LeftWidth+join.RightWidth ||
					n.Kind == parser.JoinRight && slot < join.LeftWidth
			}
			for i := range ns.Frame {
				if nullable(i) {
					ns.Frame[i].Nullable, ns.Frame[i].NullabilityKnown = true, true
				}
			}
			for i := range ns.Columns {
				c := &ns.Columns[i]
				if nullable(c.Slot) {
					c.Descriptor.Nullable, c.Descriptor.NullabilityKnown = true, true
				}
			}
			for i := range ns.Qualifiers {
				if i < len(lns.Qualifiers) && (n.Kind == parser.JoinRight || n.Kind == parser.JoinFull) || i >= len(lns.Qualifiers) && i < len(lns.Qualifiers)+len(rns.Qualifiers) && (n.Kind == parser.JoinLeft || n.Kind == parser.JoinFull) {
					ns.Qualifiers[i].NullExtended = true
				}
				copied := false
				for j, column := range ns.Qualifiers[i].Columns {
					if !nullable(column.Slot) {
						continue
					}
					if !copied {
						ns.Qualifiers[i].Columns = append([]queryColumn(nil), ns.Qualifiers[i].Columns...)
						copied = true
					}
					c := &ns.Qualifiers[i].Columns[j]
					c.Descriptor.Nullable, c.Descriptor.NullabilityKnown = true, true
				}
			}
		}
		join.QuerySchema = ns.Frame
		return join, ns, nil
	}
	return nil, queryNamespace{}, fmt.Errorf("stuntgres: invalid FROM item")
}

func usingQueryColumn(columns []queryColumn, name, side string) (queryColumn, error) {
	var result queryColumn
	count := 0
	for _, c := range columns {
		if c.Descriptor.Name == name {
			result = c
			count++
		}
	}
	if count == 0 {
		return result, sqlError("42703", fmt.Sprintf(`column "%s" specified in USING clause does not exist in %s table`, name, side))
	}
	if count > 1 {
		return result, sqlError("42702", fmt.Sprintf(`common column name "%s" appears more than once in %s table`, name, side))
	}
	return result, nil
}

func (b *expressionBinder) queryAliasVisible(name string, ref ids.ObjectRef) bool {
	if b.query.namespace != nil {
		for _, q := range b.query.namespace.Qualifiers {
			if q.Name == name && q.Ref == ref {
				return true
			}
		}
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		scope := &b.query.scopes[i]
		if !scope.allowed || scope.namespace == nil {
			continue
		}
		for _, q := range scope.namespace.Qualifiers {
			if q.Name == name && q.Ref == ref {
				return true
			}
		}
	}
	return false
}

func (b *expressionBinder) queryQualifierNeedsLateral(q queryQualifier) bool {
	if b.query.nonLateral {
		return true
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		scope := &b.query.scopes[i]
		if scope.allowed && !scope.nonLateral {
			continue
		}
		qualifiers := scope.hidden
		if len(qualifiers) == 0 && scope.namespace != nil {
			qualifiers = scope.namespace.Qualifiers
		}
		for _, candidate := range qualifiers {
			if candidate.Name == q.Name && candidate.Ref == q.Ref {
				return true
			}
		}
	}
	return false
}

// These are only the references still free at a FROM subtree boundary. A
// join's consumed local captures never escape into its enclosing join.
func functionFromCaptures(root plan.Node) []plan.FunctionCorrelation {
	switch n := root.(type) {
	case *plan.FunctionFromNode:
		return n.Captures
	case *plan.JoinNode:
		return n.Captures
	}
	return nil
}

// Strip an unused ancestor prefix from function invocation layouts only.
// Descendants with no free captures have already satisfied/normalized their
// own dependencies and must not be rebased again. Ordinary join expressions
// retain their independent output-row layout.
func RebaseFunctionCorrelation(view catalog.Read, root plan.Node, frame uint32, prefix int) (plan.Node, error) {
	captures := functionFromCaptures(root)
	needed := false
	for _, capture := range captures {
		if capture.Frame == frame {
			if capture.Slot < prefix {
				return nil, fmt.Errorf("stuntgres: captured function prefix cannot be removed")
			}
			needed = true
		}
	}
	if !needed {
		return root, nil
	}
	rebased := append([]plan.FunctionCorrelation(nil), captures...)
	for i := range rebased {
		if rebased[i].Frame == frame {
			rebased[i].Slot -= prefix
		}
	}
	switch n := root.(type) {
	case *plan.JoinNode:
		left, err := RebaseFunctionCorrelation(view, n.Left, frame, prefix)
		if err != nil {
			return nil, err
		}
		right, correlationPrefix := n.Right, n.CorrelationPrefix
		// A zero prefix means this right operand has already been normalized
		// to local-left slots. Only the left operand may still export captures.
		if n.Frame == frame && correlationPrefix != 0 {
			if correlationPrefix < prefix {
				return nil, fmt.Errorf("stuntgres: invalid nested function correlation prefix")
			}
			right, err = RebaseFunctionCorrelation(view, right, frame, prefix)
			if err != nil {
				return nil, err
			}
			correlationPrefix -= prefix
		}
		copy := *n
		copy.Left, copy.Right, copy.Captures = left, right, rebased
		copy.CorrelationPrefix = correlationPrefix
		return &copy, nil
	case *plan.FunctionFromNode:
		var rewrite func(expr.Expr) (expr.Expr, bool, error)
		rewrite = func(bound expr.Expr) (expr.Expr, bool, error) {
			// Virtual owner recipes have their own frame. The lexical walker
			// stops at their column, so only actual argument captures enter.
			used := false
			analyze.WalkQueryArgumentExpression(bound, func(node expr.Expr) {
				if column, ok := node.(plan.ColumnExpr); ok && column.Frame == frame {
					used = true
				}
			})
			if !used {
				return bound, false, nil
			}
			return mapBoundExpression(view, bound, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(query plan.Node) (plan.Node, error) {
				return plan.RewriteQueryExpressions(query, rewrite)
			}, nil, func(node expr.Expr) (expr.Expr, bool, error) {
				column, ok := node.(plan.ColumnExpr)
				if !ok {
					return node, false, nil
				}
				if column.Frame != frame {
					// Do not enter an unrelated virtual owner recipe whose
					// private frame number can coincide with this lexical one.
					return node, column.Virtual != nil, nil
				}
				if column.Slot < prefix || column.Virtual != nil && column.RowStart < prefix {
					return nil, false, fmt.Errorf("stuntgres: invalid rebased function argument slot")
				}
				column.Slot -= prefix
				if column.Virtual != nil {
					column.RowStart -= prefix
				}
				return column, true, nil
			})
		}
		copy := *n
		copy.Captures = rebased
		copy.Calls = append([]expr.Expr(nil), n.Calls...)
		for i := range copy.Calls {
			var args []expr.Expr
			for j, arg := range copy.Calls[i].(expr.Composite).Children() {
				next, changed, err := rewrite(arg)
				if err != nil {
					return nil, err
				}
				if changed {
					if args == nil {
						args = append([]expr.Expr(nil), copy.Calls[i].(expr.Composite).Children()...)
					}
					args[j] = next
				}
			}
			if args != nil {
				var err error
				copy.Calls[i], err = expr.Substitute(copy.Calls[i].(expr.Composite), expr.InfoOf(copy.Calls[i]), args)
				if err != nil {
					return nil, err
				}
			}
		}
		return &copy, nil
	}
	return nil, fmt.Errorf("stuntgres: invalid function correlation owner")
}

func (b *expressionBinder) BindMixedFunctionTable(rawCalls []parser.FunctionExpr, ordinality bool, ordinal int, definitions []parser.FunctionColumnDefinition, multiUnnest bool) (plan.Node, queryNamespace, error) {
	calls := make([]expr.Expr, len(rawCalls))
	schema := make([]catalog.Column, 0, len(calls))
	oldClause := b.clause.aggregateClause
	b.clause.aggregateClause = "FROM"
	defer func() { b.clause.aggregateClause = oldClause }()
	// Selected calls diagnose actual P/L captures after binding, rather
	// than inheriting the string family's blanket FROM lookup guard.
	oldForbidden := b.clause.stringFromForbidden
	b.clause.stringFromForbidden = false
	defer func() { b.clause.stringFromForbidden = oldForbidden }()
	// FROM arguments are query expressions even before a local FROM
	// namespace exists. An empty local frame still permits lexical outer
	// references through queryScopes, including nested correlated SRFs.
	if b.query.namespace == nil {
		b.query.namespace = &queryNamespace{}
		defer func() { b.query.namespace = nil }()
	}
	for i, raw := range rawCalls {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		if name == "string_to_table" {
			b.clause.stringFromForbidden = oldForbidden
		}
		expr, err := b.BindFunctionSource(raw)
		b.clause.stringFromForbidden = false
		if err != nil {
			return nil, queryNamespace{}, err
		}
		calls[i] = expr
	}
	if len(definitions) != 0 {
		pos := definitions[0].Name.Pos
		switch {
		case multiUnnest:
			return nil, queryNamespace{}, functionDefinitionError("UNNEST() with multiple arguments cannot have a column definition list", "Use separate UNNEST() calls inside ROWS FROM(), and attach a column definition list to each one.", pos)
		case len(calls) > 1:
			return nil, queryNamespace{}, functionDefinitionError("ROWS FROM() with multiple functions cannot have a column definition list", "Put a separate column definition list for each function inside ROWS FROM().", pos)
		case ordinality:
			return nil, queryNamespace{}, functionDefinitionError("WITH ORDINALITY cannot be used with a column definition list", "Put the column definition list inside ROWS FROM().", pos)
		}
	}
	for i, raw := range rawCalls {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		columns := raw.ColumnDefinitions
		if len(definitions) != 0 {
			if len(columns) != 0 {
				return nil, queryNamespace{}, expr.ErrorAt("42601", "multiple column definition lists are not allowed for the same function", definitions[0].Name.Pos)
			}
			columns = definitions
		}
		node, err := b.completeFunctionDefinitions(raw, calls[i], columns)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		if !exec.SourceAdmitted(node) {
			return nil, queryNamespace{}, expr.ErrorAt("0A000", "function table source is unsupported", raw.Pos)
		}
		calls[i] = node
		if node.ResultUsage().Type.BaseType().IsComposite() {
			for j := range exec.FunctionFromCallWidth(node) {
				member := node.ResultUsage().Type.BaseType().CompositeMember(j)
				schema = append(schema, catalog.Column{Name: member.Name, Usage: member.Usage})
			}
		} else {
			if out := expr.SourceOutName(node); out != "" {
				name = out
			}
			schema = append(schema, catalog.Column{Name: name, Usage: node.ResultUsage()})
		}

	}
	if ordinality {
		schema = append(schema, catalog.Column{Name: "ordinality", Usage: b.typ(sqltype.Int8).Usage()})
	}
	columns := make([]queryColumn, len(schema))
	for i, column := range schema {
		columns[i] = queryColumn{Descriptor: column, Slot: i, Leaf: -1, Physical: -1}
	}
	name := rawCalls[0].Name.Parts[len(rawCalls[0].Name.Parts)-1].Text
	q := queryQualifier{Name: name, Original: name, Columns: columns, ScalarAlias: len(calls) == 1 && !ordinality && !calls[0].ResultUsage().Type.BaseType().IsComposite()}
	if ordinal >= 0 {
		for i, raw := range rawCalls {
			bound := calls[i]
			path := viewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewFunctionLiteral, Source: ordinal, Target: i}
			at := 0
			if err := b.captureViewLiteralTree(raw, bound, path, &at); err != nil {
				return nil, queryNamespace{}, err
			}
		}
	}
	captures := b.functionCaptures(calls)
	b.query.hidden = append(b.query.hidden, q)
	return &plan.FunctionFromNode{QuerySchema: schema, Calls: calls, Ordinality: ordinality, Frame: b.FrameID(), Correlated: len(captures) != 0, Captures: captures, RegexpNamespace: true, ViewOrdinal: ordinal}, queryNamespace{Columns: columns, Qualifiers: []queryQualifier{q}, Frame: schema}, nil
}

func (q *queryScopeState) freezeViewColumns(columns []queryColumn) []viewNamespaceColumn {
	out := make([]viewNamespaceColumn, len(columns))
	for i, c := range columns {
		out[i] = viewNamespaceColumn{Name: c.Descriptor.Name, Slot: c.Slot, Leaf: c.Leaf}
		if c.Leaf >= 0 {
			out[i].Attribute = q.sources[c.Leaf].Relation.Attributes[c.Physical]
		}
	}
	return out
}
