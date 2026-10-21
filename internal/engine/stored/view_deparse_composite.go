package stored

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func (v *QueryRenderer) wholeRowSQL(captured analyze.CompositeOccurrence) (string, error) {
	rendered, ok := v.wholeRows[captured.Source]
	if !ok || rendered.sql == "" {
		return "", fmt.Errorf("stuntgres: missing rendered whole-row occurrence")
	}
	return rendered.sql, nil
}

func (v *QueryRenderer) frozenCompositeFields(ctx context.Context, catalogView catalog.Read, captured analyze.CompositeOccurrence, base string) ([]string, error) {
	shape := captured.Base.Type.BaseType()
	fields := make([]string, len(captured.Attributes))
	var exposed []string
	var scalarRoot plan.Node
	var scalarColumns []analyze.ViewNamespaceColumn
	if captured.Source.Namespace != 0 {
		rendered, ok := v.wholeRows[captured.Source]
		if !ok || rendered.sql == "" {
			return nil, fmt.Errorf("stuntgres: missing rendered whole-row field namespace")
		}
		exposed = rendered.names
		namespace := v.namespaces[captured.Source.Namespace-1]
		root := v.namespaceRoot(namespace)
		if function, ok := root.(*plan.FunctionFromNode); ok && len(function.Calls) == 1 && !function.Ordinality && !function.Calls[0].ResultUsage().Type.IsComposite() && captured.Kind == analyze.CompositeExpansion {
			// A namespace list is not a composite field selection on the
			// function's scalar alias value. Replay its analyzed column slots.
			scalarRoot = root
			scalarColumns = namespace.Qualifiers[captured.Source.Qualifier].Columns
		}
	}
	for i, attribute := range captured.Attributes {
		index, ok := shape.CompositeAttributeIndex(attribute)
		if !ok {
			return nil, fmt.Errorf("stuntgres: retired rendered composite member")
		}
		if scalarRoot != nil {
			if index >= len(scalarColumns) {
				return nil, fmt.Errorf("stuntgres: missing rendered scalar namespace member")
			}
			text, err := v.SlotSQL(ctx, catalogView, scalarRoot, scalarColumns[index].Slot)
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
		fields[i] = "(" + base + ")." + catalog.QuoteIdentifier(name)
	}
	return fields, nil
}

func (v *QueryRenderer) compositeExpansionSQL(ctx context.Context, catalog catalog.Read, raw parser.FieldExpr, path analyze.ViewLiteralPath) ([]string, error) {
	path.Query = v.Frame
	path.Node = 0
	captured, ok := v.composites[path]
	if !ok || captured.Kind != analyze.CompositeExpansion {
		return nil, fmt.Errorf("stuntgres: missing frozen composite expansion")
	}
	ordinal := 1
	base, err := v.ExpressionSQLAt(ctx, catalog, raw.Base, path, &ordinal, DeparseParent{})
	if err != nil {
		return nil, err
	}
	return v.frozenCompositeFields(ctx, catalog, captured, base)
}

func frozenDotBuiltinSQL(captured analyze.CompositeOccurrence, base string) (string, error) {
	name := ""
	switch captured.Function {
	case functions.CountNulls:
		name = "num_nulls"
	case functions.CountNonnulls:
		name = "num_nonnulls"
	default:
		return "", fmt.Errorf("stuntgres: invalid rendered dot provider")
	}
	return "pg_catalog." + name + "(" + base + ")", nil
}
