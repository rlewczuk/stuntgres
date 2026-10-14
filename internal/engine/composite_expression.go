package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type rowExpression struct {
	expressionInfo
	fields         []BoundExpr
	raw            *parser.RowExpr
	sources        []int
	wholeNames     []string
	wholeName      string
	wholeSource    wholeRowSource
	presenceSlot   int
	presenceFrame  uint32
	mergeSource    bool
	returningWhole bool   // Direct whole-row member projection uses the RETURNING output frame.
	returningNull  bool   // DELETE's scalar NEW whole row is NULL, independently of projection.
	ruleSide       uint32 // Resolved OLD/NEW whole-row identity, including zero members.
	expansions     map[int]compositeExpansionBinding
}

func (n rowExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if n.returningNull {
		return sqltype.Null(n.usage.Type), nil
	}
	absent, err := n.absent(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if absent {
		return sqltype.Null(n.usage.Type), nil
	}
	values := make([]sqltype.Value, len(n.fields))
	for i, child := range n.fields {
		v, err := child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return sqltype.OwnedCompositeValue(n.usage.Type, values)
}

type fieldExpression struct {
	expressionInfo
	base      BoundExpr
	attribute int32
	index     int
}

func (n fieldExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	// Analysis/preflight still visits every constructor child. Runtime demand
	// may bypass its allocation and unused volatile children.
	if row, ok := n.base.(rowExpression); ok {
		absent, err := row.absent(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if absent {
			return sqltype.Null(n.usage.Type), nil
		}
		return row.fields[n.index].Eval(c)
	}
	value, err := n.base.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.usage.Type), nil
	}
	index, ok := value.Type().BaseType().CompositeAttributeIndex(n.attribute)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: retired composite attribute")
	}
	return value.CompositeField(index), nil
}

