package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// QueryTableColumns derives the columns of CREATE TABLE AS s from the final
// projection of b, in column order: names, duplicates, system names,
// pseudo-types, indeterminate collations and USAGE on user-defined types
// (checked with env). bases are the type definitions the stage must observe.
func QueryTableColumns(view access.View, env access.Environment, s *parser.CreateQueryTable, b analyze.Binding) (columns []catalog.Column, names map[string]int, bases []catalog.Descriptor, err error) {
	projection, ok := b.Query.Root.(*plan.ProjectNode)
	if !ok {
		return nil, nil, nil, errors.New("stuntgres: query table lacks final projection")
	}
	schema := projection.Schema()
	if len(s.Columns) > len(schema) {
		return nil, nil, nil, sqlError("42601", "too many column names were specified")
	}
	if len(schema) > 1600 {
		return nil, nil, nil, sqlError("54011", "tables can have at most 1600 columns")
	}
	columns = make([]catalog.Column, len(schema))
	names = make(map[string]int, len(schema))
	for i, column := range schema {
		column.Nullable, column.NullabilityKnown = true, true
		column.OriginTable, column.OriginAttribute = 0, 0
		column.DeclaredDimensions = 0
		if i < len(s.Columns) {
			column.Name = s.Columns[i].Text
		}
		if _, exists := names[column.Name]; exists {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		if catalog.IsSystemColumn(column.Name) {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, column.Name))
		}
		names[column.Name] = i
		typ := column.Usage.Type
		if sqltype.OccurrenceRecordArray(typ) || typ != nil && typ.ID() == 2249 && typ.IsComposite() {
			typeName := "record"
			if sqltype.OccurrenceRecordArray(typ) {
				typeName = "record[]"
			}
			return nil, nil, nil, sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type %s`, column.Name, typeName))
		}
		if typ == nil || !typ.Concrete() || typ.ID() == sqltype.Void {
			typeName := "unknown"
			if typ != nil {
				typeName = typ.Name()
			}
			return nil, nil, nil, sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type %s`, column.Name, typeName))
		}
		if err := table.ValidateQueryTableOutputType(typ); err != nil {
			return nil, nil, nil, err
		}
		if projection.Expressions[i].CollationIndeterminate() {
			return nil, nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: fmt.Sprintf(`no collation was derived for column "%s" with collatable type %s`, column.Name, typ.Name()), Hint: "Use the COLLATE clause to set the collation explicitly."}
		}
		if !column.Usage.Valid() {
			return nil, nil, nil, errors.New("stuntgres: invalid query table usage")
		}
		dependencyType := typ
		for dependencyType.IsArray() {
			dependencyType = dependencyType.Element()
		}
		if dependencyType.IsUserDefined() {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(dependencyType.ID())}
			if err := view.CanUse(env, view.Ref(address)); err != nil {
				return nil, nil, nil, err
			}
			descriptor := view.Catalog.Get(address)
			if descriptor == nil {
				return nil, nil, nil, errors.New("stuntgres: query table type definition is missing")
			}
			bases = append(bases, descriptor)
		}
		columns[i] = column
	}
	return columns, names, bases, nil
}
