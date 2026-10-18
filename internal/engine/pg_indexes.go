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
	if catalog.owner == nil || catalog.state == nil || auth.engine.catalog.owner() != catalog.owner || auth.catalog.state != catalog.state || auth.catalog.owner != catalog.owner || ref.Domain() != catalog.owner.domain || ref.Epoch != catalog.Epoch() || ref.Address.SubID != 0 || source == nil || source.address != ref.Address || source.address.ObjectID != 12002 || catalog.Get(ref.Address) != source {
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
		parent, ok := catalog.Relation(index.SQLIndex.parent)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index parent")
		}
		schema, ok := catalog.Find(parent.namespace)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index namespace")
		}
		schemaRef := ref.Domain().Ref(ref.Epoch, parent.namespace)
		err := auth.CanUse(schemaRef)
		if err == nil {
			err = auth.checkColumns(binding{ref: ref.Domain().Ref(ref.Epoch, parent.address), relation: parent}, authorization.SelectPrivilege, nil)
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
			sqltype.String(source.Columns[1].Usage.Type, parent.name),
			sqltype.String(source.Columns[2].Usage.Type, index.name),
			sqltype.Null(source.Columns[3].Usage.Type),
			sqltype.String(source.Columns[4].Usage.Type, definition),
		})
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