func (b *expressionBinder) bindRow(raw parser.RowExpr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position) (BoundExpr, error) {
	list, err := b.expandRawList(raw.Fields)
	if err != nil {
		return nil, err
	}
	fields := make([]BoundExpr, len(list.raw))
	for i, child := range list.raw {
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
		return nil, expressionError("54011", "ROW expressions can have at most 1664 entries", raw.Pos)
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
					return nil, b.rowCoercionError(target, field.SourcePosition(), fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.diagnosticTypeName(field.ResultUsage().Type), b.diagnosticTypeName(target.Type.BaseType().CompositeMember(i).Usage.Type), i+1))
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
	props := ExprProperties{}
	for _, field := range fields {
		props = mergeExpressionProperties(props, field.Properties())
	}
	return rowExpression{expressionInfo: b.info(target, pos, props), fields: fields, raw: &raw, sources: list.sources, expansions: list.expansions}, nil
}
func (b *expressionBinder) rowCoercionError(target sqltype.Usage, pos parser.Position, detail string) error {
	d := expressionError("42846", "cannot cast type record to "+b.diagnosticTypeName(target.Type), pos).(*sqlstate.Error)
	d.Detail = detail
	return d
}
func (b *expressionBinder) assignCompositeMember(node BoundExpr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position) (BoundExpr, error) {
	if node.ResultUsage() == target {
		return node, nil
	}
	// Structural conversion is an anonymous RECORD authority, not permission
	// to reinterpret a named constructor or a domain's underlying members.
	if row, ok := node.(rowExpression); ok && row.usage.Type.ID() == 2249 && target.Type.BaseType().IsComposite() {
		if row.raw != nil {
			return b.bindRow(*row.raw, target, mode, pos)
		}
		if len(row.fields) != target.Type.BaseType().CompositeLen() {
			detail := "Input has too many columns."
			if len(row.fields) < target.Type.BaseType().CompositeLen() {
				detail = "Input has too few columns."
			}
			return nil, b.rowCoercionError(target, pos, detail)
		}
		fields := make([]BoundExpr, len(row.fields))
		props := ExprProperties{}
		for i, child := range row.fields {
			var err error
			fields[i], err = b.assignCompositeMember(child, target.Type.BaseType().CompositeMember(i).Usage, mode, pos)
			if err != nil {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" {
					return nil, b.rowCoercionError(target, child.SourcePosition(), fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.diagnosticTypeName(child.ResultUsage().Type), b.diagnosticTypeName(target.Type.BaseType().CompositeMember(i).Usage.Type), i+1))
				}
				return nil, err
			}
			props = mergeExpressionProperties(props, fields[i].Properties())
		}
		return rowExpression{expressionInfo: b.info(target, row.pos, props), fields: fields}, nil
	}
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), target, mode)
	if err != nil {
		return node, err
	}
	return coercionExpression{b.info(target, node.SourcePosition(), coercionProperties(node, plan)), node, plan, mode, false}, nil
}
func (b *expressionBinder) selectCompositeField(base BoundExpr, name string, pos parser.Position) (BoundExpr, error) {
	typ := base.ResultUsage().Type
	if typ == nil {
		return nil, expressionError("42809", "column notation ."+name+" applied to type unknown, which is not a composite type", pos)
	}
	shape := typ.BaseType()
	if !shape.IsComposite() {
		return nil, expressionError("42809", fmt.Sprintf("column notation .%s applied to type %s, which is not a composite type", name, b.diagnosticTypeName(typ)), pos)
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
	if row, ok := base.(rowExpression); ok && row.wholeNames != nil {
		message = fmt.Sprintf("column %s.%s does not exist", row.wholeName, name)
	}
	return nil, expressionError("42703", message, pos)
}
func (b *expressionBinder) compositeFieldAt(base BoundExpr, index int, pos parser.Position) BoundExpr {
	member := base.ResultUsage().Type.BaseType().CompositeMember(index)
	var field BoundExpr
	switch row := base.(type) {
	case rowExpression:
		if row.returningWhole {
			field = row.fields[index]
		}
	case returningWholeRowExpression:
		field = row.members[index]
	}
	if field != nil {
		var column columnExpression
		switch value := field.(type) {
		case returningColumnExpression:
			if value.merge {
				value.pos = pos
				return value
			}
			if value.value != nil {
				value.old, value.absent = false, false
				value.pos = pos
				return value
			}
			column = value.columnExpression
		default:
			return fieldExpression{b.info(member.Usage, pos, base.Properties()), base, member.Attribute, index}
		}
		column.pos = pos
		return column
	}
	return fieldExpression{b.info(member.Usage, pos, base.Properties()), base, member.Attribute, index}
}
func (b *expressionBinder) retainedCompositeField(base BoundExpr, key preparedCompositeKey, captured compositeOccurrence, pos parser.Position) BoundExpr {
	index, exists := base.ResultUsage().Type.BaseType().CompositeAttributeIndex(captured.attributes[0])
	b.captureComposite(key, captured.base, captured.attributes)
	if !exists {
		oldIndex, _ := captured.base.Type.BaseType().CompositeAttributeIndex(captured.attributes[0])
		usage := captured.base.Type.BaseType().CompositeMember(oldIndex).Usage
		return literalExpression{b.info(usage, pos, ExprProperties{}), sqltype.Null(usage.Type)}
	}
	return b.compositeFieldAt(base, index, pos)
}

func (b *expressionBinder) bindField(raw parser.FieldExpr) (BoundExpr, error) {
	if raw.Expansion {
		return nil, expressionError("0A000", `row expansion via "*" is not supported here`, raw.Pos)
	}
	base, err := b.bindCompositeBase(raw.Base)
	if err != nil {
		return nil, err
	}
	key := fieldCompositeKey(raw)
	if captured, found, err := b.retainedComposite(key, base.ResultUsage()); err != nil {
		return nil, err
	} else if found {
		if captured.kind == compositeDotBuiltin {
			return b.bindDotBuiltin(raw, key)
		}
		return b.retainedCompositeField(base, key, captured, raw.Pos), nil
	}
	node, err := b.selectCompositeField(base, raw.Field.Text, raw.Pos)
	if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42703" || diagnostic.Code == "42809") && functions.NullCountFunctionKind(raw.Field.Text) != 0 {
		return b.bindDotBuiltin(raw, key)
	}
	if field, ok := node.(fieldExpression); err == nil && ok {
		b.captureComposite(key, base.ResultUsage(), []int32{field.attribute})
	}
	return node, err
}

type compositeExpansionBinding struct {
	base       BoundExpr
	fields     []BoundExpr
	names      []string
	attributes []int32
}

