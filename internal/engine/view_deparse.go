package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type viewPresentation struct {
	deparse, pretty    bool
	wrap               int64
	path               []ObjectAddress
	columns            []ColumnDescriptor
	output             sqltype.OutputEnvironment
	nonstandardStrings bool
}

func (v *viewDefinition) selectSQL(ctx context.Context, catalog CatalogRead, presentation viewPresentation) (string, error) {
	renderer := newViewRenderer(v)
	renderer.presentation = presentation
	return renderer.selectQuerySQL(ctx, catalog, v.syntax, v.queries[v.syntax])
}

func (renderer *viewRenderer) selectQuerySQL(ctx context.Context, catalog CatalogRead, s *parser.Select, project *projectNode) (string, error) {
	var columns []ColumnDescriptor
	if s == renderer.viewDefinition.syntax {
		columns = renderer.presentation.columns
	}
	return renderer.selectQueryColumnsSQL(ctx, catalog, s, project, columns, false)
}

func (renderer *viewRenderer) selectQueryColumnsSQL(ctx context.Context, catalog CatalogRead, s *parser.Select, project *projectNode, columns []ColumnDescriptor, setOperand bool) (string, error) {
	if project == nil {
		return "", fmt.Errorf("stuntgres: missing frozen query frame")
	}
	previous, previousFrame, previousSimple := renderer.query, renderer.frame, renderer.simple
	renderer.query, renderer.frame = &BoundQuery{Root: project}, project.frame
	defer func() { renderer.query, renderer.frame, renderer.simple = previous, previousFrame, previousSimple }()
	presentation := renderer.presentation
	presentation.columns = columns
	_, renderer.simple = s.From.(*parser.TableReference)
	renderer.simple = renderer.simple && presentation.deparse
	v := renderer
	if s.Set != nil {
		set, ok := viewFromRoot(project).(*setNode)
		if !ok || set.left == nil || set.right == nil {
			return "", fmt.Errorf("stuntgres: missing frozen set query")
		}
		if columns == nil {
			columns = set.Schema()
		}
		left, err := renderer.selectQueryColumnsSQL(ctx, catalog, s.Set.Left, set.left, columns, true)
		if err != nil {
			return "", err
		}
		right, err := renderer.selectQueryColumnsSQL(ctx, catalog, s.Set.Right, set.right, columns, true)
		if err != nil {
			return "", err
		}
		// Preserve the exact binary tree, as well as any operand-local tail.
		// Undecorated leaves need no parentheses, matching ordinary view display.
		if viewSetOperandParentheses(s.Set.Left) {
			left = "(" + left + ")"
		}
		if viewSetOperandParentheses(s.Set.Right) {
			if presentation.deparse && s.Set.Right.Set != nil {
				right = "(\n        " + strings.ReplaceAll(right, "\n", "\n        ") + "\n)"
			} else {
				right = "(" + right + ")"
			}
		}
		separator := " "
		if presentation.deparse {
			separator = "\n"
		}
		operator := strings.ToUpper(set.operation)
		if set.all {
			operator += " ALL"
		}
		text := left + separator + operator + separator + right
		if presentation.deparse && s.Set.Right.Set != nil {
			text = left + separator + operator + " " + right
		}
		// Set ORDER names refer to synthetic output slots. Emit ordinals so
		// current view column overrides and duplicate labels cannot change
		// their binding when this structural SQL is replayed.
		slots := make(map[int]string, len(set.Schema()))
		for i := range set.Schema() {
			slots[i] = strconv.Itoa(i + 1)
		}
		renderer.slots[set] = slots
		return renderer.selectQueryTailSQL(ctx, catalog, s, text, presentation, setOperand)
	}
	if s.Values != nil {
		values, ok := viewFromRoot(project).(*valuesNode)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing frozen VALUES query")
		}
		renderer.values = values.viewOrdinal + 1
		text, err := renderer.valuesSQL(ctx, catalog, s.Values, values.viewOrdinal)
		if err != nil {
			return "", err
		}
		labels := make([]string, len(v.query.Root.Schema()))
		renamed := false
		for i, column := range v.query.Root.Schema() {
			name := column.Name
			if len(presentation.columns) > i {
				name = presentation.columns[i].Name
			}
			labels[i] = quoteIdentifier(name)
			renamed = renamed || name != fmt.Sprintf("column%d", i+1)
		}
		if presentation.deparse && setOperand {
			targets := make([]string, len(labels))
			for i, column := range columns {
				targets[i] = "\"*VALUES*\"." + pgIdentifier(fmt.Sprintf("column%d", i+1))
				if column.Name != fmt.Sprintf("column%d", i+1) {
					targets[i] += " AS " + pgIdentifier(column.Name)
				}
			}
			return viewTargetsSQL(targets, presentation, false) + "\n   FROM (" + text + ") \"*VALUES*\"", nil
		}
		if renamed {
			if presentation.deparse {
				targets := make([]string, len(labels))
				for i, column := range v.query.Root.Schema() {
					name := column.Name
					if len(presentation.columns) > i {
						name = presentation.columns[i].Name
					}
					targets[i] = fmt.Sprintf("column%d AS %s", i+1, pgIdentifier(name))
				}
				return viewTargetsSQL(targets, presentation, false) + "\n   FROM (" + text + ") AS __view_values", nil
			}
			text = "SELECT * FROM (" + text + ") AS __view_values(" + strings.Join(labels, ", ") + ")"
		}
		return text, nil
	}
	var from string
	var setSourceColumns []viewRenderColumn
	if s.From != nil {
		source, err := renderer.fromSQL(ctx, catalog, s.From)
		if err != nil {
			return "", err
		}
		from = source.text
		if setOperand && renderer.simple {
			root, ok := source.root.(*sourceNode)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing simple set-view source")
			}
			relation, ok := catalog.state.descriptors[root.ref.Address].(*relationDescriptor)
			if !ok {
				return "", ErrCatalogReplaced
			}
			// PostgreSQL qualifies simple source slots inside set operands.
			// Keep natural attribute labels separate from their displayed
			// qualification so unchanged labels still omit redundant AS.
			setSourceColumns = source.columns
			for _, column := range source.columns {
				source.slots[column.slot] = pgIdentifier(relation.name) + "." + pgIdentifier(column.name)
			}
		}
	}
	var targets []string
	for i, expr := range s.Targets {
		if _, star := expr.(parser.StarExpr); star {
			for _, column := range v.stars[viewLiteralPath{query: project.frame, clause: viewTargetLiteral, target: i}] {
				owner := v.frames[column.frame]
				if owner == nil {
					return "", fmt.Errorf("stuntgres: missing frozen star frame")
				}
				text, err := renderer.slotSQL(ctx, catalog, viewFromRoot(owner), column.slot)
				if err != nil {
					return "", err
				}
				targets = append(targets, text)
			}
			continue
		}
		text, err := renderer.expressionSQL(ctx, catalog, expr, viewLiteralPath{clause: viewTargetLiteral, target: i})
		if err != nil {
			return "", err
		}
		targets = append(targets, text)
	}
	for i := range targets {
		label := v.query.Root.Schema()[i].Name
		if len(presentation.columns) > i {
			label = presentation.columns[i].Name
		}
		natural := ""
		if renderer.simple {
			// Simple source slots carry current attribute names, not frozen
			// aliases; omit AS only when it does not change the result label.
			if setOperand {
				for _, column := range setSourceColumns {
					if targets[i] == renderer.slots[viewFromRoot(v.query.Root)][column.slot] {
						natural = pgIdentifier(column.name)
						break
					}
				}
			} else {
				for _, name := range renderer.slots[viewFromRoot(v.query.Root)] {
					if targets[i] == name {
						natural = name
						break
					}
				}
			}
		}
		if !presentation.deparse || natural != pgIdentifier(label) {
			identifier := quoteIdentifier
			if presentation.deparse {
				identifier = pgIdentifier
			}
			targets[i] += " AS " + identifier(label)
		}
	}
	text := viewTargetsSQL(targets, presentation, s.Distinct != nil)
	clause := " "
	if presentation.deparse {
		clause = "\n  "
	}
	if s.From != nil {
		if presentation.deparse {
			text += "\n   FROM " + from
		} else {
			text += " FROM " + from
		}
	}
	if s.Where != nil {
		part, err := renderer.expressionSQL(ctx, catalog, s.Where, viewLiteralPath{clause: viewWhereLiteral})
		if err != nil {
			return "", err
		}
		text += clause + "WHERE " + part
	}
	if len(s.GroupBy) > 0 {
		var keys []string
		for i, expr := range s.GroupBy {
			part, err := renderer.expressionSQL(ctx, catalog, expr, viewLiteralPath{clause: viewGroupLiteral, target: i})
			if err != nil {
				return "", err
			}
			keys = append(keys, part)
		}
		text += clause + "GROUP BY " + strings.Join(keys, ", ")
	}
	if s.Having != nil {
		part, err := renderer.expressionSQL(ctx, catalog, s.Having, viewLiteralPath{clause: viewHavingLiteral})
		if err != nil {
			return "", err
		}
		text += clause + "HAVING " + part
	}
	return renderer.selectQueryTailSQL(ctx, catalog, s, text, presentation, setOperand)
}

