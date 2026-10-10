package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// slots may be nil for the target-first lock-analysis checkpoint. Repeated
// partial targets compose; any repeated whole-column target remains an error.
func bindInsertTargets(relation *relationDescriptor, targets []parser.InsertTarget, slots []int) error {
	for i, target := range targets {
		slot, ok := relation.index[target.Name]
		if !ok {
			return insertError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, target.Name, relation.name), target.Pos)
		}
		for _, previous := range targets[:i] {
			if previous.Name == target.Name && (len(previous.Subscripts) == 0 || len(target.Subscripts) == 0) {
				return insertError("42701", fmt.Sprintf(`column "%s" specified more than once`, target.Name), target.Pos)
			}
		}
		if slots != nil {
			slots[i] = slot
		}
	}
	return nil
}

func (e *Engine) bindInsertSource(out *binding, binder *expressionBinder, auth AuthorizationView, s *parser.Insert) error {
	var query *BoundQuery
	var err error
	if sel := s.Source.Select; sel != nil {
		binder.queryAuth = &auth
		// INSERT's target is an error namespace entry, not a correlation
		// source. Keep it hidden when entering the shared SELECT binder.
		columns := make([]queryColumn, len(out.relation.columns))
		for i, descriptor := range out.relation.columns {
			columns[i] = queryColumn{descriptor: descriptor, slot: i, leaf: queryTargetLeaf, physical: i}
		}
		target := queryQualifier{name: out.relation.name, original: out.relation.name, ref: out.ref, columns: columns, schema: auth.catalog.state.descriptors[out.relation.namespace].Name(), lateralForbidden: true}
		binder.queryHidden = append(binder.queryHidden, target)
		query, err = binder.bindSelect(sel, true)
		out.querySources = binder.querySources
	} else {
		query, err = binder.bindValues(s.Source.Values, s.Override == parser.OverrideUser && len(out.relation.identities) != 0)
	}
	if err != nil {
		return err
	}
	project := query.Root.(*projectNode)
	width := len(project.expressions)
	if len(s.Targets) == 0 {
		if width > len(out.relation.columns) {
			return insertError("42601", "INSERT has more expressions than target columns", project.expressions[len(out.relation.columns)].SourcePosition())
		}
		out.targets = make([]int, width)
		for i := range width {
			out.targets[i] = i
		}
	} else {
		if width > len(out.targets) {
			return insertError("42601", "INSERT has more expressions than target columns", project.expressions[len(out.targets)].SourcePosition())
		}
		if width < len(out.targets) {
			return insertError("42601", "INSERT has more target columns than expressions", s.Targets[width].Pos)
		}
	}
	schemaDetached := false
	for i, expr := range project.expressions {
		target := out.relation.columns[out.targets[i]]
		var partial *arrayAssignmentTarget
		if len(out.arrayTargets) != 0 {
			partial = out.arrayTargets[i]
			if partial != nil {
				target = partial.column(target)
			}
		}
		if _, generated := out.relation.generations[out.relation.attributes[out.targets[i]]]; generated {
			return generationWriteError(target.Name, false)
		}
		if identity, ok := out.relation.identities[out.relation.attributes[out.targets[i]]]; ok && identity.mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
			return identityInsertError(target.Name)
		}
		assigned, err := binder.assignOutput(expr, target)
		if err != nil && partial != nil {
			err = partial.diagnostic(err, expr.ResultUsage().Type)
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
		project.expressions[i] = assigned
		if usage := assigned.ResultUsage(); usage != project.querySchema[i].Usage {
			// The output schema may share backing with source nodes. Assignment
			// owns only the final projection, never source validation or set keys.
			if !schemaDetached {
				project.querySchema = append(querySchema(nil), project.querySchema...)
				schemaDetached = true
			}
			project.querySchema[i].Usage = usage
		}
	}
	// Analysis and parameter inference above remain complete. Only unused pure
	// USER identity projections lose runtime demand, before coercion preflight.
	if len(out.identityDiscard) != 0 && (s.Source.Select != nil || len(s.Source.Values.Rows) == 1) {
		for i, target := range out.targets {
			expression := project.expressions[i]
			if !out.identityDiscard[target] || expression.Properties().Volatility == Volatile {
				continue
			}
			var discard bool
			project.child, discard = pruneIdentityQueryProducer(project.child, expression)
			if discard {
				project.expressions[i] = identityUnusedOutput(expression)
			}
		}
	}
	// This binding owns the schedule; supplied query outputs, including NULL,
	// never consume defaults. Filter once rather than masking every row.
	if len(out.defaults) != 0 {
		remaining := len(out.relation.defaults) + len(out.relation.identities)
		for _, target := range out.targets {
			if len(out.identityDiscard) != 0 && out.identityDiscard[target] {
				continue
			}
			if out.defaults[target] != nil {
				out.defaults[target] = nil
				remaining--
			}
		}
		if remaining == 0 {
			out.defaults = nil
		}
	}
	out.query, out.commandQuery = query, true
	return nil
}

