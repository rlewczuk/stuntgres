package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ForeignTableDeclaration(s *parser.CreateTable) error {
	if len(s.Parents) != 0 || s.PartitionOf != nil || s.PartitionBy != nil {
		return sqlError("0A000", "foreign table hierarchies are unsupported in embedded mode")
	}
	for _, element := range s.Elements {
		if element.Unique != nil || element.ForeignKey != nil || element.Exclusion != nil {
			return sqlError("0A000", "unique, primary key, exclusion and foreign key constraints are unsupported on foreign tables")
		}
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.Unique != nil || constraint.ForeignKey != nil || constraint.Identity != nil {
					return sqlError("0A000", "unique, primary key, foreign key and identity constraints are unsupported on foreign tables")
				}
			}
		}
	}
	return nil
}

// PostgreSQL accepts these LIKE options for foreign destinations but does not
// copy their physical/identity properties. Apply that policy before the common
// binder or stage can create an index, sequence or physical column metadata.
func ForeignTableSyntax(table *parser.CreateTable) *parser.CreateTable {
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

func ForeignAlterAdmission(s *parser.ForeignDDL) parser.Statement {
	if s.Inner != nil {
		return s.Inner
	}
	return &parser.AlterTable{Name: s.Name, IfExists: s.IfExists, Pos: s.Pos}
}

func ForeignImportError(err error, name string) error {
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		if copy.Context != "" {
			copy.Context += "\n"
		}
		copy.Context += fmt.Sprintf(`importing foreign table "%s"`, name)
		return &copy
	}
	return err
}
