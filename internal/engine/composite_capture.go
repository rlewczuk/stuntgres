package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) retainedComposite(key preparedCompositeKey, base sqltype.Usage) (compositeOccurrence, bool, error) {
	if b.expected == nil {
		return compositeOccurrence{}, false, nil
	}
	catalog := b.bindingCatalog()
	if b.expected.catalogOwner != catalog.Owner() || b.expected.catalogEpoch != catalog.Epoch() {
		return compositeOccurrence{}, false, nil
	}
	captured, ok := b.expected.composites[key]
	if !ok {
		return captured, false, nil
	}
	if (captured.Kind != key.Kind && !(key.Kind == CompositeSelection && captured.Kind == CompositeDotBuiltin)) || captured.Kind == CompositeSelection && len(captured.Attributes) != 1 || captured.Kind == CompositeDotBuiltin && (len(captured.Attributes) != 0 || captured.Function == 0) || base.Type == nil || captured.Base.Type == nil || base.Type.ID() != captured.Base.Type.ID() || captured.Kind != CompositeDotBuiltin && !base.Type.BaseType().IsComposite() {
		return compositeOccurrence{}, false, fmt.Errorf("stuntgres: incompatible retained composite occurrence")
	}
	return captured, true, nil
}
func fieldCompositeKey(raw parser.FieldExpr) preparedCompositeKey {
	offset := raw.Field.Pos.Offset
	kind := CompositeSelection
	if raw.Expansion {
		kind = CompositeExpansion
		offset = raw.OpenPos.Offset
		if raw.OpenPos.Character == 0 {
			offset = raw.Pos.Offset
		}
	}
	return preparedCompositeKey{Offset: offset, Kind: kind}
}

func validateCompositeOccurrence(captured compositeOccurrence, catalog catalog.Read) error {
	if captured.Kind != CompositeSelection && captured.Kind != CompositeExpansion && captured.Kind != CompositeDotBuiltin && captured.Kind != CompositeWholeRow && captured.Kind != CompositeSourceExpansion && captured.Kind != CompositeRecordArgument || captured.Kind == CompositeSelection && len(captured.Attributes) != 1 || captured.Kind == CompositeRecordArgument && len(captured.Attributes) != 0 || captured.Kind == CompositeWholeRow && (len(captured.Attributes) != 0 || captured.Source.Namespace == 0) || captured.Kind == CompositeDotBuiltin && (len(captured.Attributes) != 0 || captured.Function != functions.CountNulls && captured.Function != functions.CountNonnulls) || captured.Kind != CompositeDotBuiltin && captured.Function != 0 || !validExpressionUsage(catalog, captured.Base) || captured.Kind != CompositeDotBuiltin && !captured.Base.Type.BaseType().IsComposite() {
		return fmt.Errorf("stuntgres: invalid frozen composite authority")
	}
	if captured.Kind == CompositeSourceExpansion && len(captured.Names) != len(captured.Attributes) || captured.Kind != CompositeSourceExpansion && len(captured.Names) != 0 {
		return fmt.Errorf("stuntgres: invalid frozen source labels")
	}
	for i, attribute := range captured.Attributes {
		if _, ok := captured.Base.Type.BaseType().CompositeAttributeIndex(attribute); !ok {
			return fmt.Errorf("stuntgres: retired frozen composite attribute")
		}
		for _, previous := range captured.Attributes[:i] {
			if previous == attribute {
				return fmt.Errorf("stuntgres: repeated frozen composite attribute")
			}
		}
	}
	return nil
}
func (b *expressionBinder) captureCompositeExpansion(raw parser.Expr, expansion plan.CompositeExpansionBinding, path viewLiteralPath, ordinal *int) error {
	path.Node = *ordinal
	(*ordinal)++
	b.capture.CaptureViewComposite(path, CompositeExpansion, expansion.Base, expansion.Attributes)
	switch expression := raw.(type) {
	case parser.FieldExpr:
		return b.captureViewLiteralTree(expression.Base, expansion.Base, path, ordinal)
	case parser.StarExpr:
		return nil
	default:
		return fmt.Errorf("stuntgres: invalid raw composite expansion")
	}
}

