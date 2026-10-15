package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type compositeOccurrenceKind uint8

const (
	compositeSelection compositeOccurrenceKind = iota + 1
	compositeExpansion
	compositeDotBuiltin
	compositeWholeRow
	compositeSourceExpansion
	compositeRecordArgument
)

type wholeRowSource struct {
	namespace, qualifier int
	frame                uint32
}
type compositeOccurrence struct {
	kind       compositeOccurrenceKind
	base       sqltype.Usage
	attributes []int32
	names      []string // Original exposed labels for a selected source star.
	function   functions.NullCountKind
	source     wholeRowSource
}
type preparedCompositeKey struct {
	offset     int
	kind       compositeOccurrenceKind
	projection int // Star offset; one source can occur in several target stars.
}

func (b *expressionBinder) retainedComposite(key preparedCompositeKey, base sqltype.Usage) (compositeOccurrence, bool, error) {
	if b.expected == nil {
		return compositeOccurrence{}, false, nil
	}
	catalog := b.bindingCatalog()
	if b.expected.catalogOwner != catalog.owner || b.expected.catalogEpoch != catalog.Epoch() {
		return compositeOccurrence{}, false, nil
	}
	captured, ok := b.expected.composites[key]
	if !ok {
		return captured, false, nil
	}
	if (captured.kind != key.kind && !(key.kind == compositeSelection && captured.kind == compositeDotBuiltin)) || captured.kind == compositeSelection && len(captured.attributes) != 1 || captured.kind == compositeDotBuiltin && (len(captured.attributes) != 0 || captured.function == 0) || base.Type == nil || captured.base.Type == nil || base.Type.ID() != captured.base.Type.ID() || captured.kind != compositeDotBuiltin && !base.Type.BaseType().IsComposite() {
		return compositeOccurrence{}, false, fmt.Errorf("stuntgres: incompatible retained composite occurrence")
	}
	return captured, true, nil
}
func (b *expressionBinder) captureComposite(key preparedCompositeKey, base sqltype.Usage, attributes []int32) {
	if b.composites == nil {
		b.composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	b.composites[key] = compositeOccurrence{kind: key.kind, base: base, attributes: attributes}
}
func fieldCompositeKey(raw parser.FieldExpr) preparedCompositeKey {
	offset := raw.Field.Pos.Offset
	kind := compositeSelection
	if raw.Expansion {
		kind = compositeExpansion
		offset = raw.OpenPos.Offset
		if raw.OpenPos.Character == 0 {
			offset = raw.Pos.Offset
		}
	}
	return preparedCompositeKey{offset: offset, kind: kind}
}

func (captured compositeOccurrence) validate(catalog CatalogRead) error {
	if captured.kind != compositeSelection && captured.kind != compositeExpansion && captured.kind != compositeDotBuiltin && captured.kind != compositeWholeRow && captured.kind != compositeSourceExpansion && captured.kind != compositeRecordArgument || captured.kind == compositeSelection && len(captured.attributes) != 1 || captured.kind == compositeRecordArgument && len(captured.attributes) != 0 || captured.kind == compositeWholeRow && (len(captured.attributes) != 0 || captured.source.namespace == 0) || captured.kind == compositeDotBuiltin && (len(captured.attributes) != 0 || captured.function != functions.CountNulls && captured.function != functions.CountNonnulls) || captured.kind != compositeDotBuiltin && captured.function != 0 || !validExpressionUsage(catalog, captured.base) || captured.kind != compositeDotBuiltin && !captured.base.Type.BaseType().IsComposite() {
		return fmt.Errorf("stuntgres: invalid frozen composite authority")
	}
	if captured.kind == compositeSourceExpansion && len(captured.names) != len(captured.attributes) || captured.kind != compositeSourceExpansion && len(captured.names) != 0 {
		return fmt.Errorf("stuntgres: invalid frozen source labels")
	}
	for i, attribute := range captured.attributes {
		if _, ok := captured.base.Type.BaseType().CompositeAttributeIndex(attribute); !ok {
			return fmt.Errorf("stuntgres: retired frozen composite attribute")
		}
		for _, previous := range captured.attributes[:i] {
			if previous == attribute {
				return fmt.Errorf("stuntgres: repeated frozen composite attribute")
			}
		}
	}
	return nil
}
func (b *expressionBinder) captureViewComposite(path viewLiteralPath, kind compositeOccurrenceKind, base BoundExpr, attributes []int32) {
	if b.viewComposites == nil {
		b.viewComposites = make(map[viewLiteralPath]compositeOccurrence)
	}
	source := wholeRowSource{}
	if row, ok := base.(rowExpression); ok {
		source = row.wholeSource
	}
	b.viewComposites[path] = compositeOccurrence{kind: kind, base: base.ResultUsage(), attributes: attributes, source: source}
}
func (b *expressionBinder) captureCompositeExpansion(raw parser.Expr, expansion compositeExpansionBinding, path viewLiteralPath, ordinal *int) error {
	path.node = *ordinal
	(*ordinal)++
	b.captureViewComposite(path, compositeExpansion, expansion.base, expansion.attributes)
	switch expression := raw.(type) {
	case parser.FieldExpr:
		return b.captureViewLiteralTree(expression.Base, expansion.base, path, ordinal)
	case parser.StarExpr:
		return nil
	default:
		return fmt.Errorf("stuntgres: invalid raw composite expansion")
	}
}

func (b *expressionBinder) bindDotBuiltin(raw parser.FieldExpr, key preparedCompositeKey) (BoundExpr, error) {
	node, err := b.bindFunction(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{raw.Field}}, Args: []parser.Expr{raw.Base}, Pos: raw.Pos})
	if err != nil {
		return nil, err
	}
	call, ok := node.(functions.NullCount[BoundExpr, *EvalContext, ExprProperties])
	if !ok || len(call.Args) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid dot builtin resolution")
	}
	call.DotBuiltin = true
	if b.composites == nil {
		b.composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	b.composites[key] = compositeOccurrence{kind: compositeDotBuiltin, base: call.Args[0].ResultUsage(), function: call.Kind}
	return call, nil
}

