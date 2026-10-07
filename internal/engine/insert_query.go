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
		binder.queryAuth = &auth
		query, err = binder.bindSelect(sel, true)
		out.querySources = binder.querySources
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
	// This binding owns the schedule; supplied query outputs, including NULL,
	// never consume defaults. Filter once rather than masking every row.
	if len(out.defaults) != 0 {
		remaining := len(out.relation.defaults)
		for _, target := range out.targets {
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
	switch t.ID() {
	case sqltype.Bool:
		return 'B'
	case sqltype.Oid, sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8:
		return 'N'
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name:
		return 'S'
	case sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
		return 'D'
	case sqltype.Bytea, sqltype.UUID:
		return 'U'
	}
	return 0
}
func preferredType(t *sqltype.Descriptor) bool {
	return t.ID() == sqltype.Oid || t.ID() == sqltype.Text || t.ID() == sqltype.Float8 || t.ID() == sqltype.Bool
}
func commonValuesUsage(nodes [][]BoundExpr, column int, text sqltype.Usage) (sqltype.Usage, error) {
	return selectCommonUsage(len(nodes), func(i int) comparisonOperand {
		node := nodes[i][column]
		return comparisonOperand{node: node}
	}, text, "VALUES")
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
			if _, requested := raw.(parser.DefaultExpr); requested {
				return nil, expressionError("42601", "DEFAULT is not allowed in this context", raw.SourcePosition())
			}
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
			node = coercionExpression{b.info(common, node.SourcePosition(), coercionProperties(node, plan)), node, plan, sqltype.Implicit}
			nodes[i][j] = node
		}
		schema[j] = ColumnDescriptor{Name: fmt.Sprintf("column%d", j+1), Usage: common}
		projection[j] = columnExpression{expressionInfo: b.info(common, parser.Position{Offset: -1}, ExprProperties{RowDependent: true}), scope: 1, frame: 1, slot: j}
	}
	var root LogicalNode = &valuesNode{querySchema: schema, rows: nodes}
	namespace := queryNamespace{frame: schema, values: true}
	names := make([]string, width)
	for i, column := range schema {
		names[i] = column.Name
		namespace.columns = append(namespace.columns, queryColumn{descriptor: column, slot: i, leaf: -1, physical: -1})
	}
	oldNamespace := b.queryNamespace
	b.queryNamespace = &namespace
	defer func() { b.queryNamespace = oldNamespace }()
	order, err := b.bindOrder(source.Order, projection, names)
	if err != nil {
		return nil, err
	}
	root, err = b.finishOrder(root, order, projection)
	if err != nil {
		return nil, err
	}
	// The output schema is owned separately: destination assignment modifies it,
	// never the common-typed VALUES input or its sort descriptor.
	results := append([]ColumnDescriptor(nil), schema...)
	return &BoundQuery{Root: &projectNode{querySchema: results, child: root, expressions: projection}}, nil
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
	scratch := make([]int, characters+2*(longest+1))
	key := scratch[:characters]
	i := 0
	for _, r := range name {
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
func evaluateInsertQuery(ctx context.Context, b binding, source *querySource, parameters []sqltype.Value, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) ([][]sqltype.Value, error) {
	defer source.close()
	stage, err := openQueryNode(ctx, &queryState{source: source, parameters: parameters, input: input, output: output}, b.query.Root)
	if err != nil {
		return nil, err
	}
	return assembleInsertRows(ctx, b, &stage, stage.count(), schema, input, output)
}

func assembleInsertRows(ctx context.Context, b binding, cursor RowCursor, count int, schema string, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (rows [][]sqltype.Value, err error) {
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
	evaluation := EvalContext{Context: ctx, Input: input, Output: output}
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
		for j, expression := range b.defaults {
			if expression != nil {
				row[j], err = expression.Eval(&evaluation)
				if err != nil {
					return nil, err
				}
			}
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