func typeCategory(t *sqltype.Descriptor) byte {
	if t.BaseType().IsArray() {
		return 'A'
	}
	switch t.BaseType().ID() {
	case sqltype.Bool:
		return 'B'
	case sqltype.Oid, sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8:
		return 'N'
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name:
		return 'S'
	case sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
		return 'D'
	case sqltype.Interval:
		return 'T'
	case sqltype.Bytea, sqltype.UUID:
		return 'U'
	}
	return 0
}
func preferredType(t *sqltype.Descriptor) bool {
	t = t.BaseType()
	return t.ID() == sqltype.Oid || t.ID() == sqltype.Text || t.ID() == sqltype.Float8 || t.ID() == sqltype.Bool
}
func commonValuesUsage(nodes [][]BoundExpr, raw [][]parser.Expr, column int, text sqltype.Usage) (sqltype.Usage, error) {
	return selectCommonUsage(len(nodes), func(i int) comparisonOperand {
		operand := comparisonOperand{node: nodes[i][column]}
		if raw != nil {
			operand.raw = raw[i][column]
		}
		return operand
	}, text, "VALUES", (*sqltype.Descriptor).DiagnosticName)
}
func (b *expressionBinder) bindValues(source *parser.ValuesQuery, identityDiscard bool) (*BoundQuery, error) {
	oldFrame := b.queryFrame
	if oldFrame == 0 {
		b.queryFrame = 1
		if b.nextQueryFrame == 0 {
			b.nextQueryFrame = 1
		}
	}
	defer func() { b.queryFrame = oldFrame }()
	oldNamespace, oldRelation, oldClause, oldNonLateral := b.queryNamespace, b.relation, b.aggregateClause, b.queryNonLateral
	b.queryNamespace, b.relation = &queryNamespace{}, nil
	b.aggregateClause, b.queryNonLateral = "VALUES", true
	defer func() {
		b.queryNamespace, b.relation, b.aggregateClause, b.queryNonLateral = oldNamespace, oldRelation, oldClause, oldNonLateral
	}()
	occurrence := b.viewValuesCount
	b.viewValuesCount++
	width := len(source.Rows[0])
	nodes := make([][]BoundExpr, len(source.Rows))
	for i, row := range source.Rows {
		nodes[i] = make([]BoundExpr, len(row))
		for j, raw := range row {
			if _, requested := raw.(parser.DefaultExpr); requested {
				return nil, expressionError("42601", "DEFAULT is not allowed in this context", raw.SourcePosition())
			}
			var err error
			nodes[i][j], err = b.bindOutput(raw, true)
			if err != nil {
				return nil, err
			}
		}
		if len(row) != width {
			return nil, expressionError("42601", "VALUES lists must all be the same length", parser.ExpressionStart(row[0]))
		}
	}
	schema := make([]ColumnDescriptor, width)
	projection := make([]BoundExpr, width)
	for j := range width {
		common, err := commonValuesUsage(nodes, source.Rows, j, b.typ(sqltype.Text).Usage())
		if err != nil {
			return nil, err
		}
		info := b.info(common, parser.Position{Offset: -1}, ExprProperties{RowDependent: true})
		for i := range nodes {
			node, err := b.coerceConditional(comparisonOperand{raw: source.Rows[i][j], node: nodes[i][j]}, common, "VALUES")
			if err != nil {
				return nil, err
			}
			nodes[i][j] = node
			info, err = b.collatedInfo(info, node)
			if err != nil {
				return nil, err
			}
			if b.noParameters || b.materializedDefinition {
				if err := b.captureValuesViewLiteral(source.Rows[i][j], node, viewValuesCellLiteral, occurrence, i, j); err != nil {
					return nil, err
				}
			}
		}
		common = info.usage
		schema[j] = ColumnDescriptor{Name: fmt.Sprintf("column%d", j+1), Usage: common}
		projection[j] = columnExpression{expressionInfo: info, scope: 1, frame: b.frameID(), slot: j}
	}
	values := &valuesNode{querySchema: schema, rows: nodes, viewOrdinal: occurrence}
	if identityDiscard && len(nodes) == 1 {
		values.identityOrderDemand = make([]bool, width)
	}
	var root LogicalNode = values
	namespace := queryNamespace{frame: schema, values: true, identityValuesOrderDemand: values.identityOrderDemand}
	names := make([]string, width)
	for i, column := range schema {
		names[i] = column.Name
		namespace.columns = append(namespace.columns, queryColumn{descriptor: column, slot: i, leaf: -1, physical: -1})
	}
	b.queryNamespace = &namespace
	order, err := b.bindOrder(source.Order, projection, names, "ORDER BY")
	if err != nil {
		return nil, err
	}
	bounds, err := b.bindQueryBounds(source.Bounds, false) // VALUES captures literals with its source-local paths below.
	if err != nil {
		return nil, err
	}
	if b.noParameters || b.materializedDefinition {
		if source.Order != nil {
			for i, key := range source.Order.Keys {
				if err := b.captureValuesViewLiteral(key.Expr, order[i].expression, viewValuesOrderLiteral, occurrence, 0, i); err != nil {
					return nil, err
				}
			}
		}
		if source.Bounds != nil {
			if err := b.captureValuesViewLiteral(source.Bounds.Offset, bounds.offset, viewValuesOffsetLiteral, occurrence, 0, 0); err != nil {
				return nil, err
			}
			if err := b.captureValuesViewLiteral(source.Bounds.Count, bounds.count, viewValuesLimitLiteral, occurrence, 0, 0); err != nil {
				return nil, err
			}
		}
	}
	root, err = b.finishOrder(root, order, projection)
	if err != nil {
		return nil, err
	}
	if bounds != nil {
		bounds.finishTies(root)
	}
	if bounds != nil {
		bounds.querySchema, bounds.child = root.Schema(), root
		root = bounds
	}
	// The output schema is owned separately: destination assignment modifies it,
	// never the common-typed VALUES input or its sort descriptor.
	results := append([]ColumnDescriptor(nil), schema...)
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection, frozenValues: true, frame: b.frameID()}}, nil
}

