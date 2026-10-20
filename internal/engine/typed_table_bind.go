package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

func bindTypedColumns(host analyzeHost, auth analyze.Authority, s *parser.CreateTable) ([]catalog.Column, []createColumnDeclaration, *catalog.Type, error) {
	typ, err := host.NamedType(auth, *s.OfType)
	if err != nil {
		return nil, nil, nil, err
	}
	path, err := host.ReferencePath(auth)
	if err != nil {
		return nil, nil, nil, err
	}
	source, err := typedef.StandaloneComposite(auth.ReadCatalog(), typ, path)
	if err == nil {
		err = host.CanUse(auth, host.CatalogOwner().Domain.Ref(auth.ReadCatalog().Epoch(), typ.Address()))
	}
	if err != nil {
		return nil, nil, nil, err
	}
	columns := make([]catalog.Column, len(source.Columns))
	declarations := make([]createColumnDeclaration, len(columns))
	for i, member := range source.Columns {
		if catalog.IsSystemColumn(member.Name) {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, member.Name))
		}
		columns[i] = catalog.Column{Name: member.Name, Usage: member.Usage, Nullable: true, NullabilityKnown: true}
	}
	seen := make(map[string]bool, len(s.Elements))
	for _, element := range s.Elements {
		column := element.Column
		if column == nil {
			continue
		}
		slot, exists := source.Index[column.Name]
		if !exists {
			return nil, nil, nil, sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, column.Name))
		}
		if seen[column.Name] {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		seen[column.Name] = true
		declaration, err := analyze.BindCreateColumnDeclaration(column, s.Name.Parts[len(s.Name.Parts)-1].Text)
		if err != nil {
			return nil, nil, nil, err
		}
		if declaration.Identity != nil {
			return nil, nil, nil, sqlError("0A000", "identity columns are not supported on typed tables")
		}
		if declaration.Generation != nil {
			return nil, nil, nil, sqlError("0A000", "generated columns are not supported on typed tables")
		}
		declarations[slot] = declaration
	}
	return columns, declarations, typ, nil
}