func (b *expressionBinder) bindDotBuiltin(raw parser.FieldExpr, key preparedCompositeKey) (expr.Expr, error) {
	node, err := b.bindFunction(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{raw.Field}}, Args: []parser.Expr{raw.Base}, Pos: raw.Pos})
	if err != nil {
		return nil, err
	}
	call, ok := node.(functions.NullCount)
	if !ok || len(call.Args) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid dot builtin resolution")
	}
	call.DotBuiltin = true
	if b.prepared.Composites == nil {
		b.prepared.Composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	b.prepared.Composites[key] = compositeOccurrence{Kind: CompositeDotBuiltin, Base: call.Args[0].ResultUsage(), Function: call.Kind}
	return call, nil
}

func (b *expressionBinder) frozenWholeRowSource(qualifier queryQualifier, frame uint32) (plan.WholeRowSource, error) {
	if !b.params.noParameters && !b.consumer.materializedDefinition {
		return plan.WholeRowSource{}, nil
	}
	var source plan.WholeRowSource
	for index, namespace := range b.capture.Namespaces {
		if namespace.Frame != frame {
			continue
		}
		for position, frozen := range namespace.Qualifiers {
			if frozen.Name != qualifier.name || len(frozen.Columns) != len(qualifier.columns) {
				continue
			}
			matches := true
			for i, column := range qualifier.columns {
				captured := frozen.Columns[i]
				if captured.Name != column.descriptor.Name || captured.Leaf != column.leaf {
					matches = false
					break
				}
				if column.leaf >= 0 && captured.Attribute != b.query.sources[column.leaf].Relation.Attributes[column.physical] {
					matches = false
					break
				}
			}
			if matches {
				source = plan.WholeRowSource{Namespace: index + 1, Qualifier: position, Frame: frame}
			}
		}
	}
	if source.Namespace == 0 {
		return source, fmt.Errorf("stuntgres: missing frozen whole-row namespace")
	}
	return source, nil
}

func (v *viewDefinition) validateCompositeSource(captured compositeOccurrence) error {
	source := captured.Source
	if source.Namespace == 0 {
		if source.Qualifier != 0 || source.Frame != 0 || captured.Kind == CompositeWholeRow {
			return fmt.Errorf("stuntgres: missing frozen whole-row source")
		}
		return nil
	}
	if source.Namespace < 1 || source.Namespace > len(v.namespaces) {
		return fmt.Errorf("stuntgres: invalid frozen whole-row namespace")
	}
	namespace := v.namespaces[source.Namespace-1]
	if source.Frame != namespace.Frame || v.frames[source.Frame] == nil || source.Qualifier < 0 || source.Qualifier >= len(namespace.Qualifiers) {
		return fmt.Errorf("stuntgres: invalid frozen whole-row source frame")
	}
	root := v.namespaceRoot(namespace)
	if root == nil {
		return fmt.Errorf("stuntgres: missing frozen whole-row source node")
	}
	columns := namespace.Qualifiers[source.Qualifier].Columns
	shape := captured.Base.Type.BaseType()
	if !shape.IsComposite() || len(columns) != shape.CompositeLen() {
		return fmt.Errorf("stuntgres: incompatible frozen whole-row shape")
	}
	for i, column := range columns {
		member := shape.CompositeMember(i)
		if column.Slot < 0 || column.Slot >= len(root.Schema()) || !sqltype.SameRecordUsage(member.Usage, root.Schema()[column.Slot].Usage) {
			return fmt.Errorf("stuntgres: incompatible frozen whole-row member")
		}
		if shape.ID() != 2249 && column.Leaf >= 0 && member.Attribute != column.Attribute {
			return fmt.Errorf("stuntgres: incompatible frozen whole-row attribute")
		}
	}
	return nil
}
