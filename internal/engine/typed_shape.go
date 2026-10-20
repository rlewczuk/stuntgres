package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func standaloneComposite(view catalog.Read, typ *catalog.Type, path []ids.ObjectAddress) (*catalog.Relation, error) {
	name := typ.SQLType.DiagnosticName()
	if typ.SQLType.IsUserDefined() {
		name = catalog.TypeDiagnosticNameAt(view, typ, path)
	}
	if !typ.SQLType.IsComposite() {
		return nil, sqlError("42809", "type "+name+" is not a composite type")
	}
	source, ok := view.Relation(typ.OwningRelation)
	if !ok || source.RowType != typ.SQLType.ID() {
		return nil, fmt.Errorf("stuntgres: missing composite type authority")
	}
	if source.RelationKind != catalog.CompositeRelation {
		d := sqlError("42809", "type "+name+" is the row type of another table").(*sqlstate.Error)
		d.Detail = "A typed table must use a stand-alone composite type created with CREATE TYPE."
		return nil, d
	}
	return source, nil
}

func typedShape(table, source *catalog.Relation) error {
	for i, member := range source.Columns {
		if i >= len(table.Columns) {
			return sqlError("42804", fmt.Sprintf(`table is missing column "%s"`, member.Name))
		}
		actual := table.Columns[i]
		if actual.Name != member.Name {
			return sqlError("42804", fmt.Sprintf(`table has column "%s" where type requires "%s"`, actual.Name, member.Name))
		}
		if actual.Usage != member.Usage {
			return sqlError("42804", fmt.Sprintf(`table "%s" has different type for column "%s"`, table.Name(), member.Name))
		}
	}
	if len(table.Columns) > len(source.Columns) {
		return sqlError("42804", fmt.Sprintf(`table has extra column "%s"`, table.Columns[len(source.Columns)].Name))
	}
	return nil
}

func validateTypedAttachment(view catalog.Read, table *catalog.Relation) error {
	if table.OfType == (ids.ObjectAddress{}) {
		return nil
	}
	if table.OfType.ClassID != ids.TypeClassID || table.OfType.SubID != 0 || table.RelationKind != catalog.TableRelation {
		return fmt.Errorf("stuntgres: invalid typed attachment")
	}
	typ, ok := view.Type(table.OfType)
	if !ok {
		return fmt.Errorf("stuntgres: missing typed source")
	}
	source, err := standaloneComposite(view, typ, nil)
	if err != nil {
		return err
	}
	if table.Inheritance != nil && len(table.Inheritance.Parents) != 0 {
		return sqlError("42809", "typed tables cannot inherit")
	}
	return typedShape(table, source)
}

func typedDirectActionsGuard(table *catalog.Relation, actions []parser.AlterTableAction) error {
	if table.OfType == (ids.ObjectAddress{}) {
		return nil
	}
	for _, action := range actions {
		switch action.Action {
		case parser.AddColumn:
			return sqlError("42809", "cannot add column to typed table")
		case parser.DropColumn:
			return sqlError("42809", "cannot drop column from typed table")
		case parser.AlterColumnType:
			if _, exists := table.Index[action.Column.Text]; !exists {
				return sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, table.Name()))
			}
			return expr.ErrorAt("42809", "cannot alter column type of typed table", action.Column.Pos)
		}
	}
	return nil
}
