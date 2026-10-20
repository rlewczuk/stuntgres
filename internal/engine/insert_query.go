package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// slots may be nil for the target-first lock-analysis checkpoint. Repeated
// partial targets compose; any repeated whole-column target remains an error.
func bindInsertTargets(relation *catalog.Relation, targets []parser.InsertTarget, slots []int) error {
	for i, target := range targets {
		slot, ok := relation.Index[target.Name]
		if !ok {
			return insertError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, target.Name, relation.Name()), target.Pos)
		}
		for _, previous := range targets[:i] {
			if previous.Name == target.Name && (len(previous.Steps) == 0 || len(target.Steps) == 0) {
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
		out.insert.subquerySource = true
		binder.queryAuth = &auth
		// INSERT's target is an error namespace entry, not a correlation
		// source. Keep it hidden when entering the shared SELECT binder.
		columns := make([]queryColumn, len(out.relation.Columns))
		for i, descriptor := range out.relation.Columns {
			columns[i] = queryColumn{descriptor: descriptor, slot: i, leaf: queryTargetLeaf, physical: i}
		}
		target := queryQualifier{name: out.relation.Name(), original: out.relation.Name(), ref: out.ref, columns: columns, schema: auth.Catalog.Get(out.relation.Namespace()).Name(), lateralForbidden: true}
		binder.queryHidden = append(binder.queryHidden, target)
		query, err = binder.bindSelect(sel, true)
		out.querySources = binder.querySources
	} else {
		out.insert.subquerySource = s.Source.Values.Order != nil || s.Source.Values.Bounds != nil
		query, err = binder.bindValues(s.Source.Values, s.Override == parser.OverrideUser && len(out.relation.ColumnMeta.Identities) != 0)
	}
	if err != nil {
		return err
	}
	project := query.Root.(*projectNode)
	width := len(project.expressions)
	if len(s.Targets) == 0 {
		if width > len(out.relation.Columns) {
			return insertError("42601", "INSERT has more expressions than target columns", project.expressions[len(out.relation.Columns)].SourcePosition())
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
		target := out.relation.Columns[out.targets[i]]
		var partial *arrayAssignmentTarget
		if len(out.arrayTargets) != 0 {
			partial = out.arrayTargets[i]
			if partial != nil {
				target = partial.column(target)
			}
		}
		if _, generated := out.relation.ColumnMeta.Generations[out.relation.Attributes[out.targets[i]]]; generated {
			return generationWriteError(target.Name, false)
		}
		if identity, ok := out.relation.ColumnMeta.Identities[out.relation.Attributes[out.targets[i]]]; ok && identity.Mode == parser.IdentityAlways && s.Override == parser.OverrideNone {
			return identityInsertError(target.Name)
		}
		assigned, err := binder.assignOutput(expr, target)
		if err != nil && partial != nil {
			err = partial.diagnostic(binder, err, expr.ResultUsage().Type)
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
	if len(out.insert.identityDiscard) != 0 && (s.Source.Select != nil || len(s.Source.Values.Rows) == 1) {
		for i, target := range out.targets {
			expression := project.expressions[i]
			if !out.insert.identityDiscard[target] || expression.Properties().Volatility == expr.Volatile {
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
	if len(out.insert.defaults) != 0 {
		remaining := 0
		for _, expression := range out.insert.defaults {
			if expression != nil {
				remaining++
			}
		}
		for _, target := range out.targets {
			if len(out.insert.identityDiscard) != 0 && out.insert.identityDiscard[target] {
				continue
			}
			if out.insert.defaults[target] != nil {
				out.insert.defaults[target] = nil
				remaining--
			}
		}
		if remaining == 0 {
			out.insert.defaults = nil
		}
	}
	out.query, out.commandQuery = query, true
	return nil
}

func commonValuesUsage(nodes [][]expr.Expr, raw [][]parser.Expr, column int, text sqltype.Usage) (sqltype.Usage, error) {
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
	oldNamespace, oldRelation, oldClause, oldNonLateral := b.queryNamespace, b.target.relation, b.clause.aggregateClause, b.queryNonLateral
	b.queryNamespace, b.target.relation = &queryNamespace{}, nil
	b.clause.aggregateClause, b.queryNonLateral = "VALUES", true
	defer func() {
		b.queryNamespace, b.target.relation, b.clause.aggregateClause, b.queryNonLateral = oldNamespace, oldRelation, oldClause, oldNonLateral
	}()
	occurrence := b.stored.valuesCount
	b.stored.valuesCount++
	width := -1
	nodes := make([][]expr.Expr, len(source.Rows))
	rawRows := make([][]parser.Expr, len(source.Rows))
	var lists []expandedExpressionList
	if b.params.noParameters || b.consumer.materializedDefinition {
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
		common, err := commonValuesUsage(nodes, rawRows, j, b.typ(sqltype.Text).Usage())
		if err != nil {
			return nil, err
		}
		info := b.info(common, parser.Position{Offset: -1}, expr.Properties{RowDependent: true})
		for i := range nodes {
			node, err := b.coerceConditional(comparisonOperand{raw: rawRows[i][j], node: nodes[i][j]}, common, "VALUES")
			if err != nil {
				return nil, err
			}
			nodes[i][j] = node
			info, err = b.collatedInfo(info, node)
			if err != nil {
				return nil, err
			}
		}
		common = info.Usage
		schema[j] = catalog.Column{Name: fmt.Sprintf("column%d", j+1), Usage: common}
		projection[j] = columnExpression{Info: info, scope: 1, frame: b.frameID(), slot: j}
	}
	if lists != nil {
		for i, written := range source.Rows {
			slot := 0
			for target, raw := range written {
				if expansion, expanded := lists[i].expansions[target]; expanded {
					path := viewLiteralPath{query: b.frameID(), clause: viewValuesCellLiteral, source: occurrence, row: i, target: target}
					ordinal := 0
					if err := b.captureCompositeExpansion(raw, expansion, path, &ordinal); err != nil {
						return nil, err
					}
					slot += len(expansion.fields)
				} else {
					if err := b.captureValuesViewLiteral(raw, nodes[i][slot], viewValuesCellLiteral, occurrence, i, target); err != nil {
						return nil, err
					}
					slot++
				}
			}
		}
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
	if b.params.noParameters || b.consumer.materializedDefinition {
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
	results := append([]catalog.Column(nil), schema...)
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection, frozenValues: true, frame: b.frameID()}}, nil
}

// VALUES exposes generated ASCII column names in one synthetic namespace.
// Match PostgreSQL's bounded, case-sensitive character-distance suggestions
// without changing ordinary relation/projection name resolution.
func valuesOrderColumnError(schema []catalog.Column, name string, pos parser.Position) error {
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
func evaluateInsertQuery(ctx context.Context, b binding, source *querySource, parameters []sqltype.Value, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, scope *StatementScope) (indexWriteBatch, error) {
	defer source.close()
	project, ok := b.query.Root.(*projectNode)
	if !ok {
		return indexWriteBatch{}, fmt.Errorf("stuntgres: INSERT query lacks final projection")
	}
	// Bind uniqueness before opening the producer: opening can itself evaluate
	// rows. A target without unique indexes needs no per-row catalog recapture;
	// its epoch is still checked when the completed batch reaches Apply.
	unique := len(b.relation.Constraints.Unique) != 0
	if scope != nil && scope.partitionInsert == nil && b.relation.RelationKind != catalog.ForeignRelation {
		bound, err := scope.bindUniqueWrite(b.ref)
		if err != nil {
			return indexWriteBatch{}, err
		}
		unique = len(bound.keys) != 0 || bound.checked
	}
	state := &queryState{source: source, parameters: parameters, input: input, output: output, locking: source.locks != nil}
	defer state.releaseMemberships()
	stage, err := openQueryProjectStage(ctx, state, project)
	if err != nil {
		return indexWriteBatch{}, err
	}
	return assembleInsertRows(ctx, b, &stage, -1, schema, input, output, scope, unique, parameters, state)
}

func assembleInsertRows(ctx context.Context, b binding, cursor RowCursor, count int, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, scope *StatementScope, unique bool, parameters []sqltype.Value, query *queryState) (rows indexWriteBatch, err error) {
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
				rows = indexWriteBatch{}
			}
		}
	}()
	if scope == nil || scope.insertReturning == nil {
		if count >= 0 {
			rows.values = make([][]sqltype.Value, 0, count)
		} else {
			rows.values = make([][]sqltype.Value, 0)
		}
	}
	scratch := make([]sqltype.Value, len(b.targets))
	defaultSource, _ := cursor.(interface{ rowDefaults() []bool })
	var copyInsert *copyInsertionState
	if producer, ok := cursor.(copyInsertionCursor); ok && scope != nil && (unique || scope.partitionInsert != nil) {
		copyInsert = newCopyInsertionState(producer, b)
		unique = false // COPY admission belongs to its genuine flush, not decode.
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, Parameters: parameters, Input: input, Output: output, statementScope: scope}, evalFrame: evalFrame{Scope: 1, Frame: 1, query: query}}
	copyInput := scope != nil && scope.op.copyInput.active.Load()
	for consumed := 0; count < 0 || consumed < count; consumed++ {
		if copyInput {
			scope.op.copyInput.decoding.Store(true)
			ctx = scope.op.Context()
		} else if err := ctx.Err(); err != nil {
			return indexWriteBatch{}, err
		}
		present, err := cursor.Next(ctx, scratch)
		if copyInput {
			scope.op.copyInput.decoding.Store(false)
			ctx = scope.op.Context()
			evaluation.Context = ctx
		}
		if err != nil {
			return indexWriteBatch{}, err
		}
		if !present {
			if count >= 0 {
				return indexWriteBatch{}, fmt.Errorf("stuntgres: early INSERT source EOF")
			}
			break
		}
		row := make([]sqltype.Value, len(b.relation.Columns))
		for j, c := range b.relation.Columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		for j, target := range b.targets {
			value := scratch[j]
			if len(b.arrayTargets) != 0 && b.arrayTargets[j] != nil {
				value, err = b.arrayTargets[j].apply(&evaluation, row[target], value)
				if err != nil {
					return indexWriteBatch{}, err
				}
			}
			row[target] = value
		}
		for j, expression := range b.insert.defaults {
			if expression != nil && (defaultSource == nil || defaultSource.rowDefaults()[j]) {
				row[j], err = b.evalDefault(&evaluation, j, expression)
				if err != nil {
					return indexWriteBatch{}, err
				}
			}
		}
		candidate := indexWriteCandidate{values: row}
		if scope != nil && scope.partitionInsert != nil {
			if err := scope.op.session.Engine().validateInsertRow(scope, b, schema, row, &evaluation); err != nil {
				return indexWriteBatch{}, err
			}
			if copyInsert != nil {
				copyInsert.record(scope, b, &candidate, len(rows.values))
				rows.append(candidate)
				if copyInsert.full() {
					if err := copyInsert.flush(scope, b, &rows, &evaluation); err != nil {
						return indexWriteBatch{}, err
					}
				}
				continue
			}
			if scope.insertReturning != nil {
				if _, err := scope.admitInsertProducer(b, &candidate, indexWriteBatch{}, &evaluation); err != nil {
					return indexWriteBatch{}, err
				}
				continue
			}
			if err := scope.checkInsertUniqueRow(b.ref, &candidate, rows); err != nil {
				return indexWriteBatch{}, err
			}
			rows.append(candidate)
			continue
		}
		if err := evaluateGenerations(ctx, b.relation, row, &evaluation); err != nil {
			return indexWriteBatch{}, err
		}
		if b.relation.RelationKind == catalog.ForeignRelation {
			rows.append(candidate)
			continue
		}
		if scope != nil {
			if err := access.RowSecurityProposal(scope.rlsProposal, scope.rlsProposalTable); err != nil {
				return indexWriteBatch{}, err
			}
		}
		for j, c := range b.relation.Columns {
			if err := ctx.Err(); err != nil {
				return indexWriteBatch{}, err
			}
			if c.Nullable {
				continue
			}
			value, err := logicalColumnValue(b.relation, j, row, &evaluation)
			if err != nil {
				return indexWriteBatch{}, err
			}
			if value.IsNull() {
				if scope == nil {
					return indexWriteBatch{}, fmt.Errorf("stuntgres: NOT NULL disclosure requires statement authority")
				}
				return indexWriteBatch{}, scope.op.session.Engine().rowConstraintError(scope, b, schema, c.Name, "", row)
			}
		}
		if len(b.relation.Constraints.Check) != 0 {
			constraint, checkErr := validateCheckRow(b.relation, row, &evaluation)
			if checkErr != nil {
				return indexWriteBatch{}, checkErr
			}
			if constraint != nil {
				if scope == nil {
					return indexWriteBatch{}, fmt.Errorf("stuntgres: CHECK disclosure requires statement authority")
				}
				return indexWriteBatch{}, scope.op.session.Engine().rowConstraintError(scope, b, schema, "", constraint.Name, row)
			}
		}
		if unique || scope != nil && scope.insertReturning != nil {
			if scope == nil {
				return indexWriteBatch{}, fmt.Errorf("stuntgres: uniqueness requires statement authority")
			}
			admitted, err := scope.admitInsertProducer(b, &candidate, rows, &evaluation)
			if err != nil {
				return indexWriteBatch{}, err
			}
			if !admitted {
				continue
			}
		}
		if b.viewWrite != nil {
			if err := scope.op.session.Engine().checkViewRow(scope, &b, row, &evaluation); err != nil {
				return indexWriteBatch{}, err
			}
		}
		if copyInsert != nil {
			copyInsert.record(scope, b, &candidate, len(rows.values))
		}
		rows.append(candidate)
		if copyInsert != nil && copyInsert.full() {
			if err := copyInsert.flush(scope, b, &rows, &evaluation); err != nil {
				return indexWriteBatch{}, err
			}
		}
	}
	if copyInsert != nil && len(copyInsert.lines) != 0 {
		if err := copyInsert.flush(scope, b, &rows, &evaluation); err != nil {
			return indexWriteBatch{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return indexWriteBatch{}, err
	}
	return rows, nil
}

func identityUnusedOutput(expression expr.Expr) expr.Expr {
	info := expr.InfoOf(expression)
	info.Property = expr.Properties{}
	return literalExpression{
		Info:  info,
		value: sqltype.Null(expression.ResultUsage().Type),
	}
}

// The final output can refer to a VALUES cell or an expression moved into the
// existing count/ORDER cache. Remove only that output's pure producer; ORDER
// keys and multirow VALUES remain independent source consumers. Nodes are owned
// by this fresh binding, never a published descriptor or held execution plan.
func pruneIdentityQueryProducer(node LogicalNode, expression expr.Expr) (LogicalNode, bool) {
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
	case *recursiveUnionNode:
		return n, false // The full recursive tuple determines fixpoint identity.
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
			if n.expressions[slot].Properties().Volatility == expr.Volatile {
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
				if n.outputs[slot].Properties().Volatility == expr.Volatile {
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
		if cell.Properties().Volatility == expr.Volatile {
			return n, false
		}
		if !n.identityOrderDemand[column.slot] {
			n.rows[0][column.slot] = identityUnusedOutput(cell)
		}
		return n, true
	}
	return node, true
}
