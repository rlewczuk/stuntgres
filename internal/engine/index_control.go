package engine

import (
	"context"
	"fmt"
	"strings"
)

type indexPresentation struct {
	dump   bool
	column int64
	pretty bool
	path   []ObjectAddress
}

func indexDefinitionSQL(ctx context.Context, catalog CatalogRead, index *relationDescriptor, presentation indexPresentation) (string, error) {
	if err := validateIndexDefinition(catalog, index); err != nil {
		return "", err
	}
	parent := catalog.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if presentation.column != 0 {
		if presentation.column < 1 || presentation.column > int64(len(index.sqlIndex.keys)) {
			return "", nil
		}
		key := index.sqlIndex.keys[presentation.column-1]
		if key.derived != nil {
			return (scalarRenderer{owner: parent}).sql(ctx, catalog, key.derived.definition)
		}
		return deparseAttribute(parent, key.attribute)
	}
	identifier := pgIdentifier
	if presentation.dump {
		identifier = quoteIdentifier
	}
	schema := catalog.state.descriptors[parent.namespace].Name()
	parentName := identifier(schema) + "." + identifier(parent.name)
	if presentation.pretty {
		parentName = relationDiagnosticNameAt(catalog, parent, presentation.path)
	}
	var sql strings.Builder
	sql.WriteString("CREATE ")
	if index.sqlIndex.unique != nil {
		sql.WriteString("UNIQUE ")
	}
	sql.WriteString("INDEX " + identifier(index.name) + " ON " + parentName + " USING btree (")
	for i, key := range index.sqlIndex.keys {
		if i > 0 {
			sql.WriteString(", ")
		}
		if key.derived != nil {
			text, err := (scalarRenderer{owner: parent}).sql(ctx, catalog, key.derived.definition)
			if err != nil {
				return "", err
			}
			sql.WriteString("(" + text + ")")
		} else {
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
			sql.WriteString(identifier(parent.columns[slot].Name))
		}
		if key.desc {
			sql.WriteString(" DESC")
		} else if presentation.dump {
			sql.WriteString(" ASC")
		}
		if presentation.dump || key.nullsFirst != key.desc {
			if key.nullsFirst {
				sql.WriteString(" NULLS FIRST")
			} else {
				sql.WriteString(" NULLS LAST")
			}
		}
	}
	sql.WriteString(")")
	if index.sqlIndex.unique != nil && index.sqlIndex.unique.NullsNotDistinct {
		sql.WriteString(" NULLS NOT DISTINCT")
	}
	if presentation.dump {
		sql.WriteString(";\n")
	}
	return sql.String(), nil
}