func (b *expressionBinder) expandComposite(raw parser.FieldExpr) (compositeExpansionBinding, error) {
	base, err := b.bindCompositeBase(raw.Base)
	if err != nil {
		return compositeExpansionBinding{}, err
	}
	typ := base.ResultUsage().Type
	if typ == nil || !typ.BaseType().IsComposite() {
		name := "unknown"
		if typ != nil {
			name = b.diagnosticTypeName(typ)
		}
		return compositeExpansionBinding{}, sqlError("42809", "type "+name+" is not composite")
	}
	shape := typ.BaseType()
	key := fieldCompositeKey(raw)
	captured, found, err := b.retainedComposite(key, base.ResultUsage())
	if err != nil {
		return compositeExpansionBinding{}, err
	}
	attributes := captured.attributes
	if !found {
		attributes = make([]int32, shape.CompositeLen())
		for i := range attributes {
			attributes[i] = shape.CompositeMember(i).Attribute
		}
	}
	if found {
		b.captureComposite(key, captured.base, attributes)
	} else {
		b.captureComposite(key, base.ResultUsage(), attributes)
	}
	result := compositeExpansionBinding{base: base, fields: make([]BoundExpr, len(attributes)), names: make([]string, len(attributes)), attributes: attributes}
	for i, attribute := range attributes {
		index, exists := shape.CompositeAttributeIndex(attribute)
		if found {
			oldIndex, _ := captured.base.Type.BaseType().CompositeAttributeIndex(attribute)
			member := captured.base.Type.BaseType().CompositeMember(oldIndex)
			result.names[i] = member.Name
			if !exists {
				result.fields[i] = literalExpression{b.info(member.Usage, raw.Pos, ExprProperties{}), sqltype.Null(member.Usage.Type)}
				continue
			}
		} else {
			result.names[i] = shape.CompositeMember(index).Name
		}
		result.fields[i] = b.compositeFieldAt(base, index, raw.Pos)
		if row, ok := base.(rowExpression); ok && row.wholeNames != nil {
			result.names[i] = row.wholeNames[index]
		}
	}
	return result, nil
}

// asValue distinguishes an alias value (bare name or scalar star) from the
// exposed-column expansion used by a top-level qualified star or ROW(alias.*).
func (b *expressionBinder) wholeRow(name parser.ObjectName, pos parser.Position, asValue bool) (BoundExpr, error) {
	if b.ruleNamespace != nil && len(name.Parts) == 1 && (name.Parts[0].Text == "old" || name.Parts[0].Text == "new") {
		row, _, err := b.ruleColumn(parser.ColumnExpr{Name: name, Pos: pos})
		return row, err
	}
	if b.returningAliases != nil && len(name.Parts) == 1 {
		if expr, found, err := b.returningWholeRow(name.Parts[0].Text, b.relation, b.frameID(), b.returningAliases, pos); found {
			return expr, err
		}
		_, pseudo := b.returningPseudo(name)
		visible := b.targetName
		if b.targetAlias != "" {
			visible = b.targetAlias
		}
		if pseudo || name.Parts[0].Text == visible {
			newValues, err := b.returningQualifier(name, pos)
			if err != nil {
				return nil, err
			}
			fields := make([]BoundExpr, len(b.relation.columns))
			props := ExprProperties{}
			for i := range fields {
				fields[i] = b.returningColumnAt(i, pos, newValues)
				props = mergeExpressionProperties(props, fields[i].Properties())
			}
			typ, _ := b.bindingCatalog().LookupType(b.relation.rowType)
			return rowExpression{expressionInfo: b.info(typ.Usage(), pos, props), fields: fields, returningWhole: true, returningNull: b.returningAliases.mode == returningDelete && newValues}, nil
		}
	}
	if scope, newValues := b.outerReturningScope(name); scope != nil {
		return b.outerReturningWholeRow(scope, pos, newValues)
	}
	q, err := b.queryQualifier(name, pos)
	if err != nil {
		return nil, err
	}
	frame := b.resolvedQueryFrame
	if asValue && q.scalarAlias {
		return b.queryColumnInFrame(q.columns[0], pos, frame), nil
	}
	fields := make([]BoundExpr, len(q.columns))
	members := make([]sqltype.CompositeMember, len(fields))
	props := ExprProperties{}
	names := make([]string, len(fields))
	for i, col := range q.columns {
		fields[i] = b.queryColumnInFrame(col, pos, frame)
		names[i] = col.descriptor.Name
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: col.descriptor.Name, Usage: col.descriptor.Usage}
		props = mergeExpressionProperties(props, fields[i].Properties())
	}
	props.RowDependent = true
	var typ *sqltype.Descriptor
	if relation, ok := b.bindingCatalog().state.descriptors[q.ref.Address].(*relationDescriptor); ok && relation.rowType != 0 {
		typ, _ = b.bindingCatalog().LookupType(relation.rowType)
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
	if asValue && !b.suppressWholePresence {
		slot, err = b.wholeRowPresence(q, frame)
		if err != nil {
			return nil, err
		}
	}
	// A mutation target has no FROM producer; actual source qualifiers retain
	// their producer even with zero members or the same name in another schema.
	mergeSource := b.mergeReturningFrame(frame) && q.sourceRoot != nil
	return rowExpression{expressionInfo: b.info(typ.Usage(), pos, props), fields: fields, wholeNames: names, wholeName: q.name, wholeSource: source, presenceSlot: slot, presenceFrame: frame, mergeSource: mergeSource}, nil
}

