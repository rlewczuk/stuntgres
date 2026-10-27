package core

import (
	"stuntgres/internal/engine/catalog"

	"stuntgres/internal/sqltype"
)

func settingColumns(typ *sqltype.Descriptor, names ...string) []catalog.Column {
	columns := make([]catalog.Column, len(names))
	for i, name := range names {
		columns[i] = catalog.Column{Name: name, Usage: typ.Usage(), NullabilityKnown: true}
	}
	return columns
}
