package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRow(raw parser.RowExpr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position) (expr.Expr, error) {
	list, err := b.expandRawList(raw.Fields)
	if err != nil {
		return nil, err
	}
	fields := make([]expr.Expr, len(list.raw))
	for i, child := range list.raw {
		if _, requested := child.(parser.DefaultExpr); requested {
			return nil, expr.ErrorAt("42601", "DEFAULT is not allowed in this context", child.SourcePosition())
		}
		if list.nodes != nil && list.nodes[i] != nil {
			fields[i] = list.nodes[i]
			continue
		}
		var expected sqltype.Usage
		if target.Type != nil && target.Type.BaseType().IsComposite() && i < target.Type.BaseType().CompositeLen() {
			expected = target.Type.BaseType().CompositeMember(i).Usage
		}
		policy := expressionPolicy{context: projectionContext, expected: expected}
		if expected.Type != nil {
			policy.context = functionArgumentContext
			policy.mode = mode
			// The enclosing ROW owns member conversion. Preserve its synthetic
			// origin instead of manufacturing an unwritten member CastExpr.
			policy.deferAssignment = true
		}
		node, err := b.bind(child, policy)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && node != nil && (d.Code == "42804" || d.Code == "42846") && expected.Type != nil {
				location := pos
				if node.ResultUsage().Type.BaseType().IsComposite() {
					location = node.SourcePosition()
				}
				return nil, b.rowCoercionError(target, location, fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.diagnosticTypeName(node.ResultUsage().Type), b.diagnosticTypeName(expected.Type), i+1))
			}
			return nil, err
		}
		fields[i], err = b.finalizeProjection(node)
		if err != nil {
			return nil, err
		}
	}
	if len(fields) > 1664 {
		return nil, expr.ErrorAt("54011", "ROW expressions can have at most 1664 entries", raw.Pos)
	}
	if target.Type != nil && target.Type.BaseType().IsComposite() {
		width := target.Type.BaseType().CompositeLen()
		if len(fields) < width {
			return nil, b.rowCoercionError(target, pos, "Input has too few columns.")
		}
		if len(fields) > width {
			return nil, b.rowCoercionError(target, pos, "Input has too many columns.")
		}
		for i, field := range fields {
			var err error
			fields[i], err = b.assignCompositeMember(field, target.Type.BaseType().CompositeMember(i).Usage, mode, pos)
			if err != nil {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
					location := pos
					if field.ResultUsage().Type.BaseType().IsComposite() {
						location = field.SourcePosition()
					}
					return nil, b.rowCoercionError(target, location, fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.diagnosticTypeName(field.ResultUsage().Type), b.diagnosticTypeName(target.Type.BaseType().CompositeMember(i).Usage.Type), i+1))
				}
				return nil, err
			}
		}
	} else {
		members := make([]sqltype.CompositeMember, len(fields))
		for i, field := range fields {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: "f" + strconv.Itoa(i+1), Usage: field.ResultUsage()}
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		target = shape.Usage()
	}
	props := expr.Properties{}
	for _, field := range fields {
		props = expr.MergeProperties(props, field.Properties())
	}
	return plan.RowExpr{Info: b.info(target, pos, props), Fields: fields, Raw: &raw, Sources: list.sources, Expansions: list.expansions}, nil
}
func (b *expressionBinder) rowCoercionError(target sqltype.Usage, pos parser.Position, detail string) error {
	d := expr.ErrorAt("42846", "cannot cast type record to "+b.diagnosticTypeName(target.Type), pos).(*sqlstate.Error)
	d.Detail = detail
	return d
}
func (b *expressionBinder) assignCompositeMember(node expr.Expr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position) (expr.Expr, error) {
	if node.ResultUsage() == target {
		return node, nil
	}
	// Structural conversion is an anonymous RECORD authority, not permission
	// to reinterpret a named constructor or a domain's underlying members.
	if row, ok := node.(plan.RowExpr); ok && row.Usage.Type.ID() == 2249 && target.Type.BaseType().IsComposite() {
		if row.Raw != nil {
			return b.bindRow(*row.Raw, target, mode, pos)
		}
		if len(row.Fields) != target.Type.BaseType().CompositeLen() {
			detail := "Input has too many columns."
			if len(row.Fields) < target.Type.BaseType().CompositeLen() {
				detail = "Input has too few columns."
			}
			return nil, b.rowCoercionError(target, pos, detail)
		}
		fields := make([]expr.Expr, len(row.Fields))
		props := expr.Properties{}
		for i, child := range row.Fields {
			var err error
			fields[i], err = b.assignCompositeMember(child, target.Type.BaseType().CompositeMember(i).Usage, mode, pos)
			if err != nil {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
					return nil, b.rowCoercionError(target, child.SourcePosition(), fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.diagnosticTypeName(child.ResultUsage().Type), b.diagnosticTypeName(target.Type.BaseType().CompositeMember(i).Usage.Type), i+1))
				}
				return nil, err
			}
			props = expr.MergeProperties(props, fields[i].Properties())
		}
		return plan.RowExpr{Info: b.info(target, row.Pos, props), Fields: fields}, nil
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), target, mode)
	if err != nil {
		return node, err
	}
	return plan.CoercionExpr{Info: b.info(target, node.SourcePosition(), coercionProperties(node, coercionPlan)), Child: node, Plan: coercionPlan, Mode: mode, SyntheticConstructorElement: true}, nil
}
func (b *expressionBinder) selectCompositeField(base expr.Expr, name string, pos parser.Position) (expr.Expr, error) {
	typ := base.ResultUsage().Type
	if typ == nil {
		return nil, expr.ErrorAt("42809", "column notation ."+name+" applied to type unknown, which is not a composite type", pos)
	}
	shape := typ.BaseType()
	if !shape.IsComposite() {
		return nil, expr.ErrorAt("42809", fmt.Sprintf("column notation .%s applied to type %s, which is not a composite type", name, b.diagnosticTypeName(typ)), pos)
	}
	index, found, err := compositeFieldIndex(base, name, pos)
	if err != nil {
		return nil, err
	}
	if found {
		return b.compositeFieldAt(base, index, pos), nil
	}
	message := fmt.Sprintf("column %q not found in data type %s", name, b.diagnosticTypeName(typ))
	if shape.ID() == 2249 {
		message = fmt.Sprintf("could not identify column %q in record data type", name)
	}
	if row, ok := base.(plan.RowExpr); ok && row.WholeNames != nil {
		message = fmt.Sprintf("column %s.%s does not exist", row.WholeName, name)
	}
	return nil, expr.ErrorAt("42703", message, pos)
}
func (b *expressionBinder) compositeFieldAt(base expr.Expr, index int, pos parser.Position) expr.Expr {
	member := base.ResultUsage().Type.BaseType().CompositeMember(index)
	var field expr.Expr
	switch row := base.(type) {
	case plan.RowExpr:
		index = row.FieldSourceIndex(index)
		if row.ReturningWhole {
			field = row.Fields[index]
		}
	case plan.ReturningWholeRowExpr:
		field = row.Members[index]
	}
	if field != nil {
		var column plan.ColumnExpr
		switch value := field.(type) {
		case plan.ReturningColumnExpr:
			if value.Merge {
				value.Pos = pos
				return value
			}
			if value.Value != nil {
				value.Old, value.Absent = false, false
				value.Pos = pos
				return value
			}
			column = value.ColumnExpr
		default:
			return plan.FieldExpr{Info: b.info(member.Usage, pos, base.Properties()), Base: base, Attribute: member.Attribute, Index: index}
		}
		column.Pos = pos
		return column
	}
	return plan.FieldExpr{Info: b.info(member.Usage, pos, base.Properties()), Base: base, Attribute: member.Attribute, Index: index}
}
func (b *expressionBinder) retainedCompositeField(base expr.Expr, key preparedCompositeKey, captured compositeOccurrence, pos parser.Position) expr.Expr {
	index, exists := base.ResultUsage().Type.BaseType().CompositeAttributeIndex(captured.Attributes[0])
	b.prepared.CaptureComposite(key, captured.Base, captured.Attributes)
	if !exists {
		oldIndex, _ := captured.Base.Type.BaseType().CompositeAttributeIndex(captured.Attributes[0])
		usage := captured.Base.Type.BaseType().CompositeMember(oldIndex).Usage
		return plan.LiteralExpr{Info: b.info(usage, pos, expr.Properties{}), Value: sqltype.Null(usage.Type)}
	}
	return b.compositeFieldAt(base, index, pos)
}

