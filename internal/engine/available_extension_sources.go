package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// The initialization-only registry supplies availability; the captured catalog
// supplies installed state. Neither target-schema visibility nor provider calls
// participate in this metadata projection.
func captureAvailableExtensions(ctx context.Context, registry map[string]registeredExtension, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := []sqltype.ID{sqltype.Name, sqltype.Text, sqltype.Text, sqltype.Text}
	switch source.address.ObjectID {
	case 12081:
	case 12085:
		ids = []sqltype.ID{sqltype.Name, sqltype.Text, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Name, 1003, sqltype.Text}
	default:
		return nil, fmt.Errorf("stuntgres: invalid available extension source")
	}
	if len(source.columns) != len(ids) {
		return nil, fmt.Errorf("stuntgres: invalid available extension source shape")
	}
	for i, id := range ids {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign available extension source type")
		}
	}
	rows := make([][]sqltype.Value, 0, len(registry))
	for name, registration := range registry {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		address := catalog.state.names[NameKey{ClassID: ExtensionClassID, Name: name}]
		installed, _ := catalog.state.descriptors[address].(*extensionDescriptor)
		row, err := projectAvailableExtension(name, registration, installed, source)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

// Metadata-only component seam: fixed control schema is not the installed
// target namespace, and an unknown installed version never creates a package.
func projectAvailableExtension(name string, registration registeredExtension, installed *extensionDescriptor, source *relationDescriptor) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, len(source.columns))
	for i, column := range source.columns {
		row[i] = sqltype.Null(column.Usage.Type)
	}
	text := func(i int, value string) { row[i] = sqltype.String(source.columns[i].Usage.Type, value) }
	optional := func(i int, value *string) {
		if value != nil {
			text(i, *value)
		}
	}
	text(0, name)
	text(1, registration.version)
	if source.address.ObjectID == 12081 {
		if installed != nil {
			text(2, installed.version)
		}
		optional(3, registration.comment)
	} else {
		for i, value := range []bool{installed != nil && installed.version == registration.version, registration.superuser, registration.trusted, registration.relocatable} {
			row[i+2] = sqltype.Boolean(source.columns[i+2].Usage.Type, value)
		}
		optional(6, registration.schema)
		if len(registration.requires) != 0 {
			value, err := sqltype.Names(source.columns[7].Usage.Type, registration.requires)
			if err != nil {
				return nil, err
			}
			row[7] = value
		}
		optional(8, registration.comment)
	}
	return row, nil
}
