package engine

import (
	"fmt"
	"strings"
)

func indexDefinitionSQL(catalog CatalogRead, index *relationDescriptor) (string, error) {
	if err := validateIndexDefinition(catalog, index); err != nil {
		return "", err
	}
	parent := catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	schema := catalog.state.descriptors[parent.namespace].Name()
	var sql strings.Builder
	sql.WriteString("CREATE INDEX " + quoteIdentifier(index.name) + " ON " + quoteIdentifier(schema) + "." + quoteIdentifier(parent.name) + " USING btree (")
	for i, key := range index.sqlIndex.keys {
		if i > 0 {
			sql.WriteString(", ")
		}
		slot := -1
		for j, attribute := range parent.attributes {
			if attribute == key.attribute {
				slot = j
				break
			}
		}
		if slot < 0 {
			return "", fmt.Errorf("stuntgres: index attribute is missing")
		}
		sql.WriteString(quoteIdentifier(parent.columns[slot].Name))
		if key.desc {
			sql.WriteString(" DESC")
		} else {
			sql.WriteString(" ASC")
		}
		if key.nullsFirst {
			sql.WriteString(" NULLS FIRST")
		} else {
			sql.WriteString(" NULLS LAST")
		}
	}
	sql.WriteString(");\n")
	return sql.String(), nil
}