func mapRow(row rowExpression, child func(BoundExpr) (BoundExpr, bool, error)) (rowExpression, bool, error) {
	var fields []BoundExpr
	for i, old := range row.fields {
		next, changed, err := child(old)
		if err != nil {
			return row, false, err
		}
		if changed {
			if fields == nil {
				fields = append([]BoundExpr(nil), row.fields...)
			}
			fields[i] = next
		}
	}
	if fields == nil {
		return row, false, nil
	}
	row.fields = fields
	return row, true, nil
}

// Analysis associations preserve one raw occurrence across expanded slots.
// Catalog/type mapping also reowns these retained associations; runtime demand
// and grouping rewrites deliberately map only the executable fields.
func mapRowAnalysis(row rowExpression, child func(BoundExpr) (BoundExpr, bool, error)) (rowExpression, bool, error) {
	next, changed, err := mapRow(row, child)
	if err != nil {
		return row, false, err
	}
	var expansions map[int]compositeExpansionBinding
	for source, expansion := range row.expansions {
		base, replaced, err := child(expansion.base)
		if err != nil {
			return row, false, err
		}
		var fields []BoundExpr
		for i, old := range expansion.fields {
			field, mapped, err := child(old)
			if err != nil {
				return row, false, err
			}
			if mapped {
				if fields == nil {
					fields = append([]BoundExpr(nil), expansion.fields...)
				}
				fields[i] = field
			}
		}
		if !replaced && fields == nil {
			continue
		}
		if expansions == nil {
			expansions = make(map[int]compositeExpansionBinding, len(row.expansions))
			for key, value := range row.expansions {
				expansions[key] = value
			}
		}
		expansion.base = base
		if fields != nil {
			expansion.fields = fields
		}
		expansions[source] = expansion
	}
	if expansions != nil {
		next.expansions = expansions
		changed = true
	}
	return next, changed, nil
}

// Expression-list expansion preserves original structural source occurrences.
type expandedExpressionList struct {
	raw        []parser.Expr
	nodes      []BoundExpr
	sources    []int
	expansions map[int]compositeExpansionBinding
}