// VALUES exposes generated ASCII column names in one synthetic namespace.
// Match PostgreSQL's bounded, case-sensitive character-distance suggestions
// without changing ordinary relation/projection name resolution.
func valuesOrderColumnError(schema []ColumnDescriptor, name string, pos parser.Position) error {
	d := expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, name), pos).(*sqlstate.Error)
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
		distance := boundedNameDistance(column.Name, key, left, right, best)
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

// evaluateInsertQuery owns the source pin and all destination rows. Evaluation
// runs unlocked; only the complete, constrained batch reaches WriteStage.Apply.
func evaluateInsertQuery(ctx context.Context, b binding, source *querySource, parameters []sqltype.Value, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, scope *StatementScope) ([][]sqltype.Value, error) {
	defer source.close()
	project, ok := b.query.Root.(*projectNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: INSERT query lacks final projection")
	}
	// Bind uniqueness before opening the producer: opening can itself evaluate
	// rows. A target without unique indexes needs no per-row catalog recapture;
	// its epoch is still checked when the completed batch reaches Apply.
	unique := len(b.relation.uniqueConstraints) != 0
	if scope != nil {
		bound, err := scope.bindUniqueWrite(b.ref)
		if err != nil {
			return nil, err
		}
		unique = len(bound.keys) != 0
	}
	state := &queryState{source: source, parameters: parameters, input: input, output: output, locking: source.locks != nil}
	stage, err := openQueryProjectStage(ctx, state, project)
	if err != nil {
		return nil, err
	}
	return assembleInsertRows(ctx, b, &stage, -1, schema, input, output, scope, unique, parameters)
}

