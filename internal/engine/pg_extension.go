package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

func capturePGExtension(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := [8]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Bool, sqltype.Text, 1028, 1009}
	if len(source.columns) != len(ids) {
		return nil, fmt.Errorf("stuntgres: invalid extension source shape")
	}
	for i, column := range source.columns {
		owned, ok := catalog.LookupType(ids[i])
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign extension source type")
		}
	}
	// There is no installed-extension provider. Available files and seeded type
	// identities are not installed extensions, so no tuples are synthesized.
	// A nonnil empty bag identifies a captured virtual source to the existing
	// query pipeline; it needs no tuple or backing-array allocation.
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{}}, nil
}
