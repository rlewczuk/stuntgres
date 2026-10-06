package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// slots may be nil for the target-first lock-analysis checkpoint. Names are
// unique per descriptor, so duplicate target names identify duplicate slots.
func bindInsertTargets(relation *relationDescriptor, targets []parser.InsertTarget, slots []int) error {
	for i, target := range targets {
		slot, ok := relation.index[target.Name]
		if !ok {
			return insertError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, target.Name, relation.name), target.Pos)
		}
		for _, previous := range targets[:i] {
			if previous.Name == target.Name {
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
		if len(sel.Name.Parts) != 0 {
			out.sourceRef, err = e.resolveReference(auth, sel.Name)
			if err != nil {
				return err
			}
			out.sourceRelation = e.state.descriptors[out.sourceRef.Address].(*relationDescriptor)
			binder.relation, binder.ref = out.sourceRelation, out.sourceRef
		}
		query, err = binder.bindSelect(sel, true)
	} else {
		query, err = binder.bindValues(s.Source)
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
	for i, expr := range project.expressions {
		target := out.relation.columns[out.targets[i]]
		assigned, err := binder.assignOutput(expr, target)
		if err != nil {
			return err
		}
		project.expressions[i] = assigned
		project.querySchema[i].Usage = assigned.ResultUsage()
	}
	out.query, out.commandQuery = query, true
	return nil
}

func typeCategory(t *sqltype.Descriptor) byte {
	switch t.ID() {
	case sqltype.Bool:
		return 'B'
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8:
		return 'N'
	case sqltype.Text, sqltype.Name:
		return 'S'
	case sqltype.Date, sqltype.Time, sqltype.Timestamp:
		return 'D'
	case sqltype.Bytea, sqltype.UUID:
		return 'U'
	}
	return 0
}
func preferredType(t *sqltype.Descriptor) bool {
	return t.ID() == sqltype.Text || t.ID() == sqltype.Float8 || t.ID() == sqltype.Bool
}
func commonValuesUsage(nodes [][]BoundExpr, column int, text sqltype.Usage) (sqltype.Usage, error) {
	var candidate sqltype.Usage
	typmod := int32(-1)
	sameMod := true
	for _, row := range nodes {
		u := row[column].ResultUsage()
		if u.Type == nil {
			sameMod = false
			continue
		}
		if candidate.Type == nil {
			candidate = u
			typmod = u.Typmod
			continue
		}
		if u.Typmod != typmod {
			sameMod = false
		}
		if candidate.Type == u.Type {
			continue
		}
		if typeCategory(candidate.Type) != typeCategory(u.Type) {
			return sqltype.Usage{}, expressionError("42804", fmt.Sprintf("VALUES types %s and %s cannot be matched", candidate.Type.DiagnosticName(), u.Type.DiagnosticName()), row[column].SourcePosition())
		}
		if !preferredType(candidate.Type) {
			_, forward := sqltype.PlanCoercion(candidate.Type.Usage(), u.Type.Usage(), sqltype.Implicit)
			_, reverse := sqltype.PlanCoercion(u.Type.Usage(), candidate.Type.Usage(), sqltype.Implicit)
			if forward == nil && reverse != nil {
				candidate = u
			}
		}
	}
	if candidate.Type == nil {
		return text, nil
	}
	if !sameMod {
		candidate.Typmod = -1
	} else {
		candidate.Typmod = typmod
	}
	return candidate, nil
}
func (b *expressionBinder) bindValues(source *parser.InsertSource) (*BoundQuery, error) {
	width := len(source.Rows[0])
	nodes := make([][]BoundExpr, len(source.Rows))
	for i, row := range source.Rows {
		if len(row) != width {
			return nil, expressionError("42601", "VALUES lists must all be the same length", row[0].SourcePosition())
		}
		nodes[i] = make([]BoundExpr, width)
		for j, raw := range row {
			var err error
			nodes[i][j], err = b.bindOutput(raw, true)
			if err != nil {
				return nil, err
			}
		}
	}
	schema := make([]ColumnDescriptor, width)
	projection := make([]BoundExpr, width)
	for j := range width {
		common, err := commonValuesUsage(nodes, j, b.typ(sqltype.Text).Usage())
		if err != nil {
			return nil, err
		}
		for i := range nodes {
			node := nodes[i][j]
			switch n := node.(type) {
			case unknownOutput:
				node, err = b.bind(n.raw, expressionPolicy{context: projectionContext, expected: common})
			case parameterExpression:
				if n.usage.Type == nil {
					node, err = b.coerceParameter(n, common)
				}
			}
			if err != nil {
				return nil, err
			}
			if node.ResultUsage() == common {
				nodes[i][j] = node
				continue
			}
			plan, err := sqltype.PlanCoercion(node.ResultUsage(), common, sqltype.Implicit)
			if err != nil {
				return nil, expressionError("42846", fmt.Sprintf("VALUES could not convert type %s to %s", node.ResultUsage().Type.DiagnosticName(), common.Type.DiagnosticName()), node.SourcePosition())
			}
			node = coercionExpression{b.info(common, node.SourcePosition(), node.Properties()), node, plan}
			nodes[i][j] = node
		}
		schema[j] = ColumnDescriptor{Name: fmt.Sprintf("column%d", j+1), Usage: common}
		projection[j] = columnExpression{expressionInfo: b.info(common, parser.Position{Offset: -1}, ExprProperties{RowDependent: true}), scope: 1, frame: 1, slot: j}
	}
	var root LogicalNode = &valuesNode{querySchema: schema, rows: nodes}
	if order := source.Order; order != nil {
		slot := -1
		if constant := order.Constant; constant != nil {
			if constant.Sign == "+" {
				// Numeric unary plus is identity, but its operand must undergo
				// ordinary type input. A validated constant key makes all rows
				// peers, so no sort or hidden row-dependent key is needed.
				if _, err := b.bind(constant.NumericLiteral(), expressionPolicy{context: projectionContext}); err != nil {
					return nil, err
				}
			} else {
				ordinal, ok := constant.Ordinal()
				if !ok {
					return nil, expressionError("42601", "non-integer constant in ORDER BY", order.Pos)
				}
				if ordinal < 1 || ordinal > int64(width) {
					return nil, expressionError("42P10", fmt.Sprintf("ORDER BY position %d is not in select list", ordinal), order.Pos)
				}
				slot = int(ordinal) - 1
			}
		} else {
			for i, c := range schema {
				if c.Name == order.Column {
					slot = i
					break
				}
			}
			if slot < 0 {
				return nil, valuesOrderColumnError(schema, order)
			}
		}
		if slot >= 0 {
			cmp, err := sqltype.BindComparison(schema[slot].Usage.Type, schema[slot].Usage.Type)
			if err != nil {
				return nil, err
			}
			root = &sortNode{querySchema: schema, child: root, slot: slot, comparison: cmp, desc: order.Desc}
		}
	}
	// The output schema is owned separately: destination assignment modifies it,
	// never the common-typed VALUES input or its sort descriptor.
	results := append([]ColumnDescriptor(nil), schema...)
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection}}, nil
}

