package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type mathObservedArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (e mathObservedArgument) Eval(expr.Context) (sqltype.Value, error) {
	*e.order = append(*e.order, e.index)
	return e.value, e.err
}

func mathTuples(t *testing.T, result Result) [][]any {
	t.Helper()
	cursor := independentFinalRows(result.Rows)
	defer cursor.Close()
	var rows [][]any
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(context.Background(), values)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		row := make([]any, len(values))
		for i, v := range values {
			if v.IsNull() {
				continue
			}
			switch v.Type().ID() {
			case sqltype.Numeric:
				row[i], err = v.Type().Output(context.Background(), v)
			case sqltype.Float8:
				row[i], err = v.Float()
			default:
				row[i], err = v.Integer()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows, row)
	}
	return rows
}
