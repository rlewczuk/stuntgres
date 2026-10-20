package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const CursorsOID ids.OID = 12077

// Session admission protects this registry. Only registry membership determines
// visibility: producer teardown and HOLD promotion do not remove metadata.
func capturePGCursors(in Input) ([][]sqltype.Value, error) {
	ctx, d := in.Context, in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, in.Runtime.CursorCount())
	err := in.Runtime.VisitCursors(func(cursor CursorFact) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Immutable builtin timestamp data outlives catalog replacement. Give
		// this observation its current descriptor without rebinding the cursor.
		created, err := cursor.Created.Retag(d.Columns[5].Usage.Type)
		if err != nil {
			return err
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(d.Columns[0].Usage.Type, cursor.Name),
			sqltype.String(d.Columns[1].Usage.Type, cursor.Statement),
			sqltype.Boolean(d.Columns[2].Usage.Type, cursor.Hold),
			sqltype.Boolean(d.Columns[3].Usage.Type, false),
			sqltype.Boolean(d.Columns[4].Usage.Type, cursor.Scroll),
			created,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