func (b *expressionBinder) bindField(raw parser.FieldExpr) (expr.Expr, error) {
	if raw.Expansion {
		return nil, expr.ErrorAt("0A000", `row expansion via "*" is not supported here`, raw.Pos)
	}
	base, err := b.bindCompositeBase(raw.Base)
	if err != nil {
		return nil, err
	}
	key := fieldCompositeKey(raw)
	if captured, found, err := b.retainedComposite(key, base.ResultUsage()); err != nil {
		return nil, err
	} else if found {
		if captured.Kind == CompositeDotBuiltin {
			return b.bindDotBuiltin(raw, key)
		}
		return b.retainedCompositeField(base, key, captured, raw.Pos), nil
	}
	node, err := b.selectCompositeField(base, raw.Field.Text, raw.Pos)
	if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42703" || diagnostic.Code == "42809") && functions.NullCountFunctionKind(raw.Field.Text) != 0 {
		return b.bindDotBuiltin(raw, key)
	}
	if field, ok := node.(plan.FieldExpr); err == nil && ok {
		b.prepared.CaptureComposite(key, base.ResultUsage(), []int32{field.Attribute})
	}
	return node, err
}

func (b *expressionBinder) expandComposite(raw parser.FieldExpr) (plan.CompositeExpansionBinding, error) {
	base, err := b.bindCompositeBase(raw.Base)
	if err != nil {
		return plan.CompositeExpansionBinding{}, err
	}
	if jsonbRecordUnresolved(base) {
		return plan.CompositeExpansionBinding{}, sqlError("42809", "record type has not been registered")
	}
	typ := base.ResultUsage().Type
	if typ == nil || !typ.BaseType().IsComposite() {
		name := "unknown"
		if typ != nil {
			name = b.diagnosticTypeName(typ)
		}
		return plan.CompositeExpansionBinding{}, sqlError("42809", "type "+name+" is not composite")
	}
	shape := typ.BaseType()
	key := fieldCompositeKey(raw)
	captured, found, err := b.retainedComposite(key, base.ResultUsage())
	if err != nil {
		return plan.CompositeExpansionBinding{}, err
	}
	attributes := captured.Attributes
	if !found {
		attributes = make([]int32, shape.CompositeLen())
		for i := range attributes {
			attributes[i] = shape.CompositeMember(i).Attribute
		}
	}
	if found {
		b.prepared.CaptureComposite(key, captured.Base, attributes)
	} else {
		b.prepared.CaptureComposite(key, base.ResultUsage(), attributes)
	}
	result := plan.CompositeExpansionBinding{Base: base, Fields: make([]expr.Expr, len(attributes)), Names: make([]string, len(attributes)), Attributes: attributes}
	for i, attribute := range attributes {
		index, exists := shape.CompositeAttributeIndex(attribute)
		if found {
			oldIndex, _ := captured.Base.Type.BaseType().CompositeAttributeIndex(attribute)
			member := captured.Base.Type.BaseType().CompositeMember(oldIndex)
			result.Names[i] = member.Name
			if !exists {
				result.Fields[i] = plan.LiteralExpr{Info: b.info(member.Usage, raw.Pos, expr.Properties{}), Value: sqltype.Null(member.Usage.Type)}
				continue
			}
		} else {
			result.Names[i] = shape.CompositeMember(index).Name
		}
		result.Fields[i] = b.compositeFieldAt(base, index, raw.Pos)
		if row, ok := base.(plan.RowExpr); ok && row.WholeNames != nil {
			result.Names[i] = row.WholeNames[index]
		}
	}
	return result, nil
}

