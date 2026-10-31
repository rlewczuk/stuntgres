package analyze

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) BindRow(raw parser.RowExpr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position) (expr.Expr, error) {
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
		policy := ExpressionPolicy{Context: ProjectionContext, Expected: expected}
		if expected.Type != nil {
			policy.Context = FunctionArgumentContext
			policy.Mode = mode
			// The enclosing ROW owns member conversion. Preserve its synthetic
			// origin instead of manufacturing an unwritten member CastExpr.
			policy.deferAssignment = true
		}
		node, err := b.Bind(child, policy)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && node != nil && (d.Code == "42804" || d.Code == "42846") && expected.Type != nil {
				location := pos
				if node.ResultUsage().Type.BaseType().IsComposite() {
					location = node.SourcePosition()
				}
				return nil, b.rowCoercionError(target, location, fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.DiagnosticTypeName(node.ResultUsage().Type), b.DiagnosticTypeName(expected.Type), i+1))
			}
			return nil, err
		}
		fields[i], err = b.FinalizeProjection(node)
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
					return nil, b.rowCoercionError(target, location, fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.DiagnosticTypeName(field.ResultUsage().Type), b.DiagnosticTypeName(target.Type.BaseType().CompositeMember(i).Usage.Type), i+1))
				}
				return nil, err
			}
		}
	} else {
		target = sqltype.Usage{}
		key := PreparedTypeKey{Offset: raw.Pos.Offset, Kind: RecordConstructor, Projection: int(b.FrameID())}
		retained := b.Expected.Types[key].Base
		if retained.Type != nil && retained.Type.IsComposite() && retained.Type.CompositeLen() == len(fields) {
			same := true
			for i, field := range fields {
				if retained.Type.CompositeMember(i).Usage != field.ResultUsage() {
					same = false
					break
				}
			}
			if same {
				target = retained
			}
		}
		if target.Type == nil {
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
		b.Prepared.CaptureComposite(key, target, nil)
	}
	props := expr.Properties{}
	for _, field := range fields {
		props = expr.MergeProperties(props, field.Properties())
	}
	return plan.RowExpr{Info: b.Info(target, pos, props), Fields: fields, Raw: &raw, Sources: list.sources, Expansions: list.expansions}, nil
}
func (b *Binder) rowCoercionError(target sqltype.Usage, pos parser.Position, detail string) error {
	d := expr.ErrorAt("42846", "cannot cast type record to "+b.DiagnosticTypeName(target.Type), pos).(*sqlstate.Error)
	d.Detail = detail
	return d
}
func (b *Binder) assignCompositeMember(node expr.Expr, target sqltype.Usage, mode sqltype.CoercionContext, pos parser.Position) (expr.Expr, error) {
	if node.ResultUsage() == target {
		return node, nil
	}
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, err
	}
	if target.Type.ContainsCatalogIdentifier() {
		if err := b.captureElementInput(); err != nil {
			return nil, err
		}
	}
	// Structural conversion is an anonymous RECORD authority, not permission
	// to reinterpret a named constructor or a domain's underlying members.
	if row, ok := node.(plan.RowExpr); ok && row.Usage.Type.ID() == 2249 && target.Type.BaseType().IsComposite() {
		if row.Raw != nil {
			return b.BindRow(*row.Raw, target, mode, pos)
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
					return nil, b.rowCoercionError(target, child.SourcePosition(), fmt.Sprintf("Cannot cast type %s to %s in column %d.", b.DiagnosticTypeName(child.ResultUsage().Type), b.DiagnosticTypeName(target.Type.BaseType().CompositeMember(i).Usage.Type), i+1))
				}
				return nil, err
			}
			props = expr.MergeProperties(props, fields[i].Properties())
		}
		return plan.RowExpr{Info: b.Info(target, row.Pos, props), Fields: fields}, nil
	}
	if selected, handled, selectionErr := b.declaredCoercion(node, target, mode, pos, nil); handled {
		return selected, selectionErr
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), target, mode)
	if err != nil {
		return node, err
	}
	return plan.CoercionExpr{Info: b.Info(target, node.SourcePosition(), CoercionProperties(node, coercionPlan)), Child: node, Plan: coercionPlan, Mode: mode, SyntheticConstructorElement: true}, nil
}
func (b *Binder) SelectCompositeField(base expr.Expr, name string, pos parser.Position) (expr.Expr, error) {
	typ := base.ResultUsage().Type
	if typ == nil {
		return nil, expr.ErrorAt("42809", "column notation ."+name+" applied to type unknown, which is not a composite type", base.SourcePosition())
	}
	shape := typ.BaseType()
	if !shape.IsComposite() {
		return nil, expr.ErrorAt("42809", fmt.Sprintf("column notation .%s applied to type %s, which is not a composite type", name, b.DiagnosticTypeName(typ)), base.SourcePosition())
	}
	index, found, err := compositeFieldIndex(base, name, pos)
	if err != nil {
		return nil, err
	}
	if found {
		return b.compositeFieldAt(base, index, pos)
	}
	message := fmt.Sprintf("column %q not found in data type %s", name, b.DiagnosticTypeName(typ))
	if shape.ID() == 2249 {
		message = fmt.Sprintf("could not identify column %q in record data type", name)
	}
	if row, ok := base.(plan.RowExpr); ok && row.WholeNames != nil {
		message = fmt.Sprintf("column %s.%s does not exist", row.WholeName, name)
	}
	return nil, expr.ErrorAt("42703", message, pos)
}
func (b *Binder) compositeFieldAt(base expr.Expr, index int, pos parser.Position) (expr.Expr, error) {
	shape := base.ResultUsage().Type.BaseType()
	member := shape.CompositeMember(index)
	if shape.ID() == 10029 && shape.Usage().CatalogNullUsage() {
		descriptor, _ := b.definitionDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(shape.ID())})
		typ, ok := descriptor.(*catalog.Type)
		if !ok || typ.SQLType != shape {
			return nil, fmt.Errorf("stuntgres: invalid statistic field type")
		}
		read := b.BindingCatalog()
		b.dependency(read.Owner().Domain.Ref(read.Epoch(), typ.Address()), typ.Revision())
	} else if shape.ID() != 2249 {
		descriptor, _ := b.definitionDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(shape.ID())})
		typ, ok := descriptor.(*catalog.Type)
		if !ok || typ.SQLType != shape || typ.OwningRelation == (ids.ObjectAddress{}) {
			return nil, fmt.Errorf("stuntgres: invalid composite field type")
		}
		descriptor, _ = b.definitionDescriptor(typ.OwningRelation)
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RowType != shape.ID() {
			return nil, fmt.Errorf("stuntgres: invalid composite field owner")
		}
		slot := relation.IndexForAttribute(member.Attribute)
		if slot < 0 || relation.Columns[slot].Name != member.Name || relation.Columns[slot].Usage != member.Usage {
			return nil, fmt.Errorf("stuntgres: invalid composite attribute")
		}
		address := relation.Address()
		address.SubID = member.Attribute
		read := b.BindingCatalog()
		b.dependency(read.Owner().Domain.Ref(read.Epoch(), address), relation.Revision())
	}
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
				return value, nil
			}
			if value.Value != nil {
				value.Old, value.Absent = false, false
				value.Pos = pos
				return value, nil
			}
			column = value.ColumnExpr
		default:
			return plan.FieldExpr{Info: b.Info(member.Usage, pos, base.Properties()), Base: base, Attribute: member.Attribute, Index: index}, nil
		}
		column.Pos = pos
		return column, nil
	}
	return plan.FieldExpr{Info: b.Info(member.Usage, pos, base.Properties()), Base: base, Attribute: member.Attribute, Index: index}, nil
}
func (b *Binder) RetainedCompositeField(base expr.Expr, key PreparedTypeKey, captured TypeOccurrence, pos parser.Position) (expr.Expr, error) {
	index, exists := base.ResultUsage().Type.BaseType().CompositeAttributeIndex(captured.Attributes[0])
	b.Prepared.CaptureComposite(key, captured.Base, captured.Attributes)
	if !exists {
		oldIndex, _ := captured.Base.Type.BaseType().CompositeAttributeIndex(captured.Attributes[0])
		usage := captured.Base.Type.BaseType().CompositeMember(oldIndex).Usage
		return plan.LiteralExpr{Info: b.Info(usage, pos, expr.Properties{}), Value: sqltype.Null(usage.Type)}, nil
	}
	return b.compositeFieldAt(base, index, pos)
}

