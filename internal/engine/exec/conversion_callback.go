package exec

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func (c *Context) EnterEncodingConversion(reference sqltype.EncodingConversionReference) (sqltype.RoutineBodyToken, error) {
	callback, ok := reference.(catalog.EncodingConversionCallback)
	if !ok || c == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: invalid encoding conversion callback authority")
	}
	if c.ClosedEvaluation != nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: encoding conversion requires live borrowed owner")
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.RoutineBodyToken{}, fmt.Errorf("stuntgres: missing encoding conversion callback owner")
	}
	return host.EnterEncodingConversionCallback(callback)
}
