package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

// Builtin identity metadata only: no caller-created physical tablespaces exist.
func capturePGTablespace(ctx context.Context, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 2)
	for i, builtin := range [...]struct {
		oid  uint32
		name string
	}{{1663, "pg_default"}, {1664, "pg_global"}} {
		id, err := sqltype.OIDValue(source.columns[0].Usage.Type, builtin.oid)
		if err != nil {
			return nil, err
		}
		owner, err := sqltype.OIDValue(source.columns[2].Usage.Type, uint32(authorization.BootstrapPrincipalID))
		if err != nil {
			return nil, err
		}
		rows[i] = []sqltype.Value{id, sqltype.String(source.columns[1].Usage.Type, builtin.name), owner, sqltype.Null(source.columns[3].Usage.Type)}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
