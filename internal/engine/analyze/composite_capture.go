package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) retainedComposite(key PreparedTypeKey, base sqltype.Usage) (TypeOccurrence, bool, error) {
	if !b.Expected.Held {
		return TypeOccurrence{}, false, nil
	}
	catalog := b.BindingCatalog()
	if b.Expected.CatalogOwner != catalog.Owner() || b.Expected.CatalogEpoch != catalog.Epoch() {
		return TypeOccurrence{}, false, nil
	}
	captured, ok := b.Expected.Types[key]
	if !ok {
		return captured, false, nil
	}
	if (captured.Kind != key.Kind && !(key.Kind == CompositeSelection && captured.Kind == CompositeDotBuiltin)) || captured.Kind == CompositeSelection && len(captured.Attributes) != 1 || captured.Kind == CompositeDotBuiltin && (len(captured.Attributes) != 0 || captured.Function == 0) || base.Type == nil || captured.Base.Type == nil || base.Type.ID() != captured.Base.Type.ID() || captured.Kind != CompositeDotBuiltin && !base.Type.BaseType().IsComposite() {
		return TypeOccurrence{}, false, fmt.Errorf("stuntgres: incompatible retained composite occurrence")
	}
	return captured, true, nil
}
func fieldCompositeKey(raw parser.FieldExpr) PreparedTypeKey {
	offset := raw.Field.Pos.Offset
	kind := CompositeSelection
	if raw.Expansion {
		kind = CompositeExpansion
		offset = raw.OpenPos.Offset
		if raw.OpenPos.Character == 0 {
			offset = raw.Pos.Offset
		}
	}
	return PreparedTypeKey{Offset: offset, Kind: kind}
}

func (b *Binder) captureCompositeExpansion(raw parser.Expr, expansion plan.CompositeExpansionBinding, path ViewLiteralPath, ordinal *int) error {
	path.Node = *ordinal
	(*ordinal)++
	b.Capture.CaptureViewComposite(path, CompositeExpansion, expansion.Base, expansion.Attributes)
	switch expression := raw.(type) {
	case parser.FieldExpr:
		return b.CaptureViewLiteralTree(expression.Base, expansion.Base, path, ordinal)
	case parser.StarExpr:
		return nil
	default:
		return fmt.Errorf("stuntgres: invalid raw composite expansion")
	}
}

func (b *Binder) bindDotBuiltin(raw parser.FieldExpr, key PreparedTypeKey) (expr.Expr, error) {
	node, err := b.BindFunction(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{raw.Field}}, Args: []parser.Expr{raw.Base}, Pos: raw.Pos})
	if err != nil {
		return nil, err
	}
	call, ok := node.(functions.NullCount)
	if !ok || len(call.Args) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid dot builtin resolution")
	}
	call.DotBuiltin = true
	if b.Prepared.Types == nil {
		b.Prepared.Types = make(map[PreparedTypeKey]TypeOccurrence)
	}
	b.Prepared.Types[key] = TypeOccurrence{Kind: CompositeDotBuiltin, Base: call.Args[0].ResultUsage(), Function: call.Kind}
	return call, nil
}

func (b *Binder) frozenWholeRowSource(qualifier QueryQualifier, frame uint32) (plan.WholeRowSource, error) {
	if !b.Params.NoParameters && !b.Consumer.frozenDefinition {
		return plan.WholeRowSource{}, nil
	}
	var source plan.WholeRowSource
	for index, namespace := range b.Capture.Namespaces {
		if namespace.Frame != frame {
			continue
		}
		for position, frozen := range namespace.Qualifiers {
			if frozen.Name != qualifier.Name || len(frozen.Columns) != len(qualifier.Columns) {
				continue
			}
			matches := true
			for i, column := range qualifier.Columns {
				captured := frozen.Columns[i]
				if captured.Name != column.Descriptor.Name || captured.Leaf != column.Leaf {
					matches = false
					break
				}
				if column.Leaf >= 0 && captured.Attribute != b.Query.Sources[column.Leaf].Relation.Attributes[column.Physical] {
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
