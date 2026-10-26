package analyze

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// bindCreateTable binds CREATE TABLE columns, LIKE sources, inheritance and
// constraint declarations into b.
func bindCreateTable(host Host, auth Authority, s *parser.CreateTable, b *Binding) error {
	var columns []catalog.Column
	declarations := make([]ColumnDeclaration, 0, len(s.Elements))
	table := s.Name.Parts[len(s.Name.Parts)-1].Text
	var inherited []LikeConstraint
	if s.OfType != nil {
		var err error
		columns, declarations, b.Create.OfType, err = bindTypedColumns(host, auth, s)
		if err != nil {
			return err
		}
	}
	for elementIndex, element := range s.Elements {
		kinds := 0
		if element.Column != nil {
			kinds++
		}
		if element.Like != nil {
			kinds++
		}
		if element.NotNull != nil {
			kinds++
		}
		if element.Check != nil {
			kinds++
		}
		if element.Unique != nil {
			kinds++
		}
		if element.Exclusion != nil {
			kinds++
		}
		if element.ForeignKey != nil {
			kinds++
		}
		if kinds != 1 {
			return fmt.Errorf("stuntgres: invalid table element")
		}
		if element.NotNull != nil || element.Check != nil || element.Unique != nil || element.Exclusion != nil || element.ForeignKey != nil {
			continue
		}
		if c := element.Column; c != nil {
			if s.OfType != nil {
				continue
			}
			typeName := c.TypeName
			if lowered := sequence.SerialTypeName(typeName); lowered != "" {
				typeName.Name = lowered
			}
			u, err := host.ResolveType(typeName)
			if err != nil {
				return err
			}
			if err := sqltype.CheckStoredColumn(c.Name, u); err != nil {
				return err
			}
			u, err = host.ResolveColumnCollation(u, c.Collation)
			if err != nil {
				return err
			}
			declaration, err := BindCreateColumnDeclaration(c, table)
			if err != nil {
				return err
			}
			if declaration.Identity != nil && u.Type.ID() != sqltype.Int2 && u.Type.ID() != sqltype.Int4 && u.Type.ID() != sqltype.Int8 {
				return sequence.OptionError("sequence type must be smallint, integer, or bigint")
			}
			declarations = append(declarations, declaration)
			columns = append(columns, catalog.Column{Name: c.Name, Usage: u, DeclaredDimensions: int16(len(typeName.ArrayBounds)), Nullable: true, NullabilityKnown: true})
		} else {
			like := element.Like
			ref, err := host.ResolveReference(auth, like.Name)
			if err != nil {
				if diagnostic, ok := err.(*sqlstate.Error); ok {
					copy := *diagnostic
					if copy.Code == "42P01" {
						copy.Message = fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(like.Name))
					}
					copy.Position = ""
					if pos := like.Name.Parts[0].Pos.Character; pos > 0 {
						copy.Position = strconv.Itoa(pos)
					}
					err = &copy
				}
				return err
			}
			source := auth.ReadCatalog().MustRelation(ref.Address)
			if source.RelationKind != catalog.TableRelation && source.RelationKind != catalog.ForeignRelation && source.RelationKind != catalog.ViewRelation && source.RelationKind != catalog.CompositeRelation {
				kind := "relations of this kind"
				if source.RelationKind == catalog.SequenceRelation {
					kind = "sequences"
				}
				if source.RelationKind == catalog.IndexRelation {
					kind = "indexes"
				}
				diagnostic := sqlError("42809", fmt.Sprintf(`relation "%s" is invalid in LIKE clause`, source.Name())).(*sqlstate.Error)
				diagnostic.Detail = "This operation is not supported for " + kind + "."
				if pos := like.Name.Parts[0].Pos.Character; pos > 0 {
					diagnostic.Position = strconv.Itoa(pos)
				}
				return diagnostic
			}
			var sourceType *catalog.Type
			if source.RelationKind == catalog.CompositeRelation {
				typeRef := host.CatalogOwner().Domain.Ref(auth.ReadCatalog().Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
				var ok bool
				sourceType, ok = auth.ReadCatalog().Type(typeRef.Address)
				if !ok || sourceType.OwningRelation != source.Address() || sourceType.SQLType.ID() != source.RowType {
					return fmt.Errorf("stuntgres: invalid LIKE composite type provider")
				}
				if err := host.CanUse(auth, typeRef); err != nil {
					return err
				}
			} else if err := host.CheckPrivilege(auth, access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
				return err
			}
			offset := len(columns)
			b.Create.Likes = append(b.Create.Likes, LikeInput{Ref: ref, Source: source, SourceType: sourceType, Offset: offset, Options: TableLikeOptions(like.Options)})
			start := len(inherited)
			for _, constraint := range source.Constraints.NotNull {
				for i, attribute := range source.Attributes {
					if constraint.Attribute == attribute {
						inherited = append(inherited, LikeConstraint{Element: elementIndex, NotNullConstraint: catalog.NotNullConstraint{Name: constraint.Name, Attribute: int32(offset + i + 1)}})
						break
					}
				}
			}
			// PostgreSQL copies via its relation/type/name constraint index.
			slices.SortFunc(inherited[start:], func(a, b LikeConstraint) int {
				return strings.Compare(a.Name, b.Name)
			})
			for _, c := range source.Columns {
				columns = append(columns, catalog.Column{Name: c.Name, Usage: c.Usage, Nullable: c.Nullable, NullabilityKnown: c.NullabilityKnown})
				declarations = append(declarations, ColumnDeclaration{})
			}
		}
	}
	var mergeErr error
	columns, declarations, inherited, mergeErr = describeInheritance(host, auth, s, b, columns, declarations, inherited)
	if mergeErr != nil {
		return mergeErr
	}
	if len(columns) > 1600 {
		return sqlError("54011", "tables can have at most 1600 columns")
	}
	b.Create.Columns = columns
	var err error
	b.Create.ColumnIndex, err = BindCreateColumnIndex(columns)
	if err != nil {
		return err
	}
	b.Create.Uniques, err = BindCreateUniques(s, b.Create.ColumnIndex, b.Create.Likes, auth.ReadCatalog())
	if err != nil {
		return err
	}
	b.Create.NotNull, err = BindCreateNotNull(s, columns, declarations, inherited, b.Create.Uniques, b.Create.ColumnIndex)
	if err != nil {
		return err
	}
	b.Create.Declarations = declarations
	return nil
}