// asValue distinguishes an alias value (bare name or scalar star) from the
// exposed-column expansion used by a top-level qualified star or ROW(alias.*).
func (b *expressionBinder) wholeRow(name parser.ObjectName, pos parser.Position, asValue bool) (expr.Expr, error) {
	if b.target.ruleNamespace != nil && len(name.Parts) == 1 && (name.Parts[0].Text == "old" || name.Parts[0].Text == "new") {
		row, _, err := b.ruleColumn(parser.ColumnExpr{Name: name, Pos: pos})
		return row, err
	}
	if b.target.returningAliases != nil && len(name.Parts) == 1 {
		if expr, found, err := b.returningWholeRow(name.Parts[0].Text, b.target.relation, b.frameID(), b.target.returningAliases, pos); found {
			return expr, err
		}
		_, pseudo := b.target.returningPseudo(name)
		visible := b.target.name
		if b.target.alias != "" {
			visible = b.target.alias
		}
		if pseudo || name.Parts[0].Text == visible {
			newValues, err := b.returningQualifier(name, pos)
			if err != nil {
				return nil, err
			}
			fields := make([]expr.Expr, len(b.target.relation.Columns))
			props := expr.Properties{}
			for i := range fields {
				fields[i] = b.returningColumnAt(i, pos, newValues)
				props = expr.MergeProperties(props, fields[i].Properties())
			}
			typ, _ := b.definitionType(b.target.relation.RowType)
			return plan.RowExpr{Info: b.info(typ.Usage(), pos, props), Fields: fields, ReturningWhole: true, ReturningNull: b.target.returningAliases.mode == returningDelete && newValues}, nil
		}
	}
	if scope, newValues := b.outerReturningScope(name); scope != nil {
		return b.outerReturningWholeRow(scope, pos, newValues)
	}
	q, err := b.queryQualifier(name, pos)
	if err != nil {
		return nil, err
	}
	frame := b.query.resolvedQueryFrame
	if asValue && q.scalarAlias {
		return b.queryColumnInFrame(q.columns[0], pos, frame), nil
	}
	fields := make([]expr.Expr, len(q.columns))
	members := make([]sqltype.CompositeMember, len(fields))
	props := expr.Properties{}
	names := make([]string, len(fields))
	for i, col := range q.columns {
		fields[i] = b.queryColumnInFrame(col, pos, frame)
		names[i] = col.descriptor.Name
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: col.descriptor.Name, Usage: col.descriptor.Usage}
		props = expr.MergeProperties(props, fields[i].Properties())
	}
	props.RowDependent = true
	var typ *sqltype.Descriptor
	descriptor, _ := b.definitionDescriptor(q.ref.Address)
	if relation, ok := descriptor.(*catalog.Relation); ok && relation.RowType != 0 {
		typ, _ = b.definitionType(relation.RowType)
	}
	if typ == nil || !typ.Concrete() {
		typ, err = sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
	}
	source, err := b.frozenWholeRowSource(*q, frame)
	if err != nil {
		return nil, err
	}
	slot := 0
	if asValue && !b.query.suppressWholePresence {
		slot, err = b.wholeRowPresence(q, frame)
		if err != nil {
			return nil, err
		}
	}
	// A mutation target has no FROM producer; actual source qualifiers retain
	// their producer even with zero members or the same name in another schema.
	mergeSource := b.mergeReturningFrame(frame) && q.sourceRoot != nil
	return plan.RowExpr{Info: b.info(typ.Usage(), pos, props), Fields: fields, WholeNames: names, WholeName: q.name, WholeSource: source, PresenceSlot: slot, PresenceFrame: frame, MergeSource: mergeSource}, nil
}

