package stored

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Presentation struct {
	Deparse, Pretty    bool
	Wrap               int64
	Path               []ids.ObjectAddress
	Columns            []catalog.Column
	Output             sqltype.OutputEnvironment
	NonstandardStrings bool
}

func (p Presentation) identifier(name string) string {
	if !p.Deparse {
		return catalog.QuoteIdentifier(name)
	}
	return (Deparser{Output: p.Output}).Identifier(name)
}

func (v *View) SelectSQL(ctx context.Context, catalog catalog.Read, presentation Presentation) (string, error) {
	renderer := NewQueryRenderer(v)
	renderer.Presentation = presentation
	return renderer.SelectQuerySQL(ctx, catalog, v.syntax, v.queries[v.syntax])
}

func (renderer *QueryRenderer) SelectQuerySQL(ctx context.Context, view catalog.Read, s *parser.Select, project *plan.ProjectNode) (string, error) {
	var columns []catalog.Column
	if s == renderer.View.syntax {
		columns = renderer.Presentation.Columns
	}
	return renderer.selectQueryColumnsSQL(ctx, view, s, project, columns, false)
}

func (renderer *QueryRenderer) selectQueryBodyColumnsSQL(ctx context.Context, catalogView catalog.Read, s *parser.Select, project *plan.ProjectNode, columns []catalog.Column, setOperand bool) (string, error) {
	if project == nil {
		return "", fmt.Errorf("stuntgres: missing frozen query frame")
	}
	previous, previousFrame, previousSimple, previousNamespace := renderer.query, renderer.Frame, renderer.simple, renderer.namespace
	previousFunctionSources := renderer.functionSources
	renderer.query, renderer.Frame = &plan.Query{Root: project}, project.Frame
	renderer.namespace = len(renderer.namespaces)
	if frame, ok := renderer.renderFrames[project.Frame]; ok {
		renderer.namespace = frame.namespace
	}
	renderer.depth++
	defer func() {
		renderer.query, renderer.Frame, renderer.simple, renderer.namespace = previous, previousFrame, previousSimple, previousNamespace
		renderer.functionSources = previousFunctionSources
		renderer.depth--
	}()
	presentation := renderer.Presentation
	presentation.Columns = columns
	renderer.functionSources = presentation.Deparse && viewHasFunctionSource(s.From)
	_, renderer.simple = s.From.(*parser.TableReference)
	renderer.simple = renderer.simple && (presentation.Deparse || renderer.CurrentNames)
	if table, ok := s.From.(*parser.TableReference); ok && ViewCTETable(analyze.ViewFromRoot(project), table) != nil {
		renderer.simple = false
	}
	v := renderer
	if s.Set != nil {
		set := QuerySetNode(analyze.ViewFromRoot(project))
		if set == nil || set.Left == nil || set.Right == nil {
			return "", fmt.Errorf("stuntgres: missing frozen set query")
		}
		if columns == nil {
			columns = set.Schema()
		}
		if recursive, ok := analyze.ViewFromRoot(project).(*plan.RecursiveUnionNode); ok && (recursive.Search != nil || recursive.Cycle != nil) {
			// Symbolic clauses regenerate outputs and hidden parent carries.
			columns = columns[:recursive.Definition.UserWidth]
		}
		left, err := renderer.selectQueryColumnsSQL(ctx, catalogView, s.Set.Left, set.Left, columns, true)
		if err != nil {
			return "", err
		}
		right, err := renderer.selectQueryColumnsSQL(ctx, catalogView, s.Set.Right, set.Right, columns, true)
		if err != nil {
			return "", err
		}
		// Preserve the exact binary tree, as well as any operand-local tail.
		// Undecorated leaves need no parentheses, matching ordinary view display.
		if viewSetOperandParentheses(s.Set.Left) {
			left = "(" + left + ")"
		}
		if viewSetOperandParentheses(s.Set.Right) {
			if presentation.Deparse && s.Set.Right.Set != nil {
				right = "(\n        " + strings.ReplaceAll(right, "\n", "\n        ") + "\n)"
			} else {
				right = "(" + right + ")"
			}
		}
		separator := " "
		if presentation.Deparse {
			separator = "\n"
		}
		operator := strings.ToUpper(set.Operation)
		if set.All {
			operator += " ALL"
		}
		text := left + separator + operator + separator + right
		if presentation.Deparse && s.Set.Right.Set != nil {
			text = left + separator + operator + " " + right
		}
		// Set ORDER names refer to synthetic output slots. Emit ordinals so
		// current view column overrides and duplicate labels cannot change
		// their binding when this structural SQL is replayed.
		slots := make(map[int]string, len(set.Schema()))
		for i := range set.Schema() {
			slots[i] = strconv.Itoa(i + 1)
		}
		renderer.Slots[set] = slots
		return renderer.selectQueryTailSQL(ctx, catalogView, s, text, presentation, setOperand)
	}
	if s.Values != nil {
		values, ok := analyze.ViewFromRoot(project).(*plan.ValuesNode)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing frozen VALUES query")
		}
		text, err := renderer.valuesSQL(ctx, catalogView, s.Values, values.ViewOrdinal)
		if err != nil {
			return "", err
		}
		labels := make([]string, len(v.query.Root.Schema()))
		renamed := false
		for i, column := range v.query.Root.Schema() {
			name := column.Name
			if len(presentation.Columns) > i {
				name = presentation.Columns[i].Name
			}
			labels[i] = catalog.QuoteIdentifier(name)
			renamed = renamed || name != fmt.Sprintf("column%d", i+1)
		}
		if presentation.Deparse && setOperand {
			targets := make([]string, len(labels))
			for i, column := range columns {
				targets[i] = "\"*VALUES*\"." + presentation.identifier(fmt.Sprintf("column%d", i+1))
				if column.Name != fmt.Sprintf("column%d", i+1) {
					targets[i] += " AS " + presentation.identifier(column.Name)
				}
			}
			return viewTargetsSQL(targets, presentation, false) + "\n   FROM (" + text + ") \"*VALUES*\"", nil
		}
		if renamed {
			if presentation.Deparse {
				targets := make([]string, len(labels))
				for i, column := range v.query.Root.Schema() {
					name := column.Name
					if len(presentation.Columns) > i {
						name = presentation.Columns[i].Name
					}
					targets[i] = fmt.Sprintf("column%d AS %s", i+1, presentation.identifier(name))
				}
				return viewTargetsSQL(targets, presentation, false) + "\n   FROM (" + text + ") AS __view_values", nil
			}
			text = "SELECT * FROM (" + text + ") AS __view_values(" + strings.Join(labels, ", ") + ")"
		}
		return text, nil
	}
	var from string
	var setSourceColumns []viewRenderColumn
	var functionSourceColumns []viewRenderColumn
	if s.From != nil {
		source, err := renderer.fromSQL(ctx, catalogView, s.From)
		if err != nil {
			return "", err
		}
		from = source.text
		if renderer.functionSources {
			functionSourceColumns = source.columns
		}
		if setOperand && renderer.simple {
			root, ok := source.root.(*plan.SourceNode)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing simple set-view source")
			}
			relation, ok := catalogView.Relation(root.Ref.Address)
			if !ok {
				return "", ids.ErrCatalogReplaced
			}
			// PostgreSQL qualifies simple source slots inside set operands.
			// Keep natural attribute labels separate from their displayed
			// qualification so unchanged labels still omit redundant AS.
			setSourceColumns = source.columns
			for _, column := range source.columns {
				source.slots[column.slot] = presentation.identifier(relation.Name()) + "." + presentation.identifier(column.name)
			}
		}
	}
	var targets []string
	for i, expr := range s.Targets {
		if _, star := expr.(parser.StarExpr); star {
			for _, column := range v.stars[analyze.ViewLiteralPath{Query: project.Frame, Clause: analyze.ViewTargetLiteral, Target: i}] {
				owner := v.frames[column.Frame]
				if owner == nil {
					return "", fmt.Errorf("stuntgres: missing frozen star frame")
				}
				text, err := renderer.SlotSQL(ctx, catalogView, analyze.ViewFromRoot(owner), column.Slot)
				if err != nil {
					return "", err
				}
				targets = append(targets, text)
			}
			continue
		}
		if expansion, ok := expr.(parser.FieldExpr); ok && expansion.Expansion {
			fields, err := renderer.compositeExpansionSQL(ctx, catalogView, expansion, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral, Target: i})
			if err != nil {
				return "", err
			}
			targets = append(targets, fields...)
			continue
		}
		text, err := renderer.ExpressionSQL(ctx, catalogView, expr, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral, Target: i})
		if err != nil {
			return "", err
		}
		targets = append(targets, text)
	}
	for i := range targets {
		label := v.query.Root.Schema()[i].Name
		if len(presentation.Columns) > i {
			label = presentation.Columns[i].Name
		}
		natural := ""
		if renderer.simple {
			// Simple source slots carry current attribute names, not frozen
			// aliases; omit AS only when it does not change the result label.
			if setOperand {
				for _, column := range setSourceColumns {
					if targets[i] == renderer.Slots[analyze.ViewFromRoot(v.query.Root)][column.slot] {
						natural = presentation.identifier(column.name)
						break
					}
				}
			} else {
				for _, name := range renderer.Slots[analyze.ViewFromRoot(v.query.Root)] {
					if targets[i] == name {
						natural = name
						break
					}
				}
			}
		}
		if renderer.functionSources {
			for _, column := range functionSourceColumns {
				if column.slot >= 0 && targets[i] == renderer.Slots[analyze.ViewFromRoot(v.query.Root)][column.slot] {
					natural = presentation.identifier(column.name)
					break
				}
			}
		}
		if !presentation.Deparse || natural != presentation.identifier(label) {
			targets[i] += " AS " + presentation.identifier(label)
		}
	}
	if len(targets) == 0 {
		targets = append(targets, "(ROW()).*")
	}
	text := viewTargetsSQL(targets, presentation, s.Distinct != nil)
	if s.Distinct != nil && len(s.Distinct.On) != 0 {
		keys := make([]string, len(s.Distinct.On))
		for i, raw := range s.Distinct.On {
			key, err := renderer.ExpressionSQL(ctx, catalogView, raw, analyze.ViewLiteralPath{Clause: analyze.ViewDistinctOnLiteral, Target: i})
			if err != nil {
				return "", err
			}
			keys[i] = key
		}
		text = strings.Replace(text, "SELECT DISTINCT ", "SELECT DISTINCT ON ("+strings.Join(keys, ", ")+") ", 1)
	}
	clause := " "
	if presentation.Deparse {
		clause = "\n  "
	}
	if s.From != nil {
		if presentation.Deparse {
			text += "\n   FROM " + from
		} else {
			text += " FROM " + from
		}
	}
	if s.Where != nil {
		part, err := renderer.ExpressionSQL(ctx, catalogView, s.Where, analyze.ViewLiteralPath{Clause: analyze.ViewWhereLiteral})
		if err != nil {
			return "", err
		}
		text += clause + "WHERE " + part
	}
	if s.GroupBy != nil {
		leaf := 0
		var render func([]parser.GroupItem) (string, error)
		render = func(items []parser.GroupItem) (string, error) {
			parts := make([]string, len(items))
			for i, item := range items {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if item.Kind == parser.GroupExpression {
					text, err := renderer.ExpressionSQL(ctx, catalogView, item.Expr, analyze.ViewLiteralPath{Clause: analyze.ViewGroupLiteral, Target: leaf})
					if err != nil {
						return "", err
					}
					leaf++
					parts[i] = text
					continue
				}
				text, err := render(item.Items)
				if err != nil {
					return "", err
				}
				switch item.Kind {
				case parser.GroupEmpty:
					parts[i] = "()"
				case parser.GroupList:
					parts[i] = "(" + text + ")"
				case parser.GroupSets:
					parts[i] = "GROUPING SETS (" + text + ")"
				case parser.GroupRollup:
					parts[i] = "ROLLUP(" + text + ")"
				case parser.GroupCube:
					parts[i] = "CUBE(" + text + ")"
				default:
					return "", fmt.Errorf("stuntgres: invalid stored grouping item")
				}
			}
			return strings.Join(parts, ", "), nil
		}
		keys, err := render(s.GroupBy.Items)
		if err != nil {
			return "", err
		}
		modifier := ""
		if s.GroupBy.Distinct {
			modifier = "DISTINCT "
		} else if s.GroupBy.All {
			modifier = "ALL "
		}
		text += clause + "GROUP BY " + modifier + keys
	}
	if s.Having != nil {
		part, err := renderer.ExpressionSQL(ctx, catalogView, s.Having, analyze.ViewLiteralPath{Clause: analyze.ViewHavingLiteral})
		if err != nil {
			return "", err
		}
		text += clause + "HAVING " + part
	}
	if len(s.Windows) > 0 {
		definitions := make([]string, len(s.Windows))
		for i, definition := range s.Windows {
			path := analyze.ViewLiteralPath{Query: renderer.Frame, Clause: analyze.ViewWindowLiteral, Target: i}
			ordinal := 0
			part, err := windowSpecSQL(definition.Spec, func(expr parser.Expr) (string, error) {
				return renderer.ExpressionSQLAt(ctx, catalogView, expr, path, &ordinal, DeparseParent{})
			})
			if err != nil {
				return "", err
			}
			definitions[i] = catalog.QuoteIdentifier(definition.Name.Text) + " AS (" + part + ")"
		}
		text += clause + "WINDOW " + strings.Join(definitions, ", ")
	}
	return renderer.selectQueryTailSQL(ctx, catalogView, s, text, presentation, setOperand)
}