func viewSetOperandParentheses(s *parser.Select) bool {
	return s.Set != nil || s.Order != nil || s.Bounds != nil || len(s.Locking) != 0 ||
		s.Values != nil && (s.Values.Order != nil || s.Values.Bounds != nil)
}

func (renderer *viewRenderer) selectQueryTailSQL(ctx context.Context, catalog CatalogRead, s *parser.Select, text string, presentation viewPresentation, setOperand bool) (string, error) {
	clause := " "
	if presentation.deparse {
		clause = "\n  "
	}
	if s.Order != nil {
		keys := make([]string, len(s.Order.Keys))
		for i, key := range s.Order.Keys {
			path := viewLiteralPath{clause: viewOrderLiteral, target: i}
			var name string
			var err error
			if ordinal, ok := key.Expr.(parser.LiteralExpr); presentation.deparse && setOperand && s.Set == nil && ok && ordinal.Value.Kind == parser.IntegerValue {
				// Stored syntax freezes output references as ordinals. A local
				// SELECT arm displays that occurrence's original target, not its
				// projection-cache slot or an overridden result label.
				name, err = renderer.selectOutputSQL(ctx, catalog, int(ordinal.Value.Integer)-1)
			} else {
				name, err = renderer.expressionSQL(ctx, catalog, key.Expr, path)
			}
			if err != nil {
				return "", err
			}
			if key.Desc {
				name += " DESC"
			}
			switch key.Nulls {
			case parser.NullsFirst:
				name += " NULLS FIRST"
			case parser.NullsLast:
				name += " NULLS LAST"
			}
			keys[i] = name
		}
		text += clause + "ORDER BY " + strings.Join(keys, ", ")
	}
	if s.Bounds != nil {
		boundClause := clause
		if presentation.deparse && (setOperand || s.Set != nil) {
			boundClause = "\n "
		}
		for _, bound := range []struct {
			expr   parser.Expr
			clause viewLiteralClause
			name   string
		}{
			{s.Bounds.Offset, viewOffsetLiteral, "OFFSET"},
			{s.Bounds.Count, viewLimitLiteral, "LIMIT"},
		} {
			if bound.expr == nil {
				continue
			}
			part, err := renderer.expressionSQL(ctx, catalog, bound.expr, viewLiteralPath{clause: bound.clause})
			if err != nil {
				return "", err
			}
			text += boundClause + bound.name + " " + part
		}
	}
	for _, mark := range s.Locking {
		text += clause + mark.Mode
		if len(mark.Targets) != 0 {
			targets := make([]string, len(mark.Targets))
			for i, target := range mark.Targets {
				name, ok := renderer.rowTargets[target.Parts[0].Text]
				if !ok {
					return "", fmt.Errorf("stuntgres: missing view rowmark target")
				}
				targets[i] = name
			}
			text += " OF " + strings.Join(targets, ", ")
		}
		switch mark.Wait {
		case parser.RowLockNowait:
			text += " NOWAIT"
		case parser.RowLockSkip:
			text += " SKIP LOCKED"
		}
	}
	return text, nil
}

