package analyze

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func bindInsertSource(out *Binding, binder *Binder, auth Authority, s *parser.Insert) error {
	var query *plan.Query
	var err error
	if sel := s.Source.Select; sel != nil {
		out.Insert.SubquerySource = true
		binder.Query.Auth = auth
		// INSERT's target is an error namespace entry, not a correlation
		// source. Keep it hidden when entering the shared SELECT binder.
		columns := make([]QueryColumn, len(out.Relation.Columns))
		for i, descriptor := range out.Relation.Columns {
			columns[i] = QueryColumn{Descriptor: descriptor, Slot: i, Leaf: QueryTargetLeaf, Physical: i}
		}
		target := QueryQualifier{Name: out.Relation.Name(), Original: out.Relation.Name(), Ref: out.Ref, Columns: columns, Schema: auth.ReadCatalog().Get(out.Relation.Namespace()).Name(), LateralForbidden: true}
		binder.Query.Hidden = append(binder.Query.Hidden, target)
		query, err = binder.BindSelect(sel, true)
		out.QuerySources = binder.Query.Sources
	} else {
		out.Insert.SubquerySource = s.Source.Values.Order != nil || s.Source.Values.Bounds != nil
		query, err = binder.BindValues(s.Source.Values, s.Override == parser.OverrideUser && len(out.Relation.ColumnMeta.Identities) != 0)
	}
	if err != nil {
		return err
	}
	project := query.Root.(*plan.ProjectNode)
	width := len(project.Expressions)
	if len(s.Targets) == 0 {
		if width > len(out.Relation.Columns) {
			return insertError("42601", "INSERT has more expressions than target columns", project.Expressions[len(out.Relation.Columns)].SourcePosition())
		}
		out.Targets = make([]int, width)
		for i := range width {
			out.Targets[i] = i
		}
	} else {
		if width > len(out.Targets) {
			return insertError("42601", "INSERT has more expressions than target columns", project.Expressions[len(out.Targets)].SourcePosition())
		}
		if width < len(out.Targets) {
			return insertError("42601", "INSERT has more target columns than expressions", s.Targets[width].Pos)
		}
	}
	schemaDetached := false
	for i, expr := range project.Expressions {
		target := out.Relation.Columns[out.Targets[i]]
		var partial *plan.ArrayAssignmentTarget
		if len(out.ArrayTargets) != 0 {
			partial = out.ArrayTargets[i]
			if partial != nil {
				target = partial.Column(target)
			}
		}
		if _, generated := out.Relation.ColumnMeta.Generations[out.Relation.Attributes[out.Targets[i]]]; generated {
			return GenerationWriteError(target.Name, false)
		}
		if identity, ok := out.Relation.ColumnMeta.Identities[out.Relation.Attributes[out.Targets[i]]]; ok && identity.Mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
			return sequence.IdentityInsertError(target.Name)
		}
		assigned, err := binder.assignOutput(expr, target)
		if err != nil && partial != nil {
			err = binder.arrayAssignmentDiagnostic(partial, err, expr.ResultUsage().Type)
		}
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && s.Source.Select != nil && partial == nil {
				// Projection expansion can change output indices. Match the
				// retained source position instead of indexing raw targets.
				for _, raw := range s.Source.Select.Targets {
					if raw.SourcePosition() == expr.SourcePosition() && parser.ContainsStringExpression(raw) {
						copy := *d
						copy.Position = strconv.Itoa(parser.ExpressionStart(raw).Character)
						err = &copy
						break
					}
				}
			}
			return err
		}
		project.Expressions[i] = assigned
		if usage := assigned.ResultUsage(); usage != project.QuerySchema[i].Usage {
			// The output schema may share backing with source nodes. Assignment
			// owns only the final projection, never source validation or set keys.
			if !schemaDetached {
				project.QuerySchema = append(plan.QuerySchema(nil), project.QuerySchema...)
				schemaDetached = true
			}
			project.QuerySchema[i].Usage = usage
		}
	}
	// Analysis and parameter inference above remain complete. Only unused pure
	// USER identity projections lose runtime demand, before coercion preflight.
	if len(out.Insert.IdentityDiscard) != 0 && (s.Source.Select != nil || len(s.Source.Values.Rows) == 1) {
		for i, target := range out.Targets {
			expression := project.Expressions[i]
			if !out.Insert.IdentityDiscard[target] || expression.Properties().Volatility == expr.Volatile {
				continue
			}
			var discard bool
			project.Child, discard = PruneIdentityQueryProducer(project.Child, expression)
			if discard {
				project.Expressions[i] = identityUnusedOutput(expression)
			}
		}
	}
	// This binding owns the schedule; supplied query outputs, including NULL,
	// never consume defaults. Filter once rather than masking every row.
	if len(out.Insert.Defaults) != 0 {
		remaining := 0
		for _, expression := range out.Insert.Defaults {
			if expression != nil {
				remaining++
			}
		}
		for _, target := range out.Targets {
			if len(out.Insert.IdentityDiscard) != 0 && out.Insert.IdentityDiscard[target] {
				continue
			}
			if out.Insert.Defaults[target] != nil {
				out.Insert.Defaults[target] = nil
				remaining--
			}
		}
		if remaining == 0 {
			out.Insert.Defaults = nil
		}
	}
	out.Query, out.CommandQuery = query, true
	return nil
}

