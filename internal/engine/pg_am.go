package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const pgAMOID ids.OID = 2601

// Released identities for implemented logical methods, not physical storage
// algorithms or an inventory of currently instantiated user indexes.
var pgAMMethods = [...]struct {
	oid     uint32
	name    string
	handler uint32
	kind    string
}{
	{2, "heap", 3, "t"},
	{403, "btree", 330, "i"},
	{405, "hash", 331, "i"},
	{783, "gist", 332, "i"},
}

func capturePGAM(ctx context.Context, catalog catalog.Read, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || len(source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid access-method source shape")
	}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Regproc, sqltype.Char} {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign access-method source type")
		}
	}
	rows := make([][]sqltype.Value, len(pgAMMethods))
	values := make([]sqltype.Value, len(pgAMMethods)*4)
	for i, fact := range pgAMMethods {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := values[i*4 : (i+1)*4 : (i+1)*4]
		var err error
		row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, fact.oid)
		if err != nil {
			return nil, err
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, fact.name)
		row[2], err = sqltype.RegprocValue(source.Columns[2].Usage.Type, fact.handler)
		if err != nil {
			return nil, err
		}
		row[3] = sqltype.String(source.Columns[3].Usage.Type, fact.kind)
		rows[i] = row
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
