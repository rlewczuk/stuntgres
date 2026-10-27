package pgcatalog

import (
	"iter"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// RoleSettingsProjection borrows one immutable complete-record capture. It
// contains persisted rows only, never provider defaults or live session values.
type RoleSettingsProjection interface {
	Config(authorization.PrincipalRef) iter.Seq[string]
	Rows() iter.Seq2[RoleSettingScope, iter.Seq[string]]
}

type RoleSettingScope struct {
	Database ids.OID
	Role     authorization.PrincipalRef
}

func configArray(typ *sqltype.Descriptor, config iter.Seq[string]) (sqltype.Value, error) {
	if config == nil {
		return sqltype.Null(typ), nil
	}
	var values []sqltype.Value
	for entry := range config {
		values = append(values, sqltype.String(typ.Element(), entry))
	}
	if len(values) == 0 {
		return sqltype.Null(typ), nil
	}
	return sqltype.OwnedArrayValue(typ, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}, values)
}

func roleConfig(in Input, principal authorization.PrincipalRef, typ *sqltype.Descriptor) (sqltype.Value, error) {
	if in.RoleSettings == nil {
		return sqltype.Null(typ), nil
	}
	return configArray(typ, in.RoleSettings.Config(principal))
}