func CommonValuesUsage(nodes [][]expr.Expr, raw [][]parser.Expr, column int, text sqltype.Usage) (sqltype.Usage, error) {
	return selectCommonUsage(len(nodes), func(i int) ComparisonOperand {
		operand := ComparisonOperand{Node: nodes[i][column]}
		if raw != nil {
			operand.raw = raw[i][column]
		}
		return operand
	}, text, "VALUES", (*sqltype.Descriptor).DiagnosticName)
}

func (b *Binder) BindValues(source *parser.ValuesQuery, identityDiscard bool) (*plan.Query, error) {
	oldFrame := b.Query.Frame
	if oldFrame == 0 {
		b.Query.Frame = 1
		if b.Query.NextQueryFrame == 0 {
			b.Query.NextQueryFrame = 1
		}
	}
	defer func() { b.Query.Frame = oldFrame }()
	oldNamespace, oldRelation, oldClause, oldNonLateral := b.Query.Namespace, b.Target.Relation, b.Clause.AggregateClause, b.Query.NonLateral
	b.Query.Namespace, b.Target.Relation = &QueryNamespace{}, nil
	b.Clause.AggregateClause, b.Query.NonLateral = "VALUES", true
	defer func() {
		b.Query.Namespace, b.Target.Relation, b.Clause.AggregateClause, b.Query.NonLateral = oldNamespace, oldRelation, oldClause, oldNonLateral
	}()
	occurrence := b.Stored.valuesCount
	b.Stored.valuesCount++
	width := -1
	nodes := make([][]expr.Expr, len(source.Rows))
	rawRows := make([][]parser.Expr, len(source.Rows))
	var lists []expandedExpressionList
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		lists = make([]expandedExpressionList, len(source.Rows))
	}
	for i, written := range source.Rows {
		list, err := b.expandRawList(written)
		if err != nil {
			return nil, err
		}
		if lists != nil {
			lists[i] = list
		}
		row, expanded := list.raw, list.nodes
		rawRows[i] = row
		nodes[i] = make([]expr.Expr, len(row))
		for j, raw := range row {
			if _, requested := raw.(parser.DefaultExpr); requested {
				return nil, expr.ErrorAt("42601", "DEFAULT is not allowed in this context", raw.SourcePosition())
			}
			if expanded != nil && expanded[j] != nil {
				nodes[i][j] = expanded[j]
			} else {
				nodes[i][j], err = b.bindOutput(raw, true)
				if err != nil {
					return nil, err
				}
			}
		}
		if width < 0 {
			width = len(row)
		}
		if len(row) != width {
			pos := parser.Position{Offset: -1}
			if len(row) != 0 {
				pos = parser.ExpressionStart(row[0])
			}
			return nil, expr.ErrorAt("42601", "VALUES lists must all be the same length", pos)
		}
	}
	schema := make([]catalog.Column, width)
	projection := make([]expr.Expr, width)
	for j := range width {
		common, err := CommonValuesUsage(nodes, rawRows, j, b.Type(sqltype.Text).Usage())
		if err != nil {
			return nil, err
		}
		info := b.Info(common, parser.Position{Offset: -1}, expr.Properties{RowDependent: true})
		for i := range nodes {
			node, err := b.CoerceConditional(ComparisonOperand{raw: rawRows[i][j], Node: nodes[i][j]}, common, "VALUES")
			if err != nil {
				return nil, err
			}
			nodes[i][j] = node
			info, err = b.CollatedInfo(info, node)
			if err != nil {
				return nil, err
			}
		}
		common = info.Usage
		schema[j] = catalog.Column{Name: fmt.Sprintf("column%d", j+1), Usage: common}
		projection[j] = plan.ColumnExpr{Info: info, Scope: 1, Frame: b.FrameID(), Slot: j}
	}
	if lists != nil {
		for i, written := range source.Rows {
			slot := 0
			for target, raw := range written {
				if expansion, expanded := lists[i].expansions[target]; expanded {
					path := ViewLiteralPath{Query: b.FrameID(), Clause: ViewValuesCellLiteral, Source: occurrence, Row: i, Target: target}
					ordinal := 0
					if err := b.captureCompositeExpansion(raw, expansion, path, &ordinal); err != nil {
						return nil, err
					}
					slot += len(expansion.Fields)
				} else {
					if err := b.captureValuesViewLiteral(raw, nodes[i][slot], ViewValuesCellLiteral, occurrence, i, target); err != nil {
						return nil, err
					}
					slot++
				}
			}
		}
	}
	values := &plan.ValuesNode{QuerySchema: schema, Rows: nodes, ViewOrdinal: occurrence}
	if identityDiscard && len(nodes) == 1 {
		values.IdentityOrderDemand = make([]bool, width)
	}
	var root plan.Node = values
	namespace := QueryNamespace{Frame: schema, Values: true, IdentityValuesOrderDemand: values.IdentityOrderDemand}
	names := make([]string, width)
	for i, column := range schema {
		names[i] = column.Name
		namespace.Columns = append(namespace.Columns, QueryColumn{Descriptor: column, Slot: i, Leaf: -1, Physical: -1})
	}
	b.Query.Namespace = &namespace
	order, err := b.BindOrder(source.Order, projection, names, "ORDER BY")
	if err != nil {
		return nil, err
	}
	bounds, err := b.bindQueryBounds(source.Bounds, false) // VALUES captures literals with its source-local paths below.
	if err != nil {
		return nil, err
	}
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		if source.Order != nil {
			for i, key := range source.Order.Keys {
				if err := b.captureValuesViewLiteral(key.Expr, order[i].Expression, ViewValuesOrderLiteral, occurrence, 0, i); err != nil {
					return nil, err
				}
			}
		}
		if source.Bounds != nil {
			if err := b.captureValuesViewLiteral(source.Bounds.Offset, bounds.Offset, ViewValuesOffsetLiteral, occurrence, 0, 0); err != nil {
				return nil, err
			}
			if err := b.captureValuesViewLiteral(source.Bounds.Count, bounds.Count, ViewValuesLimitLiteral, occurrence, 0, 0); err != nil {
				return nil, err
			}
		}
	}
	root, err = b.FinishOrder(root, order, projection)
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		bounds.FinishTies(root)
	}
	if bounds != nil {
		bounds.QuerySchema, bounds.Child = root.Schema(), root
		root = bounds
	}
	// The output schema is owned separately: destination assignment modifies it,
	// never the common-typed VALUES input or its sort descriptor.
	results := append([]catalog.Column(nil), schema...)
	return &plan.Query{Root: &plan.ProjectNode{QuerySchema: results, Child: root, Expressions: projection, FrozenValues: true, Frame: b.FrameID()}}, nil
}