func assembleInsertRows(ctx context.Context, b binding, cursor RowCursor, count int, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, scope *StatementScope, unique bool, parameters []sqltype.Value) (rows [][]sqltype.Value, err error) {
	// A required Close error vetoes the batch, but never hides a primary failure.
	defer func() {
		closeErr := cursor.Close()
		if err == nil {
			if closeErr != nil {
				err = closeErr
			} else {
				err = ctx.Err()
			}
			if err != nil {
				rows = nil
			}
		}
	}()
	if count >= 0 {
		rows = make([][]sqltype.Value, 0, count)
	} else {
		rows = make([][]sqltype.Value, 0)
	}
	scratch := make([]sqltype.Value, len(b.targets))
	evaluation := EvalContext{Context: ctx, Parameters: parameters, Input: input, Output: output, statementScope: scope}
	for consumed := 0; count < 0 || consumed < count; consumed++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		present, err := cursor.Next(ctx, scratch)
		if err != nil {
			return nil, err
		}
		if !present {
			if count >= 0 {
				return nil, fmt.Errorf("stuntgres: early INSERT source EOF")
			}
			break
		}
		row := make([]sqltype.Value, len(b.relation.columns))
		for j, c := range b.relation.columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		for j, target := range b.targets {
			value := scratch[j]
			if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
				value, err = b.arrayTargets[j].apply(&evaluation, row[target], value)
				if err != nil {
					return nil, err
				}
			}
			row[target] = value
		}
		for j, expression := range b.defaults {
			if expression != nil {
				row[j], err = expression.Eval(&evaluation)
				if err != nil {
					return nil, err
				}
			}
		}
		if err := evaluateGenerations(ctx, b.relation, row, &evaluation); err != nil {
			return nil, err
		}
		for j, c := range b.relation.columns {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if row[j].IsNull() && !c.Nullable {
				return nil, notNullError(schema, b.relation.name, c.Name, row, output)
			}
		}
		if len(b.relation.checkConstraints) != 0 {
			constraint, checkErr := validateCheckRow(b.relation, row, &evaluation)
			if checkErr != nil {
				return nil, checkErr
			}
			if constraint != nil {
				if scope == nil {
					return nil, fmt.Errorf("stuntgres: CHECK disclosure requires statement authority")
				}
				return nil, scope.op.session.token.owner.rowConstraintError(scope, b, schema, "", constraint.Name, row)
			}
		}
		if unique {
			if scope == nil {
				return nil, fmt.Errorf("stuntgres: uniqueness requires statement authority")
			}
			admitted, err := scope.admitInsertRow(b.ref, row, rows)
			if err != nil {
				return nil, err
			}
			if !admitted {
				continue
			}
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func identityUnusedOutput(expression BoundExpr) BoundExpr {
	info := boundExpressionInfo(expression)
	info.properties = ExprProperties{}
	return literalExpression{
		expressionInfo: info,
		value:          sqltype.Null(expression.ResultUsage().Type),
	}
}

// The final output can refer to a VALUES cell or an expression moved into the
// existing count/ORDER cache. Remove only that output's pure producer; ORDER
// keys and multirow VALUES remain independent source consumers. Nodes are owned
// by this fresh binding, never a published descriptor or held execution plan.
func pruneIdentityQueryProducer(node LogicalNode, expression BoundExpr) (LogicalNode, bool) {
	for {
		coercion, ok := expression.(coercionExpression)
		if !ok {
			break
		}
		expression = coercion.child
	}
	switch n := node.(type) {
	case *setNode:
		return n, false // Every set retains source output/key/cardinality demand.
	case *distinctNode:
		return n, false // Every projected slot participates in source cardinality.
	case *distinctOnNode:
		return n, false // ON keys and ordering determine source winners.
	case *limitNode:
		var discard bool
		n.child, discard = pruneIdentityQueryProducer(n.child, expression)
		return n, discard
	case *rowLockNode:
		var discard bool
		n.child, discard = pruneIdentityQueryProducer(n.child, expression)
		return n, discard
	case *evaluationNode:
		if cached, ok := expression.(cachedOrderExpression); ok && cached.slot >= n.base && cached.slot < n.base+len(n.expressions) {
			slot := cached.slot - n.base
			if n.expressions[slot].Properties().Volatility == Volatile {
				return n, false
			}
			n.expressions[slot] = identityUnusedOutput(n.expressions[slot])
			// A solely discarded projection no longer needs a per-row cache.
			unused := true
			for _, producer := range n.expressions {
				literal, ok := producer.(literalExpression)
				if !ok || !literal.value.IsNull() {
					unused = false
					break
				}
			}
			if unused {
				return n.child, true
			}
			return n, true
		}
		var discard bool
		n.child, discard = pruneIdentityQueryProducer(n.child, expression)
		return n, discard
	case *sortNode:
		if cached, ok := expression.(cachedOrderExpression); ok {
			for _, key := range n.keys {
				if key.cache == cached.slot {
					return n, true
				}
			}
			slot := cached.slot - (n.cacheWidth - len(n.outputs))
			if slot >= 0 && slot < len(n.outputs) {
				if n.outputs[slot].Properties().Volatility == Volatile {
					return n, false
				}
				n.outputs[slot] = identityUnusedOutput(n.outputs[slot])
				return n, true
			}
		}
		var discard bool
		n.child, discard = pruneIdentityQueryProducer(n.child, expression)
		return n, discard
	case *valuesNode:
		column, ok := expression.(columnExpression)
		if !ok || len(n.rows) != 1 || len(n.identityOrderDemand) == 0 {
			return n, false
		}
		cell := n.rows[0][column.slot]
		if cell.Properties().Volatility == Volatile {
			return n, false
		}
		if !n.identityOrderDemand[column.slot] {
			n.rows[0][column.slot] = identityUnusedOutput(cell)
		}
		return n, true
	}
	return node, true
}