func (b *expressionBinder) frozenWholeRowSource(qualifier queryQualifier, frame uint32) (wholeRowSource, error) {
	if !b.noParameters && !b.materializedDefinition {
		return wholeRowSource{}, nil
	}
	var source wholeRowSource
	for index, namespace := range b.viewNamespaces {
		if namespace.frame != frame {
			continue
		}
		for position, frozen := range namespace.qualifiers {
			if frozen.name != qualifier.name || len(frozen.columns) != len(qualifier.columns) {
				continue
			}
			matches := true
			for i, column := range qualifier.columns {
				captured := frozen.columns[i]
				if captured.name != column.descriptor.Name || captured.leaf != column.leaf {
					matches = false
					break
				}
				if column.leaf >= 0 && captured.attribute != b.querySources[column.leaf].relation.attributes[column.physical] {
					matches = false
					break
				}
			}
			if matches {
				source = wholeRowSource{namespace: index + 1, qualifier: position, frame: frame}
			}
		}
	}
	if source.namespace == 0 {
		return source, fmt.Errorf("stuntgres: missing frozen whole-row namespace")
	}
	return source, nil
}

func (v *viewDefinition) validateCompositeSource(captured compositeOccurrence) error {
	source := captured.source
	if source.namespace == 0 {
		if source.qualifier != 0 || source.frame != 0 || captured.kind == compositeWholeRow {
			return fmt.Errorf("stuntgres: missing frozen whole-row source")
		}
		return nil
	}
	if source.namespace < 1 || source.namespace > len(v.namespaces) {
		return fmt.Errorf("stuntgres: invalid frozen whole-row namespace")
	}
	namespace := v.namespaces[source.namespace-1]
	if source.frame != namespace.frame || v.frames[source.frame] == nil || source.qualifier < 0 || source.qualifier >= len(namespace.qualifiers) {
		return fmt.Errorf("stuntgres: invalid frozen whole-row source frame")
	}
	root := v.namespaceRoot(namespace)
	if root == nil {
		return fmt.Errorf("stuntgres: missing frozen whole-row source node")
	}
	columns := namespace.qualifiers[source.qualifier].columns
	shape := captured.base.Type.BaseType()
	if !shape.IsComposite() || len(columns) != shape.CompositeLen() {
		return fmt.Errorf("stuntgres: incompatible frozen whole-row shape")
	}
	for i, column := range columns {
		member := shape.CompositeMember(i)
		if column.slot < 0 || column.slot >= len(root.Schema()) || !sameRecordUsage(member.Usage, root.Schema()[column.slot].Usage) {
			return fmt.Errorf("stuntgres: incompatible frozen whole-row member")
		}
		if shape.ID() != 2249 && column.leaf >= 0 && member.Attribute != column.attribute {
			return fmt.Errorf("stuntgres: incompatible frozen whole-row attribute")
		}
	}
	return nil
}