func (renderer *viewRenderer) selectOutputSQL(ctx context.Context, catalog CatalogRead, slot int) (string, error) {
	project, ok := renderer.query.Root.(*projectNode)
	if !ok || project.syntax == nil || slot < 0 || slot >= len(project.Schema()) {
		return "", fmt.Errorf("stuntgres: missing view ORDER output occurrence")
	}
	ordinal := slot + 1
	for target, raw := range project.syntax.Targets {
		path := viewLiteralPath{query: project.frame, clause: viewTargetLiteral, target: target}
		if _, star := raw.(parser.StarExpr); star {
			columns := renderer.stars[path]
			if slot < len(columns) {
				column := columns[slot]
				owner := renderer.frames[column.frame]
				if owner == nil {
					return "", fmt.Errorf("stuntgres: missing frozen star frame")
				}
				return renderer.slotSQL(ctx, catalog, viewFromRoot(owner), column.slot)
			}
			slot -= len(columns)
			continue
		}
		if slot == 0 {
			if _, literal := raw.(parser.LiteralExpr); literal {
				// A bare rendered constant can be reparsed as a different
				// ordinal (or an invalid noninteger ORDER constant).
				return strconv.Itoa(ordinal), nil
			}
			return renderer.expressionSQL(ctx, catalog, raw, path)
		}
		slot--
	}
	return "", fmt.Errorf("stuntgres: missing view ORDER target expression")
}