func mapRow(row plan.RowExpr, child func(expr.Expr) (expr.Expr, bool, error)) (plan.RowExpr, bool, error) {
	var fields []expr.Expr
	for i, old := range row.Fields {
		next, changed, err := child(old)
		if err != nil {
			return row, false, err
		}
		if changed {
			if fields == nil {
				fields = append([]expr.Expr(nil), row.Fields...)
			}
			fields[i] = next
		}
	}
	if fields == nil {
		return row, false, nil
	}
	row.Fields = fields
	return row, true, nil
}

// Analysis associations preserve one raw occurrence across expanded slots.
// Catalog/type mapping also reowns these retained associations; runtime demand
// and grouping rewrites deliberately map only the executable fields.
func mapRowAnalysis(row plan.RowExpr, child func(expr.Expr) (expr.Expr, bool, error)) (plan.RowExpr, bool, error) {
	next, changed, err := mapRow(row, child)
	if err != nil {
		return row, false, err
	}
	var expansions map[int]plan.CompositeExpansionBinding
	for source, expansion := range row.Expansions {
		base, replaced, err := child(expansion.Base)
		if err != nil {
			return row, false, err
		}
		var fields []expr.Expr
		for i, old := range expansion.Fields {
			field, mapped, err := child(old)
			if err != nil {
				return row, false, err
			}
			if mapped {
				if fields == nil {
					fields = append([]expr.Expr(nil), expansion.Fields...)
				}
				fields[i] = field
			}
		}
		if !replaced && fields == nil {
			continue
		}
		if expansions == nil {
			expansions = make(map[int]plan.CompositeExpansionBinding, len(row.Expansions))
			for key, value := range row.Expansions {
				expansions[key] = value
			}
		}
		expansion.Base = base
		if fields != nil {
			expansion.Fields = fields
		}
		expansions[source] = expansion
	}
	if expansions != nil {
		next.Expansions = expansions
		changed = true
	}
	return next, changed, nil
}

