package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const pgCursorsOID ids.OID = 12077

// Session admission protects this registry. Only registry membership determines
// visibility: producer teardown and HOLD promotion do not remove metadata.
func capturePGCursors(ctx context.Context, ref ids.ObjectRef, d *relationDescriptor, cursors *cursorRegistry) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, len(cursors.entries))
	for name, cursor := range cursors.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Immutable builtin timestamp data outlives catalog replacement. Give
		// this observation its current descriptor without rebinding the cursor.
		created, err := cursor.created.Retag(d.columns[5].Usage.Type)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(d.columns[0].Usage.Type, name),
			sqltype.String(d.columns[1].Usage.Type, cursor.statement),
			sqltype.Boolean(d.columns[2].Usage.Type, cursor.hold),
			sqltype.Boolean(d.columns[3].Usage.Type, false),
			sqltype.Boolean(d.columns[4].Usage.Type, cursor.scroll),
			created,
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
