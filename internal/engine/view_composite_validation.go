package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (v *viewDefinition) validateCompositeSource(captured compositeOccurrence) error {
	source := captured.Source
	if source.Namespace == 0 {
		if source.Qualifier != 0 || source.Frame != 0 || captured.Kind == analyze.CompositeWholeRow {
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

func validateCompositeOccurrence(captured compositeOccurrence, catalog catalog.Read) error {
	if captured.Kind != analyze.CompositeSelection && captured.Kind != analyze.CompositeExpansion && captured.Kind != analyze.CompositeDotBuiltin && captured.Kind != analyze.CompositeWholeRow && captured.Kind != analyze.CompositeSourceExpansion && captured.Kind != analyze.CompositeRecordArgument || captured.Kind == analyze.CompositeSelection && len(captured.Attributes) != 1 || captured.Kind == analyze.CompositeRecordArgument && len(captured.Attributes) != 0 || captured.Kind == analyze.CompositeWholeRow && (len(captured.Attributes) != 0 || captured.Source.Namespace == 0) || captured.Kind == analyze.CompositeDotBuiltin && (len(captured.Attributes) != 0 || captured.Function != functions.CountNulls && captured.Function != functions.CountNonnulls) || captured.Kind != analyze.CompositeDotBuiltin && captured.Function != 0 || !validExpressionUsage(catalog, captured.Base) || captured.Kind != analyze.CompositeDotBuiltin && !captured.Base.Type.BaseType().IsComposite() {
		return fmt.Errorf("stuntgres: invalid frozen composite authority")
	}
	if captured.Kind == analyze.CompositeSourceExpansion && len(captured.Names) != len(captured.Attributes) || captured.Kind != analyze.CompositeSourceExpansion && len(captured.Names) != 0 {
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