// Expression-list expansion preserves original structural source occurrences.
type expandedExpressionList struct {
	raw        []parser.Expr
	nodes      []expr.Expr
	sources    []int
	expansions map[int]plan.CompositeExpansionBinding
}

func (b *expressionBinder) expandRawList(raw []parser.Expr) (expandedExpressionList, error) {
	result := expandedExpressionList{raw: raw}
	for i, expression := range raw {
		var expansion plan.CompositeExpansionBinding
		var err error
		var base parser.Expr
		if field, ok := expression.(parser.FieldExpr); ok && field.Expansion {
			expansion, err = b.expandComposite(field)
			base = field.Base
		} else if star, ok := expression.(parser.StarExpr); ok {
			expansion.Base, err = b.wholeRow(star.Qualifier, star.Pos, false)
			base = star
			if err == nil {
				row := expansion.Base.(plan.RowExpr)
				expansion.Fields = row.Fields
				shape := row.Usage.Type.BaseType()
				expansion.Names = make([]string, len(row.Fields))
				expansion.Attributes = make([]int32, len(row.Fields))
				for j := range row.Fields {
					member := shape.CompositeMember(j)
					expansion.Names[j] = member.Name
					expansion.Attributes[j] = member.Attribute
				}
			}
		} else {
			if result.expansions != nil {
				result.raw = append(result.raw, expression)
				result.nodes = append(result.nodes, nil)
				result.sources = append(result.sources, i)
			}
			continue
		}
		if err != nil {
			return expandedExpressionList{}, err
		}
		if result.expansions == nil {
			result.raw = make([]parser.Expr, i, len(raw)+len(expansion.Fields))
			copy(result.raw, raw[:i])
			result.nodes = make([]expr.Expr, i, cap(result.raw))
			result.sources = make([]int, i, cap(result.raw))
			for j := range i {
				result.sources[j] = j
			}
			result.expansions = make(map[int]plan.CompositeExpansionBinding)
		}
		result.expansions[i] = expansion
		for j, node := range expansion.Fields {
			result.raw = append(result.raw, parser.FieldExpr{Base: base, Field: parser.Identifier{Text: expansion.Names[j]}, Pos: expression.SourcePosition()})
			result.nodes = append(result.nodes, node)
			result.sources = append(result.sources, i)
		}
	}
	return result, nil
}