// VALUES exposes generated ASCII column names in one synthetic namespace.
// Match PostgreSQL's bounded, case-sensitive character-distance suggestions
// without changing ordinary relation/projection name resolution.
func ValuesOrderColumnError(schema []catalog.Column, name string, pos parser.Position) error {
	d := expr.ErrorAt("42703", fmt.Sprintf(`column "%s" does not exist`, name), pos).(*sqlstate.Error)
	if len(schema) == 0 {
		return d
	}
	best := min(3, len(name)/2)
	characters := utf8.RuneCountInString(name)
	longest, shortest := 0, len(schema[0].Name)
	for _, column := range schema {
		longest = max(longest, len(column.Name))
		shortest = min(shortest, len(column.Name))
	}
	if characters > longest+best || characters+best < shortest {
		return d
	}
	// One scratch allocation serves the decoded key and every candidate's DP.
	scratch := make([]int, characters+2*(characters+1))
	key := scratch[:characters]
	i := 0
	for _, r := range name {
		key[i] = int(r)
		i++
	}
	left := scratch[characters : 2*characters+1]
	right := scratch[2*characters+1:]
	var candidates [2]string
	ties := 0
	for _, column := range schema {
		distance := BoundedNameDistance(column.Name, key, left, right, best)
		if distance > best {
			continue
		}
		if distance < best {
			best = distance
			ties = 0
		}
		if ties < len(candidates) {
			candidates[ties] = column.Name
		}
		ties++
	}
	switch ties {
	case 1:
		d.Hint = fmt.Sprintf(`Perhaps you meant to reference the column "*VALUES*.%s".`, candidates[0])
	case 2:
		d.Hint = fmt.Sprintf(`Perhaps you meant to reference the column "*VALUES*.%s" or the column "*VALUES*.%s".`, candidates[0], candidates[1])
	}
	return d
}