func (b *Binder) bindField(raw parser.FieldExpr) (expr.Expr, error) {
	if raw.Expansion {
		return nil, expr.ErrorAt("0A000", `row expansion via "*" is not supported here`, raw.Pos)
	}
	base, err := b.BindCompositeBase(raw.Base)
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
		return b.RetainedCompositeField(base, key, captured, raw.Pos)
	}
	node, err := b.SelectCompositeField(base, raw.Field.Text, raw.Pos)
	if diagnostic, ok := err.(*sqlstate.Error); ok && (diagnostic.Code == "42703" || diagnostic.Code == "42809") && functions.NullCountFunctionKind(raw.Field.Text) != 0 {
		return b.bindDotBuiltin(raw, key)
	}
	if field, ok := node.(plan.FieldExpr); err == nil && ok {
		b.Prepared.CaptureComposite(key, base.ResultUsage(), []int32{field.Attribute})
	}
	return node, err
}

func (b *Binder) ExpandComposite(raw parser.FieldExpr) (plan.CompositeExpansionBinding, error) {
	base, err := b.BindCompositeBase(raw.Base)
	if err != nil {
		return plan.CompositeExpansionBinding{}, err
	}
	if JSONBRecordUnresolved(base) {
		return plan.CompositeExpansionBinding{}, sqlError("42809", "record type has not been registered")
	}
	typ := base.ResultUsage().Type
	if typ == nil || !typ.BaseType().IsComposite() {
		name := "unknown"
		if typ != nil {
			name = b.DiagnosticTypeName(typ)
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
		b.Prepared.CaptureComposite(key, captured.Base, attributes)
	} else {
		b.Prepared.CaptureComposite(key, base.ResultUsage(), attributes)
	}
	result := plan.CompositeExpansionBinding{Base: base, Fields: make([]expr.Expr, len(attributes)), Names: make([]string, len(attributes)), Attributes: attributes}
	for i, attribute := range attributes {
		index, exists := shape.CompositeAttributeIndex(attribute)
		if found {
			oldIndex, _ := captured.Base.Type.BaseType().CompositeAttributeIndex(attribute)
			member := captured.Base.Type.BaseType().CompositeMember(oldIndex)
			result.Names[i] = member.Name
			if !exists {
				result.Fields[i] = plan.LiteralExpr{Info: b.Info(member.Usage, raw.Pos, expr.Properties{}), Value: sqltype.Null(member.Usage.Type)}
				continue
			}
		} else {
			result.Names[i] = shape.CompositeMember(index).Name
		}
		result.Fields[i], err = b.compositeFieldAt(base, index, raw.Pos)
		if err != nil {
			return plan.CompositeExpansionBinding{}, err
		}
		if row, ok := base.(plan.RowExpr); ok && row.WholeNames != nil {
			result.Names[i] = row.WholeNames[index]
		}
	}
	return result, nil
}

// asValue distinguishes an alias value (bare name or scalar star) from the
// exposed-column expansion used by a top-level qualified star or ROW(alias.*).
func (b *Binder) wholeRow(name parser.ObjectName, pos parser.Position, asValue bool) (expr.Expr, error) {
	if b.Target.RuleNamespace != nil && len(name.Parts) == 1 && (name.Parts[0].Text == "old" || name.Parts[0].Text == "new") {
		if row, matched, err := b.RuleColumn(parser.ColumnExpr{Name: name, Pos: pos}); matched {
			return row, err
		}
	}
	if b.Target.returningAliases != nil && len(name.Parts) == 1 {
		if expr, found, err := b.returningWholeRow(name.Parts[0].Text, b.Target.Relation, b.FrameID(), b.Target.returningAliases, pos); found {
			return expr, err
		}
		_, pseudo := b.Target.returningPseudo(name)
		visible := b.Target.Name
		if b.Target.alias != "" {
			visible = b.Target.alias
		}
		if pseudo || name.Parts[0].Text == visible {
			newValues, err := b.returningQualifier(name, pos)
			if err != nil {
				return nil, err
			}
			fields := make([]expr.Expr, len(b.Target.Relation.Columns))
			props := expr.Properties{}
			for i := range fields {
				fields[i] = b.returningColumnAt(i, pos, newValues)
				props = expr.MergeProperties(props, fields[i].Properties())
			}
			typ, _ := b.definitionType(b.Target.Relation.RowType)
			return plan.RowExpr{Info: b.Info(typ.Usage(), pos, props), Fields: fields, ReturningWhole: true, ReturningNull: b.Target.returningAliases.mode == ReturningDelete && newValues}, nil
		}
	}
	if scope, newValues := b.outerReturningScope(name); scope != nil {
		return b.outerReturningWholeRow(scope, pos, newValues)
	}
	q, err := b.queryQualifier(name, pos)
	if err != nil {
		return nil, err
	}
	frame := b.Query.resolvedQueryFrame
	if asValue && q.ScalarAlias {
		return b.queryColumnInFrame(q.Columns[0], pos, frame), nil
	}
	fields := make([]expr.Expr, len(q.Columns))
	members := make([]sqltype.CompositeMember, len(fields))
	props := expr.Properties{}
	names := make([]string, len(fields))
	for i, col := range q.Columns {
		fields[i] = b.queryColumnInFrame(col, pos, frame)
		names[i] = col.Descriptor.Name
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: col.Descriptor.Name, Usage: col.Descriptor.Usage}
		props = expr.MergeProperties(props, fields[i].Properties())
	}
	props.RowDependent = true
	var typ *sqltype.Descriptor
	descriptor, _ := b.definitionDescriptor(q.Ref.Address)
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
	if asValue && !b.Query.suppressWholePresence {
		slot, err = b.WholeRowPresence(q, frame)
		if err != nil {
			return nil, err
		}
	}
	// A mutation target has no FROM producer; actual source qualifiers retain
	// their producer even with zero members or the same name in another schema.
	mergeSource := b.mergeReturningFrame(frame) && q.SourceRoot != nil
	return plan.RowExpr{Info: b.Info(typ.Usage(), pos, props), Fields: fields, WholeNames: names, WholeName: q.Name, WholeSource: source, PresenceSlot: slot, PresenceFrame: frame, MergeSource: mergeSource}, nil
}