// One eligible call owns one pre-coercion operand occurrence. Nested calls save
// and restore this value; no execution nodes or cache outlive binding.
type functionProjectionScope struct {
	name         parser.ObjectName
	pos          parser.Position
	argumentPos  parser.Position
	argumentKind uint8
	operand      expr.Expr
	noMatch      error
}

func projectionArgumentKind(raw parser.Expr) uint8 {
	switch raw.(type) {
	case parser.ColumnExpr:
		return 1
	case parser.StarExpr:
		return 2
	case parser.RowExpr:
		return 3
	case parser.CastExpr:
		return 4
	case parser.FieldExpr:
		return 5
	case parser.ConditionalExpr:
		return 6
	case parser.CaseExpr:
		return 7
	case parser.FunctionExpr:
		return 8
	case parser.SubscriptExpr:
		return 9
	case parser.SubqueryExpr:
		return 10
	case parser.ParameterExpr:
		return 11
	}
	return 0
}
func (s *functionProjectionScope) matches(raw parser.Expr) bool {
	return s.argumentKind != 0 && raw.SourcePosition() == s.argumentPos && projectionArgumentKind(raw) == s.argumentKind
}
func (c *clauseState) noteFunctionSignature(name parser.ObjectName, pos parser.Position, err error) {
	scope := &c.functionProjection
	if scope.argumentKind == 0 || scope.pos != pos || len(name.Parts) != len(scope.name.Parts) {
		return
	}
	for i := range name.Parts {
		if name.Parts[i].Text != scope.name.Parts[i].Text {
			return
		}
	}
	scope.noMatch = err
}

