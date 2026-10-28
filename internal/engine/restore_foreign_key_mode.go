package engine

import (
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func decodeRestoreForeignKeyMode(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := stored.DecodeForeignKeyMode(comment)
	if err != nil || !recognized { return recognized, err }
	r.foreignKeyModes = append(r.foreignKeyModes, stored.PositionedForeignKeyMode{
		Record: record, Start: position.Offset-2, End: position.Offset+len(comment),
	})
	return true, nil
}
