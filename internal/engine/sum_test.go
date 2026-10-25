package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func sumUnitProvider(e *Engine, id sqltype.ID) (functions.AggregateProvider, *sqltype.Descriptor) {
	input, _ := e.CatalogRead().LookupType(id)
	switch id {
	case sqltype.Int2, sqltype.Int4:
		result, _ := e.CatalogRead().LookupType(sqltype.Int8)
		return functions.NewIntegralSum(input, result.Usage()), input
	case sqltype.Int8, sqltype.Numeric:
		result, _ := e.CatalogRead().LookupType(sqltype.Numeric)
		return functions.NewNumericSum(input, result.Usage()), input
	default:
		return functions.NewFloatSum(input, input.Usage()), input
	}
}
