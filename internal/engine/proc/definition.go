// Package proc owns SQL routine declarations and private invocation values.
// Name resolution, execution, authorization and transactions belong to engine.
package proc

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Definition is immutable after publication. Query is parsed syntax only: no
// executable query, session state or permission decision survives CREATE.
type Definition struct {
	Source   string
	Position parser.Position
	Query    *parser.Select
	Inputs   []sqltype.Usage
	Result   sqltype.Usage
}

func New(source string, position parser.Position, inputs []sqltype.Usage, result sqltype.Usage) (*Definition, error) {
	statements, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}
	if len(statements) != 1 {
		return nil, &sqlstate.Error{Code: "0A000", Message: "SQL function body must contain exactly one SELECT"}
	}
	query, ok := statements[0].(*parser.Select)
	if !ok {
		return nil, &sqlstate.Error{Code: "0A000", Message: "SQL function body must contain exactly one SELECT"}
	}
	if parser.LeftmostSelect(query).Into != nil {
		return nil, &sqlstate.Error{Code: "0A000", Message: "SELECT INTO is unsupported in SQL function bodies"}
	}
	if !result.Valid() || !result.Type.Concrete() {
		return nil, fmt.Errorf("invalid SQL routine result usage")
	}
	for _, input := range inputs {
		if !input.Valid() || !input.Type.Concrete() {
			return nil, fmt.Errorf("invalid SQL routine input usage")
		}
	}
	return &Definition{Source: source, Position: position, Query: query, Inputs: inputs, Result: result}, nil
}