// Expression-list expansion preserves original structural source occurrences.
type expandedExpressionList struct {
	raw        []parser.Expr
	nodes      []expr.Expr
	sources    []int
	expansions map[int]plan.CompositeExpansionBinding
}

func (b *Binder) expandRawList(raw []parser.Expr) (expandedExpressionList, error) {
	result := expandedExpressionList{raw: raw}
	for i, expression := range raw {
		var expansion plan.CompositeExpansionBinding
		var err error
		var base parser.Expr
		if field, ok := expression.(parser.FieldExpr); ok && field.Expansion {
			expansion, err = b.ExpandComposite(field)
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
type FunctionProjectionScope struct {
	name         parser.ObjectName
	pos          parser.Position
	ArgumentPos  parser.Position
	ArgumentKind uint8
	Operand      expr.Expr
	noMatch      error
}

func ProjectionArgumentKind(raw parser.Expr) uint8 {
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
func (s *FunctionProjectionScope) matches(raw parser.Expr) bool {
	return s.ArgumentKind != 0 && raw.SourcePosition() == s.ArgumentPos && ProjectionArgumentKind(raw) == s.ArgumentKind
}
func (c *ClauseState) noteFunctionSignature(name parser.ObjectName, pos parser.Position, err error) {
	scope := &c.FunctionProjection
	if scope.ArgumentKind == 0 || scope.pos != pos || len(name.Parts) != len(scope.name.Parts) {
		return
	}
	for i := range name.Parts {
		if name.Parts[i].Text != scope.name.Parts[i].Text {
			return
		}
	}
	scope.noMatch = err
}

func (b *Binder) BindFunction(raw parser.FunctionExpr) (result expr.Expr, failure error) {
	before := b.Clause.FromSetObservation
	defer func() {
		if failure != nil || b.Clause.AggregateClause != "FROM" || !exec.SourceReturnsSet(result) {
			return
		}
		if b.Clause.FromSetObservation != before {
			result, failure = nil, nestedFromSetError(b.Clause.FromSetObservation.pos)
			return
		}
		b.Clause.FromSetObservation.generation++
		b.Clause.FromSetObservation.pos = raw.Pos
	}()
	if raw.Within {
		combined := raw
		combined.CompositeCandidate = false
		combined.Args = make([]parser.Expr, 0, len(raw.Args)+len(raw.Order))
		combined.Args = append(combined.Args, raw.Args...)
		for _, key := range raw.Order {
			combined.Args = append(combined.Args, key.Expr)
		}
		selected, handled, err := b.selectSQLCallable(combined, nil)
		if err != nil {
			return nil, err
		}
		if handled && selected.Procedure != nil && selected.Procedure.Aggregate != nil {
			original := parser.AggregateExpr{Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text, Args: raw.Args, Order: raw.Order, Within: true, WithinPos: raw.WithinPos, Pos: raw.Pos, Filter: raw.Filter, Window: raw.Window, Star: raw.Star, Distinct: raw.Distinct, Variadic: raw.Variadic}
			if len(raw.Name.Parts) == 2 {
				original.Schema = &raw.Name.Parts[0]
			}
			return b.finishSQLCallable(combined, selected, original, ExpressionPolicy{Context: ProjectionContext})
		}
		combined.Within, combined.Order, combined.Window = false, nil, nil
		if _, err := b.BindFunction(combined); err != nil {
			return nil, err
		}
		name := catalog.ObjectNameText(raw.Name)
		return nil, expr.ErrorAt("42809", "WITHIN GROUP specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	eligible := len(raw.Name.Parts) == 1 && len(raw.Args) == 1 && len(raw.ArgNames) == 0 && !raw.Variadic && !raw.ResolutionOnly && raw.Syntax == parser.OrdinaryFunctionSyntax && raw.Filter == nil && raw.Window == nil && !raw.Star && !raw.Distinct && !raw.All && len(raw.Order) == 0
	previousProjection := b.Clause.FunctionProjection
	b.Clause.FunctionProjection = FunctionProjectionScope{}
	if eligible {
		b.Clause.FunctionProjection = FunctionProjectionScope{name: raw.Name, pos: raw.Pos, ArgumentPos: raw.Args[0].SourcePosition(), ArgumentKind: ProjectionArgumentKind(raw.Args[0])}
	}
	defer func() { b.Clause.FunctionProjection = previousProjection }()
	suppressed := eligible && directWholeCandidate(raw.Args[0])
	previous := b.Query.suppressWholePresence
	if suppressed {
		b.Query.suppressWholePresence = true
	}
	node, err := b.BindFunctionProvider(raw)
	b.Query.suppressWholePresence = previous
	if err == nil {
		if suppressed && !previous {
			return b.demandWholePresence(node)
		}
		return node, nil
	}
	if !eligible || b.Clause.FunctionProjection.noMatch != err || b.Clause.FunctionProjection.Operand == nil {
		return nil, err
	}
	base := b.Clause.FunctionProjection.Operand
	key := PreparedTypeKey{Offset: raw.Pos.Offset, Kind: CompositeSelection}
	captured, retained, failure := b.retainedComposite(key, base.ResultUsage())
	if failure != nil {
		return nil, failure
	}
	shape := base.ResultUsage().Type.BaseType()
	if retained {
		return b.RetainedCompositeField(base, key, captured, raw.Pos)
	}
	index, found, failure := compositeFieldIndex(base, raw.Name.Parts[0].Text, raw.Pos)
	if failure != nil {
		return nil, failure
	}
	if found {
		member := shape.CompositeMember(index)
		b.Prepared.CaptureComposite(key, base.ResultUsage(), []int32{member.Attribute})
		return b.compositeFieldAt(base, index, raw.Pos)
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
func (b *Binder) BindCompositeBase(raw parser.Expr) (expr.Expr, error) {
	previous := b.Query.suppressWholePresence
	if directWholeCandidate(raw) {
		b.Query.suppressWholePresence = true
	}
	node, err := b.Bind(raw, ExpressionPolicy{Context: ProjectionContext})
	b.Query.suppressWholePresence = previous
	return node, err
}
func (b *Binder) demandWholePresence(node expr.Expr) (expr.Expr, error) {
	next, err := b.Host.MapBoundExpression(b.BindingCatalog(), b.DefinitionReader(), node, false, func(child expr.Expr) (expr.Expr, bool, error) {
		row, ok := child.(plan.RowExpr)
		if !ok || row.WholeNames == nil || row.PresenceSlot != 0 {
			return child, false, nil
		}
		var namespace *QueryNamespace
		if row.PresenceFrame == b.FrameID() {
			namespace = b.Query.Namespace
		}
		if namespace == nil {
			for _, scope := range b.Query.Scopes {
				if scope.frame == row.PresenceFrame && scope.namespace != nil {
					namespace = scope.namespace
					break
				}
			}
		}
		if namespace == nil {
			return child, false, fmt.Errorf("stuntgres: missing whole-row demand namespace")
		}
		for i := range namespace.Qualifiers {
			qualifier := &namespace.Qualifiers[i]
			if qualifier.Name != row.WholeName {
				continue
			}
			slot, err := b.WholeRowPresence(qualifier, row.PresenceFrame)
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
