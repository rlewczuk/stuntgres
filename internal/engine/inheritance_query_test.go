package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/sqltype"
)

func inheritanceQueryInt(e *core.Engine, n int64) sqltype.Value {
	value, err := sqltype.Integer(testUsage(e, sqltype.Int4).Type, n)
	if err != nil {
		panic(err)
	}
	return value
}
