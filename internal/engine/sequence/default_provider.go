package sequence

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Raw and cooked DEFAULT consumers share the physical TEMP provider boundary.
// Cooked copies supply no position: their original call token is not in LIKE SQL.
func ValidateDefaultTemporaryProvider(provider, consumer ids.SessionToken, position parser.Position) error {
	if provider != (ids.SessionToken{}) && provider != consumer {
		return expr.ErrorAt("0A000", "temporary sequence DEFAULT is unsupported", position)
	}
	return nil
}
