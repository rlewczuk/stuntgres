package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"

	"stuntgres/internal/sqltype"
)

// Anonymous producers transfer their already-owned typed rows into the same
// final cursor contract; empty relations still carry an explicit schema.
func anonymousResult(schema []catalog.Column, rows [][]sqltype.Value, count int64) (Result, error) {
	r := exec.NewFinalRows(schema, len(rows))
	r.Anonymous = rows
	for _, row := range rows {
		if len(row) != len(schema) {
			return Result{}, fmt.Errorf("stuntgres: invalid anonymous result width")
		}
		for j, value := range row {
			if !value.ValidResult(schema[j].Usage) {
				return Result{}, fmt.Errorf("stuntgres: invalid anonymous result type")
			}
		}
	}
	return Result{Count: count, Rows: r}, nil
}
