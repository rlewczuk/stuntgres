package fdw

import (
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func CheckTableDeclaration(s *parser.CreateTable) error {
	for _, element := range s.Elements {
		if err := CheckConstraint(element.Unique, element.ForeignKey, element.Exclusion); err != nil {
			return err
		}
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if err := CheckConstraint(constraint.Unique, constraint.ForeignKey, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CheckConstraint preserves the declaration token separately from ordinary
// constraint binder positions.
func CheckConstraint(unique *parser.UniqueConstraint, foreign *parser.ForeignKeyConstraint, exclusion *parser.ExclusionConstraint) error {
	var kind string
	var pos parser.Position
	switch {
	case unique != nil:
		kind, pos = "unique", unique.Pos
		if unique.Primary {
			kind = "primary key"
		}
	case foreign != nil:
		kind, pos = "foreign key", foreign.Pos
		if foreign.NamedDeclarationPos.Character != 0 {
			pos = foreign.NamedDeclarationPos
		}
	case exclusion != nil:
		kind, pos = "exclusion", exclusion.Pos
		if exclusion.NamedDeclarationPos.Character != 0 {
			pos = exclusion.NamedDeclarationPos
		}
	default:
		return nil
	}
	return expr.ErrorAt("0A000", kind+" constraints are not supported on foreign tables", pos)
}

// PostgreSQL accepts these LIKE options for foreign destinations but does not
// copy their physical/identity properties. Apply that policy before the common
// binder or stage can create an index, sequence or physical column metadata.
func TableSyntax(table *parser.CreateTable) *parser.CreateTable {
	next := table
	for i, element := range table.Elements {
		if element.Like == nil {
			continue
		}
		if next == table {
			copy := *table
			copy.Elements = slices.Clone(table.Elements)
			next = &copy
		}
		like := *element.Like
		options := make([]parser.LikeOption, len(like.Options)+4)
		copy(options, like.Options)
		for j, name := range [...]string{"indexes", "identity", "storage", "compression"} {
			options[len(like.Options)+j] = parser.LikeOption{Name: name}
		}
		like.Options = options
		next.Elements[i].Like = &like
	}
	return next
}

func AlterAdmission(s *parser.ForeignDDL) parser.Statement {
	if s.Inner != nil {
		return s.Inner
	}
	actions := make([]parser.AlterTableAction, 0, len(s.TableActions))
	for _, action := range s.TableActions {
		if action.Ordinary != nil {
			actions = append(actions, *action.Ordinary)
		}
	}
	return &parser.AlterTable{Name: s.Name, IfExists: s.IfExists, Actions: actions, Pos: s.Pos}
}

func ImportError(err error, name string) error {
	var d *sqlstate.Error
	if errors.As(err, &d) {
		copy := *d
		if copy.Context != "" {
			copy.Context += "\n"
		}
		copy.Context += fmt.Sprintf(`importing foreign table "%s"`, name)
		return sqlstate.WithDiagnostic(err, &copy)
	}
	return err
}
