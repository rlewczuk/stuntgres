package pgcatalog

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The SQL source owns disclosure, not the ACL-neutral structural projections.
// Authorization and deparsing both read the exact immutable statement capture.
func capturePGIndexes(in Input) ([][]sqltype.Value, error) {
	ctx, view, ref, source := in.Context, in.Catalog, in.Ref, in.Source

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(view, in.Roles, ref, source, 12002); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index parent")
		}
		schema, ok := view.Find(parent.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index namespace")
		}
		schemaRef := ref.Domain().Ref(ref.Epoch, parent.Namespace())
		err := in.Runtime.CanUse(schemaRef)
		if err == nil {
			err = in.Runtime.SelectColumns(ref.Domain().Ref(ref.Epoch, parent.Address()), parent)
		}
		if err != nil {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
				continue
			}
			return nil, err
		}
		definition, err := in.Runtime.IndexDefinition(ctx, view, index)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(source.Columns[0].Usage.Type, schema.Name()),
			sqltype.String(source.Columns[1].Usage.Type, parent.Name()),
			sqltype.String(source.Columns[2].Usage.Type, index.Name()),
			sqltype.Null(source.Columns[3].Usage.Type),
			sqltype.String(source.Columns[4].Usage.Type, definition),
		})
	}
	return rows, nil
}
