package functions

import "stuntgres/internal/engine/expr"
import "stuntgres/internal/sqltype"

func (e Binary) evalConversion(c expr.Context, values []sqltype.Value) (sqltype.Value, error) {
	ctx := c.FunctionContext()
	raw, sourceName, destinationName, null, err := e.Plan.EncodingInput(ctx, values...)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	source, destination, unchanged, err := sqltype.PrepareBinaryEncoding(ctx, raw, sourceName, destinationName)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !unchanged {
		handler, err := c.FunctionBinaryConversion(source, destination)
		if err != nil {
			return sqltype.Value{}, err
		}
		if handler == nil {
			return sqltype.Value{}, sqltype.MissingBinaryConversion(source, destination)
		}
		var consumed int
		output, consumed, err := handler.ConvertValidated(ctx, raw, source, destination)
		if err != nil {
			return sqltype.Value{}, err
		}
		if consumed != len(raw) {
			return sqltype.Value{}, sqlError("XX000", "encoding conversion function returned incorrect result")
		}
		raw = output
	}
	return e.Plan.EncodingResult(raw)
}