func identityUnusedOutput(expression expr.Expr) expr.Expr {
	info := expr.InfoOf(expression)
	info.Property = expr.Properties{}
	return plan.LiteralExpr{
		Info:  info,
		Value: sqltype.Null(expression.ResultUsage().Type),
	}
}

// The final output can refer to a VALUES cell or an expression moved into the
// existing count/ORDER cache. Remove only that output's pure producer; ORDER
// keys and multirow VALUES remain independent source consumers. Nodes are owned
// by this fresh binding, never a published descriptor or held execution plan.
func PruneIdentityQueryProducer(node plan.Node, expression expr.Expr) (plan.Node, bool) {
	for {
		coercion, ok := expression.(plan.CoercionExpr)
		if !ok {
			break
		}
		expression = coercion.Child
	}
	switch n := node.(type) {
	case *plan.SetNode:
		return n, false // Every set retains source output/key/cardinality demand.
	case *plan.RecursiveUnionNode:
		return n, false // The full recursive tuple determines fixpoint identity.
	case *plan.DistinctNode:
		return n, false // Every projected slot participates in source cardinality.
	case *plan.DistinctOnNode:
		return n, false // ON keys and ordering determine source winners.
	case *plan.LimitNode:
		var discard bool
		n.Child, discard = PruneIdentityQueryProducer(n.Child, expression)
		return n, discard
	case *plan.RowLockNode:
		var discard bool
		n.Child, discard = PruneIdentityQueryProducer(n.Child, expression)
		return n, discard
	case *plan.EvaluationNode:
		if cached, ok := expression.(plan.CachedOrderExpr); ok && cached.Slot >= n.Base && cached.Slot < n.Base+len(n.Expressions) {
			slot := cached.Slot - n.Base
			if n.Expressions[slot].Properties().Volatility == expr.Volatile {
				return n, false
			}
			n.Expressions[slot] = identityUnusedOutput(n.Expressions[slot])
			// A solely discarded projection no longer needs a per-row cache.
			unused := true
			for _, producer := range n.Expressions {
				literal, ok := producer.(plan.LiteralExpr)
				if !ok || !literal.Value.IsNull() {
					unused = false
					break
				}
			}
			if unused {
				return n.Child, true
			}
			return n, true
		}
		var discard bool
		n.Child, discard = PruneIdentityQueryProducer(n.Child, expression)
		return n, discard
	case *plan.SortNode:
		if cached, ok := expression.(plan.CachedOrderExpr); ok {
			for _, key := range n.Keys {
				if key.Cache == cached.Slot {
					return n, true
				}
			}
			slot := cached.Slot - (n.CacheWidth - len(n.Outputs))
			if slot >= 0 && slot < len(n.Outputs) {
				if n.Outputs[slot].Properties().Volatility == expr.Volatile {
					return n, false
				}
				n.Outputs[slot] = identityUnusedOutput(n.Outputs[slot])
				return n, true
			}
		}
		var discard bool
		n.Child, discard = PruneIdentityQueryProducer(n.Child, expression)
		return n, discard
	case *plan.ValuesNode:
		column, ok := expression.(plan.ColumnExpr)
		if !ok || len(n.Rows) != 1 || len(n.IdentityOrderDemand) == 0 {
			return n, false
		}
		cell := n.Rows[0][column.Slot]
		if cell.Properties().Volatility == expr.Volatile {
			return n, false
		}
		if !n.IdentityOrderDemand[column.Slot] {
			n.Rows[0][column.Slot] = identityUnusedOutput(cell)
		}
		return n, true
	}
	return node, true
}