func (b *expressionBinder) bindFunction(raw parser.FunctionExpr) (result expr.Expr, failure error) {
	before := b.clause.fromSetObservation
	defer func() {
		if failure != nil || b.clause.aggregateClause != "FROM" || !exec.SourceReturnsSet(result) {
			return
		}
		if b.clause.fromSetObservation != before {
			result, failure = nil, nestedFromSetError(b.clause.fromSetObservation.pos)
			return
		}
		b.clause.fromSetObservation.generation++
		b.clause.fromSetObservation.pos = raw.Pos
	}()
	if raw.Within {
		combined := raw
		combined.CompositeCandidate = false
		combined.Args = make([]parser.Expr, 0, len(raw.Args)+len(raw.Order))
		combined.Args = append(combined.Args, raw.Args...)
		for _, key := range raw.Order {
			combined.Args = append(combined.Args, key.Expr)
		}
		combined.Within, combined.Order, combined.Window = false, nil, nil
		if _, err := b.bindFunction(combined); err != nil {
			return nil, err
		}
		name := objectNameText(raw.Name)
		return nil, expr.ErrorAt("42809", "WITHIN GROUP specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	eligible := len(raw.Name.Parts) == 1 && len(raw.Args) == 1 && len(raw.ArgNames) == 0 && !raw.Variadic && !raw.ResolutionOnly && raw.Syntax == parser.OrdinaryFunctionSyntax && raw.Filter == nil && raw.Window == nil && !raw.Star && !raw.Distinct && !raw.All && len(raw.Order) == 0
	previousProjection := b.clause.functionProjection
	b.clause.functionProjection = functionProjectionScope{}
	if eligible {
		b.clause.functionProjection = functionProjectionScope{name: raw.Name, pos: raw.Pos, argumentPos: raw.Args[0].SourcePosition(), argumentKind: projectionArgumentKind(raw.Args[0])}
	}
	defer func() { b.clause.functionProjection = previousProjection }()
	suppressed := eligible && directWholeCandidate(raw.Args[0])
	previous := b.query.suppressWholePresence
	if suppressed {
		b.query.suppressWholePresence = true
	}
	node, err := b.bindFunctionProvider(raw)
	b.query.suppressWholePresence = previous
	if err == nil {
		if suppressed && !previous {
			return b.demandWholePresence(node)
		}
		return node, nil
	}
	if !eligible || b.clause.functionProjection.noMatch != err || b.clause.functionProjection.operand == nil {
		return nil, err
	}
	base := b.clause.functionProjection.operand
	key := preparedCompositeKey{Offset: raw.Pos.Offset, Kind: CompositeSelection}
	captured, retained, failure := b.retainedComposite(key, base.ResultUsage())
	if failure != nil {
		return nil, failure
	}
	shape := base.ResultUsage().Type.BaseType()
	if retained {
		return b.retainedCompositeField(base, key, captured, raw.Pos), nil
	}
	index, found, failure := compositeFieldIndex(base, raw.Name.Parts[0].Text, raw.Pos)
	if failure != nil {
		return nil, failure
	}
	if found {
		member := shape.CompositeMember(index)
		b.prepared.CaptureComposite(key, base.ResultUsage(), []int32{member.Attribute})
		return b.compositeFieldAt(base, index, raw.Pos), nil
	}
	return nil, err
}

func compositeFieldIndex(base expr.Expr, name string, pos parser.Position) (int, bool, error) {
	shape := base.ResultUsage().Type.BaseType()
	if row, ok := base.(plan.RowExpr); ok && row.WholeNames != nil {
		index := -1
		for i, label := range row.WholeNames {
			if label != name {
				continue
			}
			if index >= 0 {
				return 0, false, expr.ErrorAt("42702", fmt.Sprintf("column reference %q is ambiguous", name), pos)
			}
			index = i
		}
		return index, index >= 0, nil
	}
	for i := range shape.CompositeLen() {
		if shape.CompositeMember(i).Name == name {
			return i, true, nil
		}
	}
	return 0, false, nil
}

func directWholeCandidate(raw parser.Expr) bool {
	switch raw.(type) {
	case parser.ColumnExpr, parser.StarExpr:
		return true
	}
	return false
}
func (b *expressionBinder) bindCompositeBase(raw parser.Expr) (expr.Expr, error) {
	previous := b.query.suppressWholePresence
	if directWholeCandidate(raw) {
		b.query.suppressWholePresence = true
	}
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	b.query.suppressWholePresence = previous
	return node, err
}
func (b *expressionBinder) demandWholePresence(node expr.Expr) (expr.Expr, error) {
	next, _, err := mapBoundExpressionReader(b.bindingCatalog(), b.definitionReader(), node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root plan.Node) (plan.Node, error) { return root, nil }, nil, false, func(child expr.Expr) (expr.Expr, bool, error) {
		row, ok := child.(plan.RowExpr)
		if !ok || row.WholeNames == nil || row.PresenceSlot != 0 {
			return child, false, nil
		}
		var namespace *queryNamespace
		if row.PresenceFrame == b.frameID() {
			namespace = b.query.namespace
		}
		if namespace == nil {
			for _, scope := range b.query.scopes {
				if scope.frame == row.PresenceFrame && scope.namespace != nil {
					namespace = scope.namespace
					break
				}
			}
		}
		if namespace == nil {
			return child, false, fmt.Errorf("stuntgres: missing whole-row demand namespace")
		}
		for i := range namespace.qualifiers {
			qualifier := &namespace.qualifiers[i]
			if qualifier.name != row.WholeName {
				continue
			}
			slot, err := b.wholeRowPresence(qualifier, row.PresenceFrame)
			if err != nil {
				return child, false, err
			}
			if slot == 0 {
				return child, false, nil
			}
			row.PresenceSlot = slot
			return row, true, nil
		}
		return child, false, fmt.Errorf("stuntgres: missing whole-row demand occurrence")
	})
	return next, err
}
