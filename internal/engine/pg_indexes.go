package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The SQL source owns disclosure, not the ACL-neutral structural projections.
// Authorization and deparsing both read the exact immutable statement capture.
func capturePGIndexes(ctx context.Context, catalog CatalogRead, auth AuthorizationView, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.Owner() == nil || catalog.State() == nil || auth.engine.catalog.owner() != catalog.Owner() || auth.catalog.State() != catalog.State() || auth.catalog.Owner() != catalog.Owner() || ref.Domain() != catalog.Owner().Domain || ref.Epoch != catalog.Epoch() || ref.Address.SubID != 0 || source == nil || source.Address() != ref.Address || source.Address().ObjectID != 12002 || catalog.Get(ref.Address) != source {
		return nil, fmt.Errorf("stuntgres: invalid pg_indexes capture")
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := catalog.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index parent")
		}
		schema, ok := catalog.Find(parent.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index namespace")
		}
		schemaRef := ref.Domain().Ref(ref.Epoch, parent.Namespace())
		err := auth.CanUse(schemaRef)
		if err == nil {
			err = auth.checkColumns(binding{ref: ref.Domain().Ref(ref.Epoch, parent.Address()), relation: parent}, authorization.SelectPrivilege, nil)
		}
		if err != nil {
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
				continue
			}
			return nil, err
		}
		definition, err := indexDefinitionSQL(ctx, catalog, index, indexPresentation{})
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
	return &querySource{ref: ref, anonymous: rows}, nil
}
