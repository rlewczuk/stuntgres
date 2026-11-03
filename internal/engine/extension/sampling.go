package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// SamplingArgumentType authenticates the complete installed declaration before
// selecting either closed engine sampling recipe. Mutable SQL names, a matching
// signature or copied extension membership do not confer execution authority.
func SamplingArgumentType(v catalog.Read, procedure *catalog.Procedure) (sqltype.ID, error) {
	symbol, err := ProcedureSymbol(v, procedure)
	if err != nil {
		return 0, err
	}
	switch {
	case procedure.Implementation == StockSystemRows && symbol == "tsm_system_rows_handler":
		return sqltype.Int8, nil
	case procedure.Implementation == StockSystemTime && symbol == "tsm_system_time_handler":
		return sqltype.Float8, nil
	default:
		return 0, fmt.Errorf("stuntgres: invalid sampling handler capability")
	}
}
