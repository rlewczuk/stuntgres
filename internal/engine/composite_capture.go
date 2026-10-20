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

type compositeOccurrenceKind uint8

const (
	compositeSelection compositeOccurrenceKind = iota + 1
	compositeExpansion
	compositeDotBuiltin
	compositeWholeRow
	compositeSourceExpansion
	compositeRecordArgument
)

type compositeOccurrence struct {
	kind       compositeOccurrenceKind
	base       sqltype.Usage
	attributes []int32
	names      []string // Original exposed labels for a selected source star.
	function   functions.NullCountKind
	source     plan.WholeRowSource
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
	if b.expected.catalogOwner != catalog.Owner() || b.expected.catalogEpoch != catalog.Epoch() {
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
func (p *preparedCaptures) captureComposite(key preparedCompositeKey, base sqltype.Usage, attributes []int32) {
	if p.composites == nil {
		p.composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	p.composites[key] = compositeOccurrence{kind: key.kind, base: base, attributes: attributes}
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

func validateCompositeOccurrence(captured compositeOccurrence, catalog catalog.Read) error {
	if captured.kind != compositeSelection && captured.kind != compositeExpansion && captured.kind != compositeDotBuiltin && captured.kind != compositeWholeRow && captured.kind != compositeSourceExpansion && captured.kind != compositeRecordArgument || captured.kind == compositeSelection && len(captured.attributes) != 1 || captured.kind == compositeRecordArgument && len(captured.attributes) != 0 || captured.kind == compositeWholeRow && (len(captured.attributes) != 0 || captured.source.Namespace == 0) || captured.kind == compositeDotBuiltin && (len(captured.attributes) != 0 || captured.function != functions.CountNulls && captured.function != functions.CountNonnulls) || captured.kind != compositeDotBuiltin && captured.function != 0 || !validExpressionUsage(catalog, captured.base) || captured.kind != compositeDotBuiltin && !captured.base.Type.BaseType().IsComposite() {
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
func (c *viewCapture) captureViewComposite(path viewLiteralPath, kind compositeOccurrenceKind, base expr.Expr, attributes []int32) {
	if c.composites == nil {
		c.composites = make(map[viewLiteralPath]compositeOccurrence)
	}
	source := plan.WholeRowSource{}
	if row, ok := base.(plan.RowExpr); ok {
		source = row.WholeSource
	}
	c.composites[path] = compositeOccurrence{kind: kind, base: base.ResultUsage(), attributes: attributes, source: source}
}
func (b *expressionBinder) captureCompositeExpansion(raw parser.Expr, expansion plan.CompositeExpansionBinding, path viewLiteralPath, ordinal *int) error {
	path.node = *ordinal
	(*ordinal)++
	b.capture.captureViewComposite(path, compositeExpansion, expansion.Base, expansion.Attributes)
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
	if b.prepared.composites == nil {
		b.prepared.composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	b.prepared.composites[key] = compositeOccurrence{kind: compositeDotBuiltin, base: call.Args[0].ResultUsage(), function: call.Kind}
	return call, nil
}

func (b *expressionBinder) frozenWholeRowSource(qualifier queryQualifier, frame uint32) (plan.WholeRowSource, error) {
	if !b.params.noParameters && !b.consumer.materializedDefinition {
		return plan.WholeRowSource{}, nil
	}
	var source plan.WholeRowSource
	for index, namespace := range b.capture.namespaces {
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
				if column.leaf >= 0 && captured.attribute != b.query.sources[column.leaf].Relation.Attributes[column.physical] {
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
	source := captured.source
	if source.Namespace == 0 {
		if source.Qualifier != 0 || source.Frame != 0 || captured.kind == compositeWholeRow {
			return fmt.Errorf("stuntgres: missing frozen whole-row source")
		}
		return nil
	}
	if source.Namespace < 1 || source.Namespace > len(v.namespaces) {
		return fmt.Errorf("stuntgres: invalid frozen whole-row namespace")
	}
	namespace := v.namespaces[source.Namespace-1]
	if source.Frame != namespace.frame || v.frames[source.Frame] == nil || source.Qualifier < 0 || source.Qualifier >= len(namespace.qualifiers) {
		return fmt.Errorf("stuntgres: invalid frozen whole-row source frame")
	}
	root := v.namespaceRoot(namespace)
	if root == nil {
		return fmt.Errorf("stuntgres: missing frozen whole-row source node")
	}
	columns := namespace.qualifiers[source.Qualifier].columns
	shape := captured.base.Type.BaseType()
	if !shape.IsComposite() || len(columns) != shape.CompositeLen() {
		return fmt.Errorf("stuntgres: incompatible frozen whole-row shape")
	}
	for i, column := range columns {
		member := shape.CompositeMember(i)
		if column.slot < 0 || column.slot >= len(root.Schema()) || !sqltype.SameRecordUsage(member.Usage, root.Schema()[column.slot].Usage) {
			return fmt.Errorf("stuntgres: incompatible frozen whole-row member")
		}
		if shape.ID() != 2249 && column.leaf >= 0 && member.Attribute != column.attribute {
			return fmt.Errorf("stuntgres: incompatible frozen whole-row attribute")
		}
	}
	return nil
}
