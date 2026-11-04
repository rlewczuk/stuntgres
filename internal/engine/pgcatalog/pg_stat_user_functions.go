package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const StatUserFunctionsOID ids.OID = 12279

func captureFunctionStatistics(in Input) ([][]sqltype.Value, error) {
	return captureFunctionStatisticsRows(in, StatUserFunctionsOID)
}