func viewSetOperandParentheses(s *parser.Select) bool {
	return s.Set != nil || s.Order != nil || s.Bounds != nil || len(s.Locking) != 0 ||
		s.Values != nil && (s.Values.Order != nil || s.Values.Bounds != nil)
}

func (renderer *QueryRenderer) selectQueryTailSQL(ctx context.Context, catalog catalog.Read, s *parser.Select, text string, presentation Presentation, setOperand bool) (string, error) {
	clause := " "
	if presentation.Deparse {
		clause = "\n  "
	}
	if s.Order != nil {
		keys := make([]string, len(s.Order.Keys))
		for i, key := range s.Order.Keys {
			path := analyze.ViewLiteralPath{Clause: analyze.ViewOrderLiteral, Target: i}
			var name string
			var err error
			if ordinal, ok := key.Expr.(parser.LiteralExpr); presentation.Deparse && setOperand && s.Set == nil && ok && ordinal.Value.Kind == parser.IntegerValue {
				// Stored syntax freezes output references as ordinals. A local
				// SELECT arm displays that occurrence's original target, not its
				// projection-cache slot or an overridden result label.
				name, err = renderer.selectOutputSQL(ctx, catalog, int(ordinal.Value.Integer)-1)
			} else {
				name, err = renderer.ExpressionSQL(ctx, catalog, key.Expr, path)
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
		if presentation.Deparse && (setOperand || s.Set != nil) {
			boundClause = "\n "
		}
		for _, bound := range []struct {
			expr   parser.Expr
			clause analyze.ViewLiteralClause
			name   string
		}{
			{s.Bounds.Offset, analyze.ViewOffsetLiteral, "OFFSET"},
			{s.Bounds.Count, analyze.ViewLimitLiteral, "LIMIT"},
		} {
			if bound.expr == nil {
				continue
			}
			part, err := renderer.ExpressionSQL(ctx, catalog, bound.expr, analyze.ViewLiteralPath{Clause: bound.clause})
			if err != nil {
				return "", err
			}
			if s.Bounds.WithTies && bound.name == "LIMIT" {
				text += boundClause + "FETCH FIRST (" + part + ") ROWS WITH TIES"
			} else {
				text += boundClause + bound.name + " " + part
			}
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

func (renderer *QueryRenderer) selectOutputSQL(ctx context.Context, catalog catalog.Read, slot int) (string, error) {
	project, ok := renderer.query.Root.(*plan.ProjectNode)
	if !ok || project.Syntax == nil || slot < 0 || slot >= len(project.Schema()) {
		return "", fmt.Errorf("stuntgres: missing view ORDER output occurrence")
	}
	ordinal := slot + 1
	for target, raw := range project.Syntax.Targets {
		path := analyze.ViewLiteralPath{Query: project.Frame, Clause: analyze.ViewTargetLiteral, Target: target}
		if _, star := raw.(parser.StarExpr); star {
			columns := renderer.stars[path]
			if slot < len(columns) {
				column := columns[slot]
				owner := renderer.frames[column.Frame]
				if owner == nil {
					return "", fmt.Errorf("stuntgres: missing frozen star frame")
				}
				return renderer.SlotSQL(ctx, catalog, analyze.ViewFromRoot(owner), column.Slot)
			}
			slot -= len(columns)
			continue
		}
		if expansion, ok := raw.(parser.FieldExpr); ok && expansion.Expansion {
			fields, err := renderer.compositeExpansionSQL(ctx, catalog, expansion, path)
			if err != nil {
				return "", err
			}
			if slot < len(fields) {
				return fields[slot], nil
			}
			slot -= len(fields)
			continue
		}
		if slot == 0 {
			if _, literal := raw.(parser.LiteralExpr); literal {
				// A bare rendered constant can be reparsed as a different
				// ordinal (or an invalid noninteger ORDER constant).
				return strconv.Itoa(ordinal), nil
			}
			return renderer.ExpressionSQL(ctx, catalog, raw, path)
		}
		slot--
	}
	return "", fmt.Errorf("stuntgres: missing view ORDER target expression")
}

// Measure complete target bytes, never split identifiers, literals or expressions.
func viewTargetsSQL(targets []string, presentation Presentation, distinct bool) string {
	prefix := "SELECT "
	if distinct {
		prefix += "DISTINCT "
	}
	if !presentation.Deparse {
		return prefix + strings.Join(targets, ", ")
	}
	var layout strings.Builder
	layout.WriteString(" " + prefix)
	lineBytes := 1 + len(prefix)
	for i, target := range targets {
		if i > 0 {
			if presentation.Wrap >= 0 && (presentation.Wrap == 0 || int64(lineBytes+2+len(target)) > presentation.Wrap) {
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

func (v *QueryRenderer) ExpressionSQL(ctx context.Context, catalog catalog.Read, raw parser.Expr, path analyze.ViewLiteralPath) (string, error) {
	path.Query = v.Frame
	ordinal := 0
	return v.ExpressionSQLAt(ctx, catalog, raw, path, &ordinal, DeparseParent{})
}

// Scalar alias names and non-expanding scalar stars retain the same analyzed
// frame/slot authority as ordinary columns.
func (v *QueryRenderer) columnSQL(ctx context.Context, catalog catalog.Read, path analyze.ViewLiteralPath) (string, error) {
	column, ok := v.columns[path]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing view column occurrence")
	}
	if column.RuleSide != 0 {
		d, ok := catalog.Relation(ids.ObjectAddress{ClassID: column.RuleRef.Address.ClassID, ObjectID: column.RuleRef.Address.ObjectID})
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		name, err := DeparseAttribute(d, column.RuleRef.Address.SubID)
		if err != nil {
			return "", err
		}
		side := "OLD"
		if column.RuleSide == 2 {
			side = "NEW"
		}
		return side + "." + name, nil
	}
	owner := v.frames[column.Frame]
	if owner == nil {
		return "", fmt.Errorf("stuntgres: missing view column frame")
	}
	root := analyze.ViewFromRoot(owner)
	if path.Clause == analyze.ViewJoinLiteral && column.Frame == path.Query {
		root = viewJoinAt(root, path.Target)
	}
	if path.Clause == analyze.ViewValuesOrderLiteral && column.Frame == path.Query {
		root = viewValuesAt(v.query.Root, path.Source)
	}
	// Current simple-source names require owner qualification for correlated
	// columns and source keys that could collide with output labels.
	// Frozen expression frames have slots but no SELECT syntax.
	currentNames := v.Presentation.Deparse || v.CurrentNames
	ownerSimple := false
	if currentNames && column.Frame != path.Query && owner.Syntax != nil {
		_, ownerSimple = owner.Syntax.From.(*parser.TableReference)
	}
	if currentNames && ((column.Frame == path.Query && v.simple && (path.Clause == analyze.ViewOrderLiteral || path.Clause == analyze.ViewGroupLiteral || path.Clause == analyze.ViewDistinctOnLiteral)) || (column.Frame != path.Query && ownerSimple)) {
		source, ok := root.(*plan.SourceNode)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing simple view source")
		}
		relation := catalog.MustRelation(source.Ref.Address)
		text, err := v.SlotSQL(ctx, catalog, root, column.Slot)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(text, v.Presentation.identifier(relation.Name())+".") {
			return text, nil
		}
		return v.Presentation.identifier(relation.Name()) + "." + text, nil
	}
	return v.SlotSQL(ctx, catalog, root, column.Slot)
}

func (v *QueryRenderer) ExpressionSQLAt(ctx context.Context, catalogView catalog.Read, raw parser.Expr, path analyze.ViewLiteralPath, ordinal *int, parent DeparseParent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path.Node = *ordinal
	(*ordinal)++
	if _, captured := v.routineRefs[path]; captured && extension.IntaggRecipeFunction(raw) == "" {
		if call, ok := raw.(parser.FunctionExpr); !ok || !call.StoredSQLRoutine {
			return "", fmt.Errorf("stuntgres: mismatched frozen routine occurrence")
		}
	}
	if literal, ok := v.literals[path]; ok {
		_, identity := v.sequenceRefs[raw.SourcePosition().Offset]
		if literal.ResultUsage().Type.BaseType().ID() != sqltype.Regclass || !identity {
			if v.Presentation.Deparse {
				r := Deparser{View: catalogView, Pretty: v.Presentation.Pretty, Path: v.Presentation.Path, Output: v.Presentation.Output, NonstandardStrings: v.Presentation.NonstandardStrings}
				return r.SQL(ctx, literal, DeparseParent{})
			}
			return regobj.Literal(ctx, catalogView, literal)
		}
	}
	currentOrderedSetDirect := parent.orderedSetDirect
	operand := func(expr parser.Expr, parent DeparseParent) (string, error) {
		parent.orderedSetDirect = parent.orderedSetDirect || currentOrderedSetDirect
		childPath := path
		childPath.Argument = 0
		return v.ExpressionSQLAt(ctx, catalogView, expr, childPath, ordinal, parent)
	}
	child := func(expr parser.Expr) (string, error) { return operand(expr, DeparseParent{}) }
	args := func(expressions []parser.Expr) (string, error) {
		parts := make([]string, 0, len(expressions))
		for _, expr := range expressions {
			text, err := operand(expr, DeparseParent{functionArgument: true})
			if err != nil {
				return "", err
			}
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, ", "), nil
	}
	selectedOperand := func(index int, expr parser.Expr, parent DeparseParent) (string, error) {
		text, err := operand(expr, parent)
		if err != nil {
			return "", err
		}
		key := path
		key.Argument = index + 1
		usage, captured := v.castUsages[key]
		if !captured {
			return text, nil
		}
		if v.Presentation.Deparse {
			r := Deparser{View: catalogView, Path: v.Presentation.Path, Pretty: v.Presentation.Pretty, Output: v.Presentation.Output}
			simple := false
			switch expr.(type) {
			case parser.ColumnExpr, parser.LiteralExpr, parser.TypedLiteralExpr:
				simple = true
			case parser.CastExpr:
				simple = v.Presentation.Pretty && parent.functionArgument
			case parser.FunctionExpr:
				call := expr.(parser.FunctionExpr)
				simple = len(call.Name.Parts) != 0 && (parser.ArrayTransformFunctionName(call.Name.Parts[len(call.Name.Parts)-1].Text) || parser.ArraySearchFunctionName(call.Name.Parts[len(call.Name.Parts)-1].Text) || parser.ArrayInspectFunctionName(call.Name.Parts[len(call.Name.Parts)-1].Text))
			}
			return r.cast(text, usage, simple)
		}
		declaration, err := regobj.TypeDeclaration(catalogView, usage)
		if err != nil {
			return "", err
		}
		return "CAST(" + text + " AS " + declaration + ")", nil
	}
	if call, ok := raw.(parser.FunctionExpr); ok && call.StoredSQLRoutine {
		name, err := v.sqlRoutineRecipeName(catalogView, path, call)
		if err != nil {
			return "", err
		}
		parts := make([]string, len(call.Args))
		for i, arg := range call.Args {
			parts[i], err = selectedOperand(i, arg, DeparseParent{functionArgument: true})
			if err != nil {
				return "", err
			}
		}
		return name + "(" + strings.Join(parts, ", ") + ")", nil
	}
	switch e := raw.(type) {
	case parser.GroupingExpr:
		text, err := args(e.Args)
		if err != nil {
			return "", err
		}
		return "GROUPING(" + text + ")", nil
	case parser.StarExpr:
		if _, scalar := v.columns[path]; scalar {
			return v.columnSQL(ctx, catalogView, path)
		}
		captured, ok := v.composites[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing frozen scalar star")
		}
		base, err := v.wholeRowSQL(captured)
		if err != nil {
			return "", err
		}
		if captured.Kind == analyze.CompositeWholeRow {
			return base, nil
		}
		if captured.Kind != analyze.CompositeExpansion {
			return "", fmt.Errorf("stuntgres: invalid frozen scalar star interpretation")
		}
		fields, err := v.frozenCompositeFields(ctx, catalogView, captured, base)
		if err != nil {
			return "", err
		}
		return v.quantifiedFieldsSQL(ctx, catalogView, path, fields)
	case parser.FieldExpr:
		captured, ok := v.composites[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing frozen field interpretation")
		}
		text, err := child(e.Base)
		if err != nil {
			return "", err
		}
		if captured.Kind == analyze.CompositeDotBuiltin {
			return frozenDotBuiltinSQL(captured, text)
		}
		fields, err := v.frozenCompositeFields(ctx, catalogView, captured, text)
		if err != nil {
			return "", err
		}
		return v.quantifiedFieldsSQL(ctx, catalogView, path, fields)
	case parser.RowExpr:
		parts := make([]string, len(e.Fields))
		for i, field := range e.Fields {
			var err error
			parts[i], err = selectedOperand(i, field, DeparseParent{functionArgument: true})
			if err != nil {
				return "", err
			}
		}
		text := strings.Join(parts, ", ")
		if e.Explicit {
			return "ROW(" + text + ")", nil
		}
		return "(" + text + ")", nil
	case parser.InListExpr:
		left, err := operand(e.Left, DeparseParent{Precedence: 5})
		if err != nil {
			return "", err
		}
		items, err := args(e.Items)
		if err != nil {
			return "", err
		}
		op := "IN"
		if e.Negate {
			op = "NOT IN"
		}
		return "(" + left + " " + op + " (" + items + "))", nil
	case parser.ArrayComparisonExpr:
		left, err := operand(e.Left, DeparseParent{Precedence: 5})
		if err != nil {
			return "", err
		}
		right, err := child(e.Right)
		if err != nil {
			return "", err
		}
		op := e.Name.Parts[len(e.Name.Parts)-1].Text
		if len(e.Name.Parts) == 2 {
			op = "OPERATOR(" + catalog.QuoteIdentifier(e.Name.Parts[0].Text) + "." + op + ")"
		}
		quantifier := "ANY"
		if e.All {
			quantifier = "ALL"
		}
		return "(" + left + " " + op + " " + quantifier + " (" + right + "))", nil
	case parser.SubqueryExpr:
		left := ""
		var err error
		if e.Left != nil {
			left, err = child(e.Left)
			if err != nil {
				return "", err
			}
		}
		query, err := v.SelectQuerySQL(ctx, catalogView, e.Query, v.queries[e.Query])
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
			op = "OPERATOR(" + catalog.QuoteIdentifier(e.Operator.Parts[0].Text) + "." + op + ")"
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
			d, exists := catalogView.Relation(ref.Address)
			if !exists {
				return "'" + strconv.FormatUint(uint64(ref.Address.ObjectID), 10) + "'", nil
			}
			name := catalog.QuoteIdentifier(catalogView.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
			return "'" + strings.ReplaceAll(name, "'", "''") + "'", nil
		}
		if (path.Clause == analyze.ViewGroupLiteral || path.Clause == analyze.ViewOrderLiteral || path.Clause == analyze.ViewDistinctOnLiteral || path.Clause == analyze.ViewValuesOrderLiteral) && path.Node == 0 && e.Value.Kind == parser.IntegerValue {
			return strconv.FormatInt(e.Value.Integer, 10), nil
		}
		return "", fmt.Errorf("stuntgres: missing analyzed view literal")
	case parser.ColumnExpr:
		if captured, whole := v.composites[path]; whole {
			if captured.Kind != analyze.CompositeWholeRow {
				return "", fmt.Errorf("stuntgres: invalid frozen scalar whole row")
			}
			return v.wholeRowSQL(captured)
		}
		return v.columnSQL(ctx, catalogView, path)
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
		id, ok := v.collations[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing analyzed view collation")
		}
		name := typedef.CollationSQLName(catalogView, id)
		if v.Presentation.Deparse {
			name = CollationDisplayName(catalogView, id, v.Presentation.Path)
		}
		return "(" + text + " COLLATE " + name + ")", nil
	case parser.CastExpr:
		literalPath := path
		literalPath.Node = *ordinal
		text, err := operand(e.Child, DeparseParent{orderedSetDirect: parent.orderedSetDirect})
		if err != nil {
			return "", err
		}
		if rawLiteral, ok := e.Child.(parser.LiteralExpr); ok && (rawLiteral.Value.Kind == parser.NullValue || rawLiteral.Value.Kind == parser.StringValue) {
			if literal, ok := v.literals[literalPath]; ok && (sqltype.IsOperatorAlias(literal.ResultUsage().Type.ID()) || sqltype.IsRegTS(literal.ResultUsage().Type.ID()) || sqltype.IsTransactionIdentifier(literal.ResultUsage().Type.ID()) || literal.ResultUsage().Type.ID() == sqltype.Oid || literal.ResultUsage().Type.ID() == sqltype.Name || literal.ResultUsage().Type.ID() == sqltype.Money || (literal.ResultUsage().Type.ID() == sqltype.Regtype || sqltype.IsRegObjectAlias(literal.ResultUsage().Type.ID())) && rawLiteral.Value.Kind == parser.StringValue) {
				// Operator and text-search aliases own their NULL/string casts.
				// REGTYPE and collation/namespace aliases need the source cast for NULL.
				return text, nil
			}
		}
		usage, ok := v.castUsages[path]
		if !ok {
			return "", fmt.Errorf("stuntgres: missing analyzed view cast usage")
		}
		if !v.Presentation.Deparse && !usage.Type.IsDomain() && (usage.Type.IsArray() || usage.Type.IsEnum() || usage.Type.IsComposite() || usage.Type.ID() == sqltype.JSON || usage.Type.ID() == sqltype.JSONB || usage.Type.ID() == sqltype.JSONPath || sqltype.IsFTS(usage.Type.ID())) {
			switch e.Child.(type) {
			case parser.LiteralExpr, parser.TypedLiteralExpr:
				if literal, captured := v.literals[literalPath]; captured && literal.ResultUsage() == usage {
					// Captured array, enum, composite, JSON/JSONPath and FTS datums own their typed cast.
					// Elide only this exact leaf annotation, not outer casts,
					// domain checks, typmod changes or actual conversions.
					return text, nil
				}
			}
		}
		if parent.orderedSetDirect && (usage.Type.IsArray() || usage.Type.IsEnum()) {
			if literal, captured := v.literals[literalPath]; captured && literal.ResultUsage() == usage {
				// Like retained registry-array constants, this captured datum
				// already renders its exact owning typed annotation. Elide only
				// that duplicated leaf annotation; outer casts, conversions and
				// domain annotations remain in the original cast chain.
				return text, nil
			}
		}
		if !v.Presentation.Deparse && (usage.Type.IsArray() || usage.Type.IsDomain()) && (usage.Type.ContainsRegclass() || usage.Type.ContainsRegrole() || usage.Type.ContainsRegObject()) {
			if literal, captured := v.literals[literalPath]; captured && literal.ResultUsage() == usage {
				// The captured datum already supplies its exact owning typed cast.
				// Rewrapping it would add another source cast on every replay.
				return text, nil
			}
		}
		if usage.Type.ContainsACLItem() {
			if literal, captured := v.literals[literalPath]; captured && literal.ResultUsage() == usage &&
				(v.Presentation.Deparse || !literal.Value.IsNull() || usage.Type.IsArray() || usage.Type.IsDomain() || usage.Type.IsComposite()) {
				// Public typed constants already own their annotation, including
				// NULL. Replay scalar NULL is untyped and retains its source cast.
				return text, nil
			}
		}
		if v.Presentation.Deparse {
			r := Deparser{View: catalogView, Path: v.Presentation.Path, Pretty: v.Presentation.Pretty, Output: v.Presentation.Output}
			_, simpleColumn := e.Child.(parser.ColumnExpr)
			literal, capturedLiteral := v.literals[literalPath]
			simpleLiteral := capturedLiteral && pgLiteralSimple(literal, text)
			_, castChild := e.Child.(parser.CastExpr)
			nestedCast := castChild || capturedLiteral && !simpleLiteral
			simpleSleepCall := false
			if call, ok := e.Child.(parser.FunctionExpr); ok {
				name := call.Name.Parts[len(call.Name.Parts)-1].Text
				simpleSleepCall = name == "pg_sleep" || name == "pg_sleep_for" || name == "pg_sleep_until" || parser.ArrayTransformFunctionName(name) || parser.ArraySearchFunctionName(name) || parser.ArrayInspectFunctionName(name)
			}
			return r.cast(text, usage, simpleColumn || simpleLiteral || simpleSleepCall || v.Presentation.Pretty && parent.functionArgument && nestedCast)
		}
		declaration, err := regobj.TypeDeclaration(catalogView, usage)
		if err != nil {
			return "", err
		}
		if _, constructor := e.Child.(parser.ArrayExpr); constructor {
			return text + "::" + declaration, nil
		}
		return "CAST(" + text + " AS " + declaration + ")", nil
	case parser.OperatorExpr:
		if ref, selected := v.operatorRefs[path]; selected {
			d, ok := catalogView.Operator(ref.Address)
			if !ok {
				return "", fmt.Errorf("stuntgres: missing frozen selected operator")
			}
			ns := catalogView.Get(d.Namespace()).Name()
			e.Name = parser.ObjectName{Parts: []parser.Identifier{{Text: ns}, {Text: d.Name()}}}
		}
		spelling := e.Name.Parts[len(e.Name.Parts)-1].Text
		op := spelling
		if len(e.Name.Parts) == 2 {
			op = "OPERATOR(" + catalog.QuoteIdentifier(e.Name.Parts[0].Text) + "." + op + ")"
		} else if op == "^@" {
			op = "OPERATOR(pg_catalog.^@)"
		} else if e.SQLSimilar && !v.Presentation.Deparse {
			op = "SIMILAR TO"
			if spelling == "!~" {
				op = "NOT SIMILAR TO"
			}
		}
		precedence := 4
		if e.SQLSimilar || pgOperatorPriority(spelling) != 0 || len(e.Args) == 1 {
			precedence = 5
		}
		leftParent := DeparseParent{Precedence: precedence, Operator: spelling}
		if precedence == 4 {
			// Comparisons are nonassociative; predicate-valued operands retain
			// their grouping rather than becoming an unsupported comparison chain.
			leftParent = DeparseParent{Precedence: 5}
		}
		if len(e.Name.Parts) == 2 {
			// OPERATOR(schema.op) has generic SQL precedence, even for a
			// familiar arithmetic spelling. Keep operand groups conservatively.
			leftParent = DeparseParent{Precedence: 5}
		}
		leftParent.unary = len(e.Args) == 1
		operatorOperand := func(index int, parent DeparseParent) (string, error) {
			if analyze.ArrayMembershipName(spelling) || spelling == "||" {
				return selectedOperand(index, e.Args[index], parent)
			}
			return operand(e.Args[index], parent)
		}
		left, err := operatorOperand(0, leftParent)
		if err != nil {
			return "", err
		}
		if len(e.Args) == 1 {
			text := op + " " + left
			if !v.Presentation.Pretty || OperatorGrouped(spelling, false, parent) {
				text = "(" + text + ")"
			}
			return text, nil
		}
		rightParent := leftParent
		rightParent.Right = true
		right, err := operatorOperand(1, rightParent)
		if err != nil {
			return "", err
		}
		text := left + " " + op + " " + right
		grouped := !v.Presentation.Pretty || parent.Precedence > precedence
		if v.Presentation.Pretty && precedence == 5 {
			grouped = OperatorGrouped(spelling, true, parent)
		}
		if v.Presentation.Pretty && len(e.Name.Parts) == 2 && parent.Precedence != 0 {
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
		left, err := operand(e.Left, DeparseParent{Precedence: precedence, Operator: op})
		if err != nil {
			return "", err
		}
		text := op + " " + left
		if e.Op != parser.LogicalNot {
			right, err := operand(e.Right, DeparseParent{Precedence: precedence, Operator: op, Right: true})
			if err != nil {
				return "", err
			}
			text = left + " " + op + " " + right
		}
		if !v.Presentation.Pretty || parent.Precedence > precedence || e.Op == parser.LogicalNot && parent.Operator == "NOT" {
			text = "(" + text + ")"
		}
		return text, nil
	case parser.PredicateExpr:
		left, err := operand(e.Left, DeparseParent{Precedence: 5})
		if err != nil {
			return "", err
		}
		operators := []string{"IS NULL", "IS NOT NULL", "IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN", "IS DISTINCT FROM", "IS NOT DISTINCT FROM"}
		text := left + " " + operators[e.Kind]
		if e.Right != nil {
			right, err := operand(e.Right, DeparseParent{Precedence: 5})
			if err != nil {
				return "", err
			}
			text += " " + right
		}
		if !v.Presentation.Pretty || parent.Precedence > 4 {
			text = "(" + text + ")"
		}
		return text, nil
	case parser.JSONPredicateExpr:
		text, err := child(e.Child)
		if err != nil {
			return "", err
		}
		return jsonPredicateRecipe(text, e.Kind, e.Unique, e.Not), nil
	case parser.SQLJSONExpr:
		parts := make([]string, len(e.Args))
		for i, arg := range e.Args {
			text, err := child(arg)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		if e.Query != nil {
			query, err := v.SelectQuerySQL(ctx, catalogView, e.Query, v.queries[e.Query])
			if err != nil {
				return "", err
			}
			parts = []string{query}
		}
		declaration := ""
		if e.Returning != nil {
			usage, ok := v.castUsages[path]
			if !ok {
				return "", fmt.Errorf("stuntgres: missing SQL/JSON returning usage")
			}
			var err error
			declaration, err = regobj.TypeDeclaration(catalogView, usage)
			if err != nil {
				return "", err
			}
		}
		return templateSQL(functions.SQLJSONPieces(e, len(parts)), parts, declaration, rawSyntaxWord)
	case parser.AggregateExpr:
		name := e.Name
		if e.Schema != nil {
			name = catalog.QuoteIdentifier(e.Schema.Text) + "." + catalog.QuoteIdentifier(e.Name)
		}
		if e.Name == "int_array_aggregate" {
			var err error
			name, err = v.intaggRecipeName(catalogView, path, e.Name)
			if err != nil {
				return "", err
			}
		}
		var text string
		var err error
		if e.Star {
			text = "*"
		} else {
			if e.Name == "json_objectagg" && e.JSON != nil {
				key, keyErr := operand(e.Args[0], DeparseParent{functionArgument: true})
				if keyErr != nil {
					return "", keyErr
				}
				value, valueErr := operand(e.Args[1], DeparseParent{functionArgument: true})
				if valueErr != nil {
					return "", valueErr
				}
				text = "(" + key + ") VALUE " + value
			} else if e.Within {
				for i, arg := range e.Args {
					part, failure := operand(arg, DeparseParent{functionArgument: true, orderedSetDirect: true})
					if failure != nil {
						return "", failure
					}
					if i > 0 {
						text += ", "
					}
					text += part
				}
			} else {
				text, err = args(e.Args)
			}
			if err != nil {
				return "", err
			}
			if e.Distinct {
				text = "DISTINCT " + text
			}
		}
		if e.JSON != nil {
			text += functions.JSONFormatSQL(e.JSON.Input)
		}
		if len(e.Order) > 0 {
			if e.Within {
				text += ") WITHIN GROUP ("
			}
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
		if e.JSON != nil {
			if e.JSON.NullPos.Character > 0 {
				if e.JSON.Absent {
					text += " ABSENT ON NULL"
				} else {
					text += " NULL ON NULL"
				}
			}
			if e.JSON.UniquePos.Character > 0 {
				if e.JSON.Unique {
					text += " WITH UNIQUE KEYS"
				} else {
					text += " WITHOUT UNIQUE KEYS"
				}
			}
			if e.JSON.Returning != nil {
				usage, ok := v.castUsages[path]
				if !ok {
					return "", fmt.Errorf("stuntgres: missing SQL/JSON returning usage")
				}
				declaration, err := regobj.TypeDeclaration(catalogView, usage)
				if err != nil {
					return "", err
				}
				text += " RETURNING " + declaration + functions.JSONFormatSQL(e.JSON.Output)
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
		if e.Window != nil {
			spec, err := windowSpecSQL(*e.Window, child)
			if err != nil {
				return "", err
			}
			if e.Window.Bare {
				text += " OVER " + spec
			} else {
				text += " OVER (" + spec + ")"
			}
		}
		return text, nil
	case parser.ConditionalExpr:
		text, err := args(e.Args)
		return e.Kind.String() + "(" + text + ")", err
	case parser.CaseExpr:
		text := "CASE"
		separator, ending := " ", " END"
		if v.Presentation.Deparse {
			// Match the existing PG CASE layout without losing the frozen AST
			// of undemanded extraction branches.
			text, separator, ending = "    CASE", "\n            ", "\n        END"
		}
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
			text += separator + "WHEN " + condition + " THEN " + result
		}
		if e.Else != nil {
			part, err := child(e.Else)
			if err != nil {
				return "", err
			}
			text += separator + "ELSE " + part
		}
		return text + ending, nil
	case parser.FunctionExpr:
		if captured, projected := v.composites[path]; projected {
			if captured.Kind != analyze.CompositeSelection || len(e.Args) != 1 {
				return "", fmt.Errorf("stuntgres: invalid frozen functional projection")
			}
			base, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			fields, err := v.frozenCompositeFields(ctx, catalogView, captured, base)
			if err != nil {
				return "", err
			}
			return fields[0], nil
		}
		name := e.Name.Parts[len(e.Name.Parts)-1].Text
		if name == "int_array_enum" {
			qualified, err := v.intaggRecipeName(catalogView, path, name)
			if err != nil {
				return "", err
			}
			text, err := args(e.Args)
			return qualified + "(" + text + ")", err
		}
		if name == "generate_subscripts" && !v.Presentation.Deparse {
			parts := make([]string, len(e.Args))
			for i, arg := range e.Args {
				var text string
				var err error
				if i == 0 {
					argumentPath := path
					argumentPath.Argument = 0
					text, err = v.subscriptsArraySQLAt(ctx, catalogView, arg, argumentPath, ordinal)
				} else {
					text, err = operand(arg, DeparseParent{functionArgument: true})
				}
				if err != nil {
					return "", err
				}
				parts[i] = text
			}
			return "pg_catalog.generate_subscripts(" + strings.Join(parts, ", ") + ")", nil
		}
		if name == "parse_ident" || parser.ArrayTransformFunctionName(name) || parser.ArraySearchFunctionName(name) || parser.ArrayInspectFunctionName(name) {
			parts := make([]string, len(e.Args))
			for i, arg := range e.Args {
				text, err := selectedOperand(i, arg, DeparseParent{functionArgument: true})
				if err != nil {
					return "", err
				}
				if len(e.ArgNames) != 0 && e.ArgNames[i].Text != "" {
					text = catalog.QuoteIdentifier(e.ArgNames[i].Text) + " => " + text
				}
				parts[i] = text
			}
			functionName := "pg_catalog." + name
			if v.Presentation.Deparse {
				functionName = v.Presentation.identifier(name)
			}
			return functionName + "(" + strings.Join(parts, ", ") + ")", nil
		}
		if e.Syntax == parser.SimilarEscapeFunctionSyntax {
			if len(e.Args) < 1 || len(e.Args) > 2 {
				return "", fmt.Errorf("stuntgres: invalid stored SIMILAR normalizer arity")
			}
			pattern, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			if v.Presentation.Deparse {
				if len(e.Args) == 1 {
					return "similar_to_escape(" + pattern + ")", nil
				}
				escape, err := child(e.Args[1])
				if err != nil {
					return "", err
				}
				return "similar_to_escape(" + pattern + ", " + escape + ")", nil
			}
			if len(e.Args) == 1 {
				return pattern, nil
			}
			escape, err := child(e.Args[1])
			if err != nil {
				return "", err
			}
			return pattern + " ESCAPE " + escape, nil
		}
		if v.similarSubstrings[path] {
			if name != "substring" || len(e.Args) != 3 {
				return "", fmt.Errorf("stuntgres: invalid stored SIMILAR substring occurrence")
			}
			source, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			pattern, err := child(e.Args[1])
			if err != nil {
				return "", err
			}
			escape, err := child(e.Args[2])
			if err != nil {
				return "", err
			}
			if e.Syntax == parser.SQLFunctionSyntax {
				return "SUBSTRING(" + source + " SIMILAR " + pattern + " ESCAPE " + escape + ")", nil
			}
			functionName := `pg_catalog."substring"`
			if v.Presentation.Deparse {
				functionName = `"substring"`
			}
			return functionName + "(" + source + ", " + pattern + ", " + escape + ")", nil
		}
		if v.posixSubstrings[path] {
			if name != "substring" || len(e.Args) != 2 {
				return "", fmt.Errorf("stuntgres: invalid stored POSIX substring occurrence")
			}
			source, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			pattern, err := child(e.Args[1])
			if err != nil {
				return "", err
			}
			if e.Syntax == parser.SQLFunctionSyntax && !v.Presentation.Deparse {
				return "SUBSTRING(" + source + " FROM " + pattern + ")", nil
			}
			functionName := `pg_catalog."substring"`
			if v.Presentation.Deparse {
				functionName = `"substring"`
			}
			return functionName + "(" + source + ", " + pattern + ")", nil
		}
		if name == "overlaps" {
			ordinary := "pg_catalog.overlaps"
			if v.Presentation.Deparse {
				ordinary = catalog.QuoteIdentifier(name)
			}
			return RenderOverlapsCall(e.Syntax, e.Args, ordinary, func(_ int, arg parser.Expr) (string, error) { return child(arg) })
		}
		if parser.IdentityFunctionName(name) && len(e.Args) == 0 {
			// A bound identity call is a pg_catalog builtin. In particular,
			// decoded quoted keyword names cannot be replayed unqualified.
			return "pg_catalog." + name + "()", nil
		}
		if functions.VersionInformationKindForName(name) != 0 && len(e.Args) == 0 {
			if v.Presentation.Deparse {
				return name + "()", nil
			}
			return "pg_catalog." + name + "()", nil
		}
		if functions.TransactionTimeKindForName(name) != 0 && len(e.Args) == 0 {
			if v.Presentation.Deparse {
				return name + "()", nil
			}
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
			pieces, err := functions.TrimPieces(name, len(e.Args))
			if err != nil {
				return "", err
			}
			return templateSQL(pieces, []string{source, set}, "", rawSyntaxWord)
		}
		if e.Syntax == parser.SQLFunctionSyntax && name == "extract" {
			if len(e.Args) != 2 {
				return "", fmt.Errorf("stuntgres: invalid stored EXTRACT arity")
			}
			field, err := child(e.Args[0])
			if err != nil {
				return "", err
			}
			if v.Presentation.Deparse {
				literal, ok := e.Args[0].(parser.LiteralExpr)
				if !ok {
					return "", fmt.Errorf("stuntgres: invalid stored EXTRACT field")
				}
				// PG's catalog renderer quotes the field as an identifier. The
				// child call above still advances literal capture.
				field = v.Presentation.identifier(literal.Value.Text)
			}
			source, err := child(e.Args[1])
			if err != nil {
				return "", err
			}
			return "EXTRACT(" + field + " FROM " + source + ")", nil
		}
		if e.Syntax == parser.SQLFunctionSyntax {
			// Render every argument in order before the keyword spelling.
			parts := make([]string, len(e.Args))
			for i, arg := range e.Args {
				text, err := child(arg)
				if err != nil {
					return "", err
				}
				parts[i] = text
			}
			pieces, err := functions.SyntaxPieces(name, len(parts))
			if err != nil {
				return "", err
			}
			return templateSQL(pieces, parts, "", rawSyntaxWord)
		}
		if name == "position" {
			text, err := args(e.Args)
			return "pg_catalog.position(" + text + ")", err
		}
		if (name == "format" || functions.StringVariadicFunctionName(name) || functions.JSONCreateKind(name) != 0) && e.Variadic {
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
			functionName := catalog.ObjectNameText(e.Name)
			if functions.JSONCreateKind(name) != 0 {
				functionName = "pg_catalog." + name
			}
			if v.Presentation.Deparse {
				functionName = v.Presentation.identifier(name)
			}
			return functionName + "(" + strings.Join(parts, ", ") + ")", nil
		}
		if parser.ACLValueFunctionName(name) || parser.ObjectAddressFunctionName(name) || parser.RegexpFunctionName(name) || name == "pg_get_acl" || name == "make_date" || name == "make_time" || name == "make_timestamp" || name == "make_timestamptz" || name == "make_interval" || name == "json_strip_nulls" || parser.JSONBSetFunctionName(name) || (name == "jsonb_path_query_array" || functions.JSONBPathQueryName(name)) || name == "json_extract_path" || name == "json_extract_path_text" || name == "jsonb_extract_path" || name == "jsonb_extract_path_text" {
			functionName, identifier := "pg_catalog."+name, catalog.QuoteIdentifier
			if v.Presentation.Deparse {
				functionName, identifier = v.Presentation.identifier(name), v.Presentation.identifier
			}
			parts := make([]string, len(e.Args))
			for i, arg := range e.Args {
				text, err := operand(arg, DeparseParent{functionArgument: true})
				if err != nil {
					return "", err
				}
				if len(e.ArgNames) > i && e.ArgNames[i].Text != "" {
					text = identifier(e.ArgNames[i].Text) + " => " + text
				}
				if (parser.JSONBSetFunctionName(name) || (name == "jsonb_path_query_array" || functions.JSONBPathQueryName(name)) || name == "json_strip_nulls" || name == "json_extract_path" || name == "json_extract_path_text" || name == "jsonb_extract_path" || name == "jsonb_extract_path_text") && e.Variadic && i == len(e.Args)-1 {
					text = "VARIADIC " + text
				}
				parts[i] = text
			}
			return functionName + "(" + strings.Join(parts, ", ") + ")", nil
		}
		text, err := args(e.Args)
		switch name {
		case "pg_sleep", "pg_sleep_for", "pg_sleep_until":
			if err != nil {
				return "", err
			}
			if v.Presentation.Deparse {
				if usage, ok := v.castUsages[path]; ok {
					r := Deparser{View: catalogView, Path: v.Presentation.Path, Pretty: v.Presentation.Pretty}
					simple := false
					switch e.Args[0].(type) {
					case parser.LiteralExpr, parser.TypedLiteralExpr, parser.ColumnExpr, parser.CastExpr:
						simple = true
					}
					text, err = r.cast(text, usage, simple)
				}
				return name + "(" + text + ")", err
			}
			return "pg_catalog." + name + "(" + text + ")", err
		case "left", "right", "substring", "substr":
			functionName := "pg_catalog." + catalog.QuoteIdentifier(name)
			if v.Presentation.Deparse {
				functionName = v.Presentation.identifier(name)
			}
			return functionName + "(" + text + ")", err
		case "num_nulls", "num_nonnulls", "convert", "convert_from", "convert_to", "encode", "decode", "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii", "unistr", "unicode_assigned", "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
			if v.Presentation.Deparse {
				return name + "(" + text + ")", err
			}
			return "pg_catalog." + name + "(" + text + ")", err
		case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "strpos", "overlay", "replace", "translate", "starts_with":
			return "pg_catalog." + name + "(" + text + ")", err
		case "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge":
			return "pg_catalog." + name + "(" + text + ")", err
		}
		if name == "generate_series" || name == "generate_subscripts" || functions.StringVariadicFunctionName(name) || name == "format" || name == "json_object" || name == "jsonb_object" || parser.JSONFunctionName(name) || functions.JSONCreateKind(name) != 0 {
			functionName := "pg_catalog." + name
			if v.Presentation.Deparse {
				functionName = v.Presentation.identifier(name)
			}
			return functionName + "(" + text + ")", err
		}
		return catalog.ObjectNameText(e.Name) + "(" + text + ")", err
	case parser.CallExpr:
		text, err := args(e.Args)
		return catalog.ObjectNameText(e.Name) + "(" + text + ")", err
	case parser.ArrayExpr:
		text, err := args(e.Elements)
		return "ARRAY[" + text + "]", err
	case parser.SubscriptExpr:
		text, err := child(e.Base)
		if err != nil {
			return "", err
		}
		text = "(" + text + ")"
		for _, s := range e.Subscripts {
			text += "["
			if s.Lower != nil {
				part, err := child(s.Lower)
				if err != nil {
					return "", err
				}
				text += part
			}
			if s.Slice {
				text += ":"
			}
			if s.Upper != nil {
				part, err := child(s.Upper)
				if err != nil {
					return "", err
				}
				text += part
			}
			text += "]"
		}
		return text, nil
	case parser.RegclassExpr:
		text, err := child(e.Child)
		return "CAST(" + text + " AS regclass)", err
	}
	return "", fmt.Errorf("stuntgres: unsupported view deparser expression %T", raw)
}

func windowSpecSQL(spec parser.WindowSpec, child func(parser.Expr) (string, error)) (string, error) {
	parts := make([]string, 0, 3)
	if spec.Name != nil {
		parts = append(parts, catalog.QuoteIdentifier(spec.Name.Text))
	}
	if len(spec.Partition) > 0 {
		keys := make([]string, len(spec.Partition))
		for i, key := range spec.Partition {
			var err error
			keys[i], err = child(key)
			if err != nil {
				return "", err
			}
		}
		parts = append(parts, "PARTITION BY "+strings.Join(keys, ", "))
	}
	if len(spec.Order) > 0 {
		keys := make([]string, len(spec.Order))
		for i, key := range spec.Order {
			part, err := child(key.Expr)
			if err != nil {
				return "", err
			}
			if key.Desc {
				part += " DESC"
			}
			if key.Nulls == parser.NullsFirst {
				part += " NULLS FIRST"
			} else if key.Nulls == parser.NullsLast {
				part += " NULLS LAST"
			}
			keys[i] = part
		}
		parts = append(parts, "ORDER BY "+strings.Join(keys, ", "))
	}
	if spec.Frame != nil {
		boundSQL := func(bound parser.FrameBound) (string, error) {
			switch bound.Kind {
			case parser.FrameUnboundedPreceding:
				return "UNBOUNDED PRECEDING", nil
			case parser.FrameUnboundedFollowing:
				return "UNBOUNDED FOLLOWING", nil
			case parser.FrameCurrentRow:
				return "CURRENT ROW", nil
			}
			text, err := child(bound.Offset)
			if bound.Kind == parser.FrameOffsetPreceding {
				return text + " PRECEDING", err
			}
			return text + " FOLLOWING", err
		}
		start, err := boundSQL(spec.Frame.Start)
		if err != nil {
			return "", err
		}
		end, err := boundSQL(spec.Frame.End)
		if err != nil {
			return "", err
		}
		unit := "RANGE"
		if spec.Frame.Unit == parser.FrameGroups {
			unit = "GROUPS"
		} else if spec.Frame.Unit == parser.FrameRows {
			unit = "ROWS"
		}
		parts = append(parts, unit+" BETWEEN "+start+" AND "+end)
		switch spec.Frame.Exclusion {
		case parser.ExcludeCurrentRow:
			parts = append(parts, "EXCLUDE CURRENT ROW")
		case parser.ExcludeGroup:
			parts = append(parts, "EXCLUDE GROUP")
		case parser.ExcludeTies:
			parts = append(parts, "EXCLUDE TIES")
		}
	}
	return strings.Join(parts, " "), nil
}

func (v *QueryRenderer) quantifiedFieldsSQL(ctx context.Context, catalog catalog.Read, path analyze.ViewLiteralPath, fields []string) (string, error) {
	for i, text := range fields {
		key := path
		key.Argument = i + 1
		usage, captured := v.castUsages[key]
		if !captured {
			continue
		}
		if v.Presentation.Deparse {
			r := Deparser{View: catalog, Path: v.Presentation.Path, Pretty: v.Presentation.Pretty, Output: v.Presentation.Output}
			var err error
			fields[i], err = r.cast(text, usage, false)
			if err != nil {
				return "", err
			}
		} else {
			declaration, err := regobj.TypeDeclaration(catalog, usage)
			if err != nil {
				return "", err
			}
			fields[i] = "CAST(" + text + " AS " + declaration + ")"
		}
	}
	return strings.Join(fields, ", "), nil
}