// Measure complete target bytes, never split identifiers, literals or expressions.
func viewTargetsSQL(targets []string, presentation viewPresentation, distinct bool) string {
	prefix := "SELECT "
	if distinct {
		prefix += "DISTINCT "
	}
	if !presentation.deparse {
		return prefix + strings.Join(targets, ", ")
	}
	var layout strings.Builder
	layout.WriteString(" " + prefix)
	lineBytes := 1 + len(prefix)
	for i, target := range targets {
		if i > 0 {
			if presentation.wrap >= 0 && (presentation.wrap == 0 || int64(lineBytes+2+len(target)) > presentation.wrap) {
				layout.WriteString(",\n    ")
				lineBytes = 4
			} else {
				layout.WriteString(", ")
				lineBytes += 2
			}
		}
		layout.WriteString(target)
		lineBytes += len(target)
	}
	return layout.String()
}

func (v *viewRenderer) expressionSQL(ctx context.Context, catalog CatalogRead, raw parser.Expr, path viewLiteralPath) (string, error) {
	path.query = v.frame
	ordinal := 0
	return v.expressionSQLAt(ctx, catalog, raw, path, &ordinal, pgRenderParent{})
}

func (v *viewRenderer) expressionSQLAt(ctx context.Context, catalog CatalogRead, raw parser.Expr, path viewLiteralPath, ordinal *int, parent pgRenderParent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path.node = *ordinal
	(*ordinal)++
	if literal, ok := v.literals[path]; ok {
		_, identity := v.sequenceRefs[raw.SourcePosition().Offset]
		if literal.ResultUsage().Type.BaseType().ID() != sqltype.Regclass || !identity {
			if v.presentation.deparse {
				r := pgRenderer{view: catalog, pretty: v.presentation.pretty, path: v.presentation.path, output: v.presentation.output, nonstandardStrings: v.presentation.nonstandardStrings}
				return r.sql(ctx, literal, pgRenderParent{})
			}
			return catalogLiteralSQL(ctx, catalog, literal)
		}
	}
	operand := func(expr parser.Expr, parent pgRenderParent) (string, error) {
		return v.expressionSQLAt(ctx, catalog, expr, path, ordinal, parent)
	}
	child := func(expr parser.Expr) (string, error) { return operand(expr, pgRenderParent{}) }
	args := func(expressions []parser.Expr) (string, error) {
		parts := make([]string, len(expressions))
		for i, expr := range expressions {
			text, err := operand(expr, pgRenderParent{functionArgument: true})
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ", "), nil
	}
	switch e := raw.(type) {
	case parser.RowExpr:
		text, err := args(e.Fields)
		if err != nil {
			return "", err
		}
		if e.Explicit {
			return "ROW(" + text + ")", nil
		}
		return "(" + text + ")", nil
	case parser.SubqueryExpr:
		left := ""
		var err error
		if e.Left != nil {
			left, err = child(e.Left)
			if err != nil {
				return "", err
			}
		}
		query, err := v.selectQuerySQL(ctx, catalog, e.Query, v.queries[e.Query])
		if err != nil {
			return "", err
		}
		if e.Kind == parser.SubqueryScalar {
			return "(" + query + ")", nil
		}
		if e.Kind == parser.SubqueryExists {
			return "EXISTS (" + query + ")", nil
		}
		op := e.Operator.Parts[len(e.Operator.Parts)-1].Text
		if len(e.Operator.Parts) == 2 {
			op = "OPERATOR(" + quoteIdentifier(e.Operator.Parts[0].Text) + "." + op + ")"
		}
		quantifier := "ANY"
		if e.Kind == parser.SubqueryAll {
			quantifier = "ALL"
		}
		text := "(" + left + " " + op + " " + quantifier + " (" + query + "))"
		if e.Negate {
			text = "(NOT " + text + ")"
		}
		return text, nil
	case parser.IdentityExpr:
		return e.Name, nil
	case parser.CurrentDateTimeExpr:
		name := e.Kind.Name()
		if name == "" || e.HasPrecision && (e.Kind == parser.CurrentDate || e.Precision < 0) {
			return "", fmt.Errorf("stuntgres: invalid current datetime syntax")
		}
		if e.HasPrecision {
			name += "(" + strconv.FormatInt(int64(min(e.Precision, 6)), 10) + ")"
		}
		return name, nil
	case parser.LiteralExpr:
		if ref, ok := v.sequenceRefs[e.Value.Pos.Offset]; ok {
			d, exists := catalog.state.descriptors[ref.Address].(*relationDescriptor)
			if !exists {
				return "'" + strconv.FormatUint(uint64(ref.Address.ObjectID), 10) + "'", nil
			}
			name := quoteIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + quoteIdentifier(d.name)
			return "'" + strings.ReplaceAll(name, "'", "''") + "'", nil
		}
		if (path.clause == viewGroupLiteral || path.clause == viewOrderLiteral || path.clause == viewValuesOrderLiteral) && path.node == 0 && e.Value.Kind == parser.IntegerValue {
			return strconv.FormatInt(e.Value.Integer, 10), nil
		}
		return "", fmt.Errorf("stuntgres: missing analyzed view literal")
	case parser.ColumnExpr:
		column, ok := v.columns[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing view column occurrence")
		}
		owner := v.frames[column.frame]
		if owner == nil {
			return "", fmt.Errorf("stuntgres: missing view column frame")
		}
		root := viewFromRoot(owner)
		if path.clause == viewJoinLiteral && column.frame == path.query {
			root = viewJoinAt(root, path.target)
		}
		if path.clause == viewValuesOrderLiteral && column.frame == path.query {
			root = viewValuesAt(v.query.Root, path.source)
		}
		_, ownerSimple := owner.syntax.From.(*parser.TableReference)
		if v.presentation.deparse && ((column.frame == path.query && v.simple && (path.clause == viewOrderLiteral || path.clause == viewGroupLiteral)) || (column.frame != path.query && ownerSimple)) {
			source, ok := root.(*sourceNode)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing simple view source")
			}
			relation := catalog.state.descriptors[source.ref.Address].(*relationDescriptor)
			text, err := v.slotSQL(ctx, catalog, root, column.slot)
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(text, pgIdentifier(relation.name)+".") {
				return text, nil
			}
			return pgIdentifier(relation.name) + "." + text, nil
		}
		return v.slotSQL(ctx, catalog, root, column.slot)
	case parser.TypedLiteralExpr:
		return "", fmt.Errorf("stuntgres: missing analyzed typed view literal")
	case parser.TimeZoneExpr:
		value, err := child(e.Value)
		if err != nil {
			return "", err
		}
		if e.Local {
			return "((" + value + ") AT LOCAL)", nil
		}
		zone, err := child(e.Zone)
		if err != nil {
			return "", err
		}
		return "((" + value + ") AT TIME ZONE (" + zone + "))", nil
	case parser.CollateExpr:
		text, err := child(e.Child)
		if err != nil {
			return "", err
		}
		return "(" + text + " COLLATE pg_catalog." + quoteIdentifier(e.Name.Parts[len(e.Name.Parts)-1].Text) + ")", nil
	case parser.CastExpr:
		literalPath := path
		literalPath.node = *ordinal
		text, err := child(e.Child)
		if err != nil {
			return "", err
		}
		if rawLiteral, ok := e.Child.(parser.LiteralExpr); ok && (rawLiteral.Value.Kind == parser.NullValue || rawLiteral.Value.Kind == parser.StringValue) {
			if literal, ok := v.literals[literalPath]; ok && (literal.ResultUsage().Type.ID() == sqltype.Oid || literal.ResultUsage().Type.ID() == sqltype.Name) {
				// The binder types these literal children directly as the cast
				// target. OID/NAME literalSQL already supplies the canonical cast;
				// wrapping it again would grow another cast on every replay.
				return text, nil
			}
		}
		usage, ok := v.castUsages[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing analyzed view cast usage")
		}
		if v.presentation.deparse {
			r := pgRenderer{view: catalog, path: v.presentation.path, pretty: v.presentation.pretty}
			_, simpleColumn := e.Child.(parser.ColumnExpr)
			literal, capturedLiteral := v.literals[literalPath]
			simpleLiteral := capturedLiteral && pgLiteralSimple(literal, text)
			_, castChild := e.Child.(parser.CastExpr)
			nestedCast := castChild || capturedLiteral && !simpleLiteral
			return r.cast(text, usage, simpleColumn || simpleLiteral || v.presentation.pretty && parent.functionArgument && nestedCast)
		}
		declaration, err := catalogTypeDeclaration(catalog, usage)
		if err != nil {
			return "", err
		}
		return "CAST(" + text + " AS " + declaration + ")", nil
	case parser.OperatorExpr:
		spelling := e.Name.Parts[len(e.Name.Parts)-1].Text
		op := spelling
		if len(e.Name.Parts) == 2 {
			op = "OPERATOR(" + quoteIdentifier(e.Name.Parts[0].Text) + "." + op + ")"
		} else if op == "^@" {
			op = "OPERATOR(pg_catalog.^@)"
		}
		precedence := 4
		if pgOperatorPriority(spelling) != 0 || len(e.Args) == 1 {
			precedence = 5
		}
		leftParent := pgRenderParent{precedence: precedence, operator: spelling}
		if precedence == 4 {
			// Comparisons are nonassociative; predicate-valued operands retain
			// their grouping rather than becoming an unsupported comparison chain.
			leftParent = pgRenderParent{precedence: 5}
		}
		if len(e.Name.Parts) == 2 {
			// OPERATOR(schema.op) has generic SQL precedence, even for a
			// familiar arithmetic spelling. Keep operand groups conservatively.
			leftParent = pgRenderParent{precedence: 5}
		}
		leftParent.unary = len(e.Args) == 1
		left, err := operand(e.Args[0], leftParent)
		if err != nil {
			return "", err
		}
		if len(e.Args) == 1 {
			text := op + " " + left
			if !v.presentation.pretty || pgOperatorGrouped(spelling, false, parent) {
				text = "(" + text + ")"
			}
			return text, nil
		}
		rightParent := leftParent
		rightParent.right = true
		right, err := operand(e.Args[1], rightParent)
		if err != nil {
			return "", err
		}
		text := left + " " + op + " " + right
		grouped := !v.presentation.pretty || parent.precedence > precedence
		if v.presentation.pretty && precedence == 5 {
			grouped = pgOperatorGrouped(spelling, true, parent)
		}
		if v.presentation.pretty && len(e.Name.Parts) == 2 && parent.precedence != 0 {
			grouped = true
		}
		if grouped {
			text = "(" + text + ")"
		}
		return text, nil
	case parser.LogicalExpr:
		op, precedence := "AND", 2
		if e.Op == parser.LogicalOr {
			op, precedence = "OR", 1
		} else if e.Op == parser.LogicalNot {
			op, precedence = "NOT", 3
		}
		left, err := operand(e.Left, pgRenderParent{precedence: precedence, operator: op})
		if err != nil {
			return "", err
		}
		text := op + " " + left
		if e.Op != parser.LogicalNot {
			right, err := operand(e.Right, pgRenderParent{precedence: precedence, operator: op, right: true})
			if err != nil {
				return "", err
			}
			text = left + " " + op + " " + right
		}
		if !v.presentation.pretty || parent.precedence > precedence || e.Op == parser.LogicalNot && parent.operator == "NOT" {
			text = "(" + text + ")"
		}
		return text, nil
	case parser.PredicateExpr:
		left, err := operand(e.Left, pgRenderParent{precedence: 5})
		if err != nil {
			return "", err
		}
		operators := []string{"IS NULL", "IS NOT NULL", "IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN", "IS DISTINCT FROM", "IS NOT DISTINCT FROM"}
		text := left + " " + operators[e.Kind]
		if e.Right != nil {
			right, err := operand(e.Right, pgRenderParent{precedence: 5})
			if err != nil {
				return "", err
			}
			text += " " + right
		}
		if !v.presentation.pretty || parent.precedence > 4 {
			text = "(" + text + ")"
		}
		return text, nil
	case parser.AggregateExpr:
		name := e.Name
		if e.Schema != nil {
			name = quoteIdentifier(e.Schema.Text) + "." + quoteIdentifier(e.Name)
		}
		var text string
		var err error
		if e.Star {
			text = "*"
		} else {
			text, err = args(e.Args)
			if err != nil {
				return "", err
			}
			if e.Distinct {
				text = "DISTINCT " + text
			}
		}
		if len(e.Order) > 0 {
			text += " ORDER BY "
			for i, key := range e.Order {
				if i > 0 {
					text += ", "
				}
				part, err := child(key.Expr)
				if err != nil {
					return "", err
				}
				text += part
				if key.Desc {
					text += " DESC"
				} else {
					text += " ASC"
				}
				if key.Nulls == parser.NullsFirst {
					text += " NULLS FIRST"
				} else if key.Nulls == parser.NullsLast {
					text += " NULLS LAST"
				}
			}
		}
		text = name + "(" + text + ")"
		if e.Filter != nil {
			filter, err := child(e.Filter)
			if err != nil {
				return "", err
			}
			text += " FILTER (WHERE " + filter + ")"
		}
		return text, nil
	case parser.ConditionalExpr:
		text, err := args(e.Args)
		return e.Kind.String() + "(" + text + ")", err
	case parser.CaseExpr:
		text := "CASE"
		if e.Test != nil {
			part, err := child(e.Test)
			if err != nil {
				return "", err
			}
			text += " " + part
		}
		for _, arm := range e.Arms {
			condition, err := child(arm.Condition)
			if err != nil {
				return "", err
			}
			result, err := child(arm.Result)
			if err != nil {
				return "", err
			}
			text += " WHEN " + condition + " THEN " + result
		}
		if e.Else != nil {
			part, err := child(e.Else)
			if err != nil {
				return "", err
			}
			text += " ELSE " + part
		}
		return text + " END", nil
	case parser.FunctionExpr:
		name := e.Name.Parts[len(e.Name.Parts)-1].Text
		if parser.IdentityFunctionName(name) && len(e.Args) == 0 {
			// A bound identity call is a pg_catalog builtin. In particular,
			// decoded quoted keyword names cannot be replayed unqualified.
			return "pg_catalog." + name + "()", nil
		}
		if e.StringSyntax == parser.TrimStringSyntax {
			if len(e.Args) < 1 || len(e.Args) > 2 {
				return "", fmt.Errorf("stuntgres: invalid stored TRIM arity")
			}
			source, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			set := ""
			if len(e.Args) == 2 {
				set, err = child(e.Args[1])
				if err != nil {
					return "", err
				}
			}
			return trimSyntaxSQL(name, source, set, len(e.Args))
		}
		if e.Syntax == parser.SQLFunctionSyntax {
			return renderBinaryCall(name, e.Syntax, e.Args, "pg_catalog."+name, child)
		}
		if name == "position" {
			text, err := args(e.Args)
			return "pg_catalog.position(" + text + ")", err
		}
		if name == "format" && e.Variadic {
			parts := make([]string, len(e.Args))
			for i, arg := range e.Args {
				text, err := child(arg)
				if err != nil {
					return "", err
				}
				if i == len(e.Args)-1 {
					text = "VARIADIC " + text
				}
				parts[i] = text
			}
			return objectNameText(e.Name) + "(" + strings.Join(parts, ", ") + ")", nil
		}
		if name == "make_date" || name == "make_time" || name == "make_timestamp" || name == "make_timestamptz" || name == "make_interval" {
			functionName, identifier := "pg_catalog."+name, quoteIdentifier
			if v.presentation.deparse {
				functionName, identifier = name, pgIdentifier
			}
			parts := make([]string, len(e.Args))
			for i, arg := range e.Args {
				text, err := operand(arg, pgRenderParent{functionArgument: true})
				if err != nil {
					return "", err
				}
				if len(e.ArgNames) > i && e.ArgNames[i].Text != "" {
					text = identifier(e.ArgNames[i].Text) + " => " + text
				}
				parts[i] = text
			}
			return functionName + "(" + strings.Join(parts, ", ") + ")", nil
		}
		text, err := args(e.Args)
		switch name {
		case "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii", "unistr", "unicode_assigned":
			if v.presentation.deparse {
				return name + "(" + text + ")", err
			}
			return "pg_catalog." + name + "(" + text + ")", err
		case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "strpos", "overlay", "replace", "translate", "starts_with":
			return "pg_catalog." + name + "(" + text + ")", err
		}
		return objectNameText(e.Name) + "(" + text + ")", err
	case parser.CallExpr:
		text, err := args(e.Args)
		return objectNameText(e.Name) + "(" + text + ")", err
	case parser.RegclassExpr:
		text, err := child(e.Child)
		return "CAST(" + text + " AS regclass)", err
	}
	return "", fmt.Errorf("stuntgres: unsupported view deparser expression %T", raw)
}