// VALUES exposes generated ASCII column names in one synthetic namespace.
// Match PostgreSQL's bounded, case-sensitive character-distance suggestions
// without changing ordinary relation/projection name resolution.
func valuesOrderColumnError(schema []ColumnDescriptor, order *parser.Order) error {
	d := expressionError("42703", fmt.Sprintf(`column "%s" does not exist`, order.Column), order.Pos).(*sqlstate.Error)
	if len(schema) == 0 {
		return d
	}
	best := min(3, len(order.Column)/2)
	characters := utf8.RuneCountInString(order.Column)
	longest, shortest := 0, len(schema[0].Name)
	for _, column := range schema {
		longest = max(longest, len(column.Name))
		shortest = min(shortest, len(column.Name))
	}
	if characters > longest+best || characters+best < shortest {
		return d
	}
	// One scratch allocation serves the decoded key and every candidate's DP.
	scratch := make([]int, characters+2*(longest+1))
	key := scratch[:characters]
	i := 0
	for _, r := range order.Column {
		key[i] = int(r)
		i++
	}
	left := scratch[characters : characters+longest+1]
	right := scratch[characters+longest+1:]
	var candidates [2]string
	ties := 0
	for _, column := range schema {
		distance := valuesColumnDistance(column.Name, key, left, right, best)
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

func valuesColumnDistance(name string, key, left, right []int, limit int) int {
	if len(name) > len(key)+limit || len(key) > len(name)+limit {
		return limit + 1
	}
	for j := 0; j <= len(name); j++ {
		left[j] = j
	}
	for i, r := range key {
		right[0] = i + 1
		minimum := right[0]
		for j := 1; j <= len(name); j++ {
			cost := 0
			if r != int(name[j-1]) {
				cost = 1
			}
			right[j] = min(left[j]+1, right[j-1]+1, left[j-1]+cost)
			minimum = min(minimum, right[j])
		}
		if minimum > limit {
			return limit + 1
		}
		left, right = right, left
	}
	return left[len(name)]
}

// evaluateInsertQuery owns the source pin and all destination rows. Evaluation
// runs unlocked; only the complete, constrained batch reaches WriteStage.Apply.
func evaluateInsertQuery(ctx context.Context, b binding, source *querySource, parameters []sqltype.Value, schema string, output sqltype.OutputEnvironment) ([][]sqltype.Value, error) {
	defer source.close()
	stage, err := openQueryNode(ctx, &queryState{source: source, parameters: parameters}, b.query.Root)
	if err != nil {
		return nil, err
	}
	return assembleInsertRows(ctx, b, &stage, stage.count(), schema, output)
}

func assembleInsertRows(ctx context.Context, b binding, cursor RowCursor, count int, schema string, output sqltype.OutputEnvironment) (rows [][]sqltype.Value, err error) {
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
	rows = make([][]sqltype.Value, count)
	var scratch []sqltype.Value
	if count != 0 {
		scratch = make([]sqltype.Value, len(b.targets))
	}
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		present, err := cursor.Next(ctx, scratch)
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, fmt.Errorf("stuntgres: early INSERT source EOF")
		}
		row := make([]sqltype.Value, len(b.relation.columns))
		for j, c := range b.relation.columns {
			row[j] = sqltype.Null(c.Usage.Type)
		}
		for j, target := range b.targets {
			row[target] = scratch[j]
		}
		for j, c := range b.relation.columns {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if row[j].IsNull() && !c.Nullable {
				return nil, notNullError(schema, b.relation.name, c.Name, row, output)
			}
		}
		rows[i] = row
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
