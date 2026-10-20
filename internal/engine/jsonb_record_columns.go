package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Only the analyzed projection is frozen. The function source keeps its current
// complete schema and still converts every destination member before yielding.
func (b *expressionBinder) jsonbRecordStarColumns(columns []queryColumn, frame uint32, star parser.StarExpr, qualified *queryQualifier) ([]queryColumn, error) {
	if qualified != nil {
		return b.jsonbRecordQualifierStar(columns, frame, star, *qualified)
	}
	if b.query.namespace == nil {
		return columns, nil
	}
	for _, qualifier := range b.query.namespace.Qualifiers {
		var err error
		columns, err = b.jsonbRecordQualifierStar(columns, frame, star, qualifier)
		if err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func (b *expressionBinder) jsonbRecordQualifierStar(columns []queryColumn, frame uint32, star parser.StarExpr, qualifier queryQualifier) ([]queryColumn, error) {
	source, ok := qualifier.SourceRoot.(*plan.FunctionFromNode)
	if !ok || source.Frame != frame {
		return columns, nil
	}
	offset := 0
	for _, expression := range source.Calls {
		width := exec.FunctionFromCallWidth(expression)
		if call, selected := expression.(functions.JSONBRecord); selected && call.ResultUsage().Type.BaseType().IsComposite() {
			if offset+width > len(qualifier.Columns) {
				return nil, fmt.Errorf("stuntgres: invalid JSONB record source span")
			}
			var err error
			columns, err = b.jsonbRecordCallStar(columns, qualifier.Columns[offset:offset+width], star, call)
			if err != nil {
				return nil, err
			}
		}
		offset += width
	}
	return columns, nil
}

func (b *expressionBinder) jsonbRecordCallStar(columns, source []queryColumn, star parser.StarExpr, call functions.JSONBRecord) ([]queryColumn, error) {
	usage := call.ResultUsage()
	shape := usage.Type.BaseType()
	key := preparedCompositeKey{Offset: call.SourcePosition().Offset, Kind: analyze.CompositeSourceExpansion, Projection: star.Pos.Offset}
	captured, retained, err := b.retainedComposite(key, usage)
	if err != nil {
		return nil, err
	}
	first := 0
	if len(source) != 0 {
		first = source[0].Slot
	}
	if !retained {
		captured = compositeOccurrence{Kind: analyze.CompositeSourceExpansion, Base: usage, Attributes: make([]int32, 0, len(source)), Names: make([]string, 0, len(source))}
		for _, column := range columns {
			index := column.Slot - first
			if index >= 0 && index < len(source) {
				captured.Attributes = append(captured.Attributes, shape.CompositeMember(index).Attribute)
				captured.Names = append(captured.Names, column.Descriptor.Name)
			}
		}
	}
	if len(captured.Names) != len(captured.Attributes) {
		return nil, fmt.Errorf("stuntgres: invalid retained JSONB record labels")
	}
	if b.prepared.Composites == nil {
		b.prepared.Composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	b.prepared.Composites[key] = captured
	if !retained {
		return columns, nil
	}
	for _, attribute := range captured.Attributes {
		if _, exists := shape.CompositeAttributeIndex(attribute); !exists {
			return nil, sqlError("42703", fmt.Sprintf("attribute %d of type record has been dropped", attribute))
		}
	}
	if len(source) == 0 {
		return columns, nil
	}
	count, changed := 0, false
	for _, column := range columns {
		index := column.Slot - first
		if index >= 0 && index < len(source) {
			if count >= len(captured.Attributes) || captured.Attributes[count] != shape.CompositeMember(index).Attribute || captured.Names[count] != column.Descriptor.Name {
				changed = true
			}
			count++
		}
	}
	if count != len(captured.Attributes) {
		changed = true
	}
	if !changed {
		return columns, nil
	}
	next := make([]queryColumn, 0, len(columns))
	emitted := false
	for _, column := range columns {
		index := column.Slot - first
		if index < 0 || index >= len(source) {
			next = append(next, column)
			continue
		}
		if emitted {
			continue
		}
		for i, attribute := range captured.Attributes {
			member, _ := shape.CompositeAttributeIndex(attribute)
			retainedColumn := source[member]
			retainedColumn.Descriptor.Name = captured.Names[i]
			next = append(next, retainedColumn)
		}
		emitted = true
	}
	return next, nil
}

// A retained base cast selects the current spelling of its original provider,
// then goes through ordinary annotation resolution and privilege checks. Never
// look up an old OID across a catalog owner/epoch boundary or retry a failed name.
func (b *expressionBinder) jsonbRecordCastAnnotation(raw parser.CastExpr) (parser.TypeName, error) {
	annotation := raw.TypeName
	if !b.clause.jsonbRecordBase || raw.Syntax == parser.FunctionCast || !b.expected.Held {
		return annotation, nil
	}
	view := b.bindingCatalog()
	if b.expected.CatalogOwner != view.Owner() || b.expected.CatalogEpoch != view.Epoch() {
		return annotation, nil
	}
	key := preparedCompositeKey{Offset: raw.TypeName.Pos.Offset, Kind: analyze.CompositeRecordArgument}
	captured, found := b.expected.Composites[key]
	if !found {
		return annotation, nil
	}
	if captured.Kind != analyze.CompositeRecordArgument || captured.Base.Type == nil || !captured.Base.Type.BaseType().IsComposite() {
		return annotation, fmt.Errorf("stuntgres: incompatible retained composite argument")
	}
	current, _ := view.LookupType(captured.Base.Type.ID())
	usage := captured.Base
	usage.Type = current
	if _, valid, err := b.retainedComposite(key, usage); err != nil || !valid {
		return annotation, err
	}
	descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.ID())})
	if !ok || descriptor.SQLType != current {
		return annotation, fmt.Errorf("stuntgres: incompatible retained composite argument")
	}
	namespace := view.Get(descriptor.ObjectNamespace)
	if namespace == nil {
		return annotation, fmt.Errorf("stuntgres: missing retained composite namespace")
	}
	annotation.Name, annotation.Quoted = descriptor.Name(), true
	annotation.Schema = &parser.Identifier{Text: namespace.Name(), Pos: raw.TypeName.Pos}
	return annotation, nil
}