func (b *expressionBinder) expandRawList(raw []parser.Expr) (expandedExpressionList, error) {
	result := expandedExpressionList{raw: raw}
	for i, expression := range raw {
		var expansion compositeExpansionBinding
		var err error
		var base parser.Expr
		if field, ok := expression.(parser.FieldExpr); ok && field.Expansion {
			expansion, err = b.expandComposite(field)
			base = field.Base
		} else if star, ok := expression.(parser.StarExpr); ok {
			expansion.base, err = b.wholeRow(star.Qualifier, star.Pos, false)
			base = star
			if err == nil {
				row := expansion.base.(rowExpression)
				expansion.fields = row.fields
				shape := row.usage.Type.BaseType()
				expansion.names = make([]string, len(row.fields))
				expansion.attributes = make([]int32, len(row.fields))
				for j := range row.fields {
					member := shape.CompositeMember(j)
					expansion.names[j] = member.Name
					expansion.attributes[j] = member.Attribute
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
			result.raw = make([]parser.Expr, i, len(raw)+len(expansion.fields))
			copy(result.raw, raw[:i])
			result.nodes = make([]BoundExpr, i, cap(result.raw))
			result.sources = make([]int, i, cap(result.raw))
			for j := range i {
				result.sources[j] = j
			}
			result.expansions = make(map[int]compositeExpansionBinding)
		}
		result.expansions[i] = expansion
		for j, node := range expansion.fields {
			result.raw = append(result.raw, parser.FieldExpr{Base: base, Field: parser.Identifier{Text: expansion.names[j]}, Pos: expression.SourcePosition()})
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
	operand      BoundExpr
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
func (b *expressionBinder) noteFunctionSignature(name parser.ObjectName, pos parser.Position, err error) {
	scope := &b.functionProjection
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

func (b *expressionBinder) bindFunction(raw parser.FunctionExpr) (result BoundExpr, failure error) {
	before := b.fromSetObservation
	defer func() {
		if failure != nil || b.aggregateClause != "FROM" || !isQuerySet(result) {
			return
		}
		if b.fromSetObservation != before {
			result, failure = nil, nestedFromSetError(b.fromSetObservation.pos)
			return
		}
		b.fromSetObservation.generation++
		b.fromSetObservation.pos = raw.Pos
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
		return nil, expressionError("42809", "WITHIN GROUP specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	eligible := len(raw.Name.Parts) == 1 && len(raw.Args) == 1 && len(raw.ArgNames) == 0 && !raw.Variadic && !raw.ResolutionOnly && raw.Syntax == parser.OrdinaryFunctionSyntax && raw.Filter == nil && raw.Window == nil && !raw.Star && !raw.Distinct && !raw.All && len(raw.Order) == 0
	previousProjection := b.functionProjection
	b.functionProjection = functionProjectionScope{}
	if eligible {
		b.functionProjection = functionProjectionScope{name: raw.Name, pos: raw.Pos, argumentPos: raw.Args[0].SourcePosition(), argumentKind: projectionArgumentKind(raw.Args[0])}
	}
	defer func() { b.functionProjection = previousProjection }()
	suppressed := eligible && directWholeCandidate(raw.Args[0])
	previous := b.suppressWholePresence
	if suppressed {
		b.suppressWholePresence = true
	}
	node, err := b.bindFunctionProvider(raw)
	b.suppressWholePresence = previous
	if err == nil {
		if suppressed && !previous {
			return b.demandWholePresence(node)
		}
		return node, nil
	}
	if !eligible || b.functionProjection.noMatch != err || b.functionProjection.operand == nil {
		return nil, err
	}
	base := b.functionProjection.operand
	key := preparedCompositeKey{offset: raw.Pos.Offset, kind: compositeSelection}
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
		b.captureComposite(key, base.ResultUsage(), []int32{member.Attribute})
		return b.compositeFieldAt(base, index, raw.Pos), nil
	}
	return nil, err
}

func compositeFieldIndex(base BoundExpr, name string, pos parser.Position) (int, bool, error) {
	shape := base.ResultUsage().Type.BaseType()
	if row, ok := base.(rowExpression); ok && row.wholeNames != nil {
		index := -1
		for i, label := range row.wholeNames {
			if label != name {
				continue
			}
			if index >= 0 {
				return 0, false, expressionError("42702", fmt.Sprintf("column reference %q is ambiguous", name), pos)
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
func (b *expressionBinder) bindCompositeBase(raw parser.Expr) (BoundExpr, error) {
	previous := b.suppressWholePresence
	if directWholeCandidate(raw) {
		b.suppressWholePresence = true
	}
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	b.suppressWholePresence = previous
	return node, err
}
func (b *expressionBinder) demandWholePresence(node BoundExpr) (BoundExpr, error) {
	next, _, err := mapBoundExpression(b.bindingCatalog(), node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, func(root LogicalNode) (LogicalNode, error) { return root, nil }, func(child BoundExpr) (BoundExpr, bool, error) {
		row, ok := child.(rowExpression)
		if !ok || row.wholeNames == nil || row.presenceSlot != 0 {
			return child, false, nil
		}
		var namespace *queryNamespace
		if row.presenceFrame == b.frameID() {
			namespace = b.queryNamespace
		}
		if namespace == nil {
			for _, scope := range b.queryScopes {
				if scope.frame == row.presenceFrame && scope.namespace != nil {
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
			if qualifier.name != row.wholeName {
				continue
			}
			slot, err := b.wholeRowPresence(qualifier, row.presenceFrame)
			if err != nil {
				return child, false, err
			}
			if slot == 0 {
				return child, false, nil
			}
			row.presenceSlot = slot
			return row, true, nil
		}
		return child, false, fmt.Errorf("stuntgres: missing whole-row demand occurrence")
	})
	return next, err
}
