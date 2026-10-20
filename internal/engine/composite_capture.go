package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) retainedComposite(key preparedCompositeKey, base sqltype.Usage) (compositeOccurrence, bool, error) {
	if !b.expected.Held {
		return compositeOccurrence{}, false, nil
	}
	catalog := b.bindingCatalog()
	if b.expected.CatalogOwner != catalog.Owner() || b.expected.CatalogEpoch != catalog.Epoch() {
		return compositeOccurrence{}, false, nil
	}
	captured, ok := b.expected.Composites[key]
	if !ok {
		return captured, false, nil
	}
	if (captured.Kind != key.Kind && !(key.Kind == analyze.CompositeSelection && captured.Kind == analyze.CompositeDotBuiltin)) || captured.Kind == analyze.CompositeSelection && len(captured.Attributes) != 1 || captured.Kind == analyze.CompositeDotBuiltin && (len(captured.Attributes) != 0 || captured.Function == 0) || base.Type == nil || captured.Base.Type == nil || base.Type.ID() != captured.Base.Type.ID() || captured.Kind != analyze.CompositeDotBuiltin && !base.Type.BaseType().IsComposite() {
		return compositeOccurrence{}, false, fmt.Errorf("stuntgres: incompatible retained composite occurrence")
	}
	return captured, true, nil
}
func fieldCompositeKey(raw parser.FieldExpr) preparedCompositeKey {
	offset := raw.Field.Pos.Offset
	kind := analyze.CompositeSelection
	if raw.Expansion {
		kind = analyze.CompositeExpansion
		offset = raw.OpenPos.Offset
		if raw.OpenPos.Character == 0 {
			offset = raw.Pos.Offset
		}
	}
	return preparedCompositeKey{Offset: offset, Kind: kind}
}

func (b *expressionBinder) captureCompositeExpansion(raw parser.Expr, expansion plan.CompositeExpansionBinding, path viewLiteralPath, ordinal *int) error {
	path.Node = *ordinal
	(*ordinal)++
	b.capture.CaptureViewComposite(path, analyze.CompositeExpansion, expansion.Base, expansion.Attributes)
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
	b.prepared.Composites[key] = compositeOccurrence{Kind: analyze.CompositeDotBuiltin, Base: call.Args[0].ResultUsage(), Function: call.Kind}
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
