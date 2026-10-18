package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func (v *viewRenderer) wholeRowSQL(captured compositeOccurrence) (string, error) {
	rendered, ok := v.wholeRows[captured.source]
	if !ok || rendered.sql == "" {
		return "", fmt.Errorf("stuntgres: missing rendered whole-row occurrence")
	}
	return rendered.sql, nil
}

func (v *viewRenderer) frozenCompositeFields(ctx context.Context, catalog catalog.Read, captured compositeOccurrence, base string) ([]string, error) {
	shape := captured.base.Type.BaseType()
	fields := make([]string, len(captured.attributes))
	var exposed []string
	var scalarRoot LogicalNode
	var scalarColumns []viewNamespaceColumn
	if captured.source.namespace != 0 {
		rendered, ok := v.wholeRows[captured.source]
		if !ok || rendered.sql == "" {
			return nil, fmt.Errorf("stuntgres: missing rendered whole-row field namespace")
		}
		exposed = rendered.names
		namespace := v.namespaces[captured.source.namespace-1]
		root := v.namespaceRoot(namespace)
		if function, ok := root.(*functionFromNode); ok && len(function.calls) == 1 && !function.ordinality && !function.calls[0].ResultUsage().Type.IsComposite() && captured.kind == compositeExpansion {
			// A namespace list is not a composite field selection on the
			// function's scalar alias value. Replay its analyzed column slots.
			scalarRoot = root
			scalarColumns = namespace.qualifiers[captured.source.qualifier].columns
		}
	}
	for i, attribute := range captured.attributes {
		index, ok := shape.CompositeAttributeIndex(attribute)
		if !ok {
			return nil, fmt.Errorf("stuntgres: retired rendered composite member")
		}
		if scalarRoot != nil {
			if index >= len(scalarColumns) {
				return nil, fmt.Errorf("stuntgres: missing rendered scalar namespace member")
			}
			text, err := v.slotSQL(ctx, catalog, scalarRoot, scalarColumns[index].slot)
			if err != nil {
				return nil, err
			}
			fields[i] = text
			continue
		}
		name := shape.CompositeMember(index).Name
		if exposed != nil {
			if index >= len(exposed) {
				return nil, fmt.Errorf("stuntgres: missing rendered whole-row member")
			}
			name = exposed[index]
		}
		fields[i] = "(" + base + ")." + quoteIdentifier(name)
	}
	return fields, nil
}

func (v *viewRenderer) compositeExpansionSQL(ctx context.Context, catalog catalog.Read, raw parser.FieldExpr, path viewLiteralPath) ([]string, error) {
	path.query = v.frame
	path.node = 0
	captured, ok := v.composites[path]
	if !ok || captured.kind != compositeExpansion {
		return nil, fmt.Errorf("stuntgres: missing frozen composite expansion")
	}
	ordinal := 1
	base, err := v.expressionSQLAt(ctx, catalog, raw.Base, path, &ordinal, pgRenderParent{})
	if err != nil {
		return nil, err
	}
	return v.frozenCompositeFields(ctx, catalog, captured, base)
}

func frozenDotBuiltinSQL(captured compositeOccurrence, base string) (string, error) {
	name := ""
	switch captured.function {
	case functions.CountNulls:
		name = "num_nulls"
	case functions.CountNonnulls:
		name = "num_nonnulls"
	default:
		return "", fmt.Errorf("stuntgres: invalid rendered dot provider")
	}
	return "pg_catalog." + name + "(" + base + ")", nil
}
