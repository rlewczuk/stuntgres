// Package proc owns immutable SQL routine syntax and private invocation values.
// Resolution, catalog captures, execution and transactions belong above proc.
package proc

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Definition contains syntax, not executable resources or permission decisions.
// Standard bodies additionally require a checked catalog-owned frozen program.
type Definition struct {
	Source        string
	Position      parser.Position
	Statements    []parser.Statement
	Standard      bool
	Name          string
	Names         []string
	Inputs        []sqltype.Usage
	Result        sqltype.Usage
	Formals       []Formal
	InputFormals  []int
	OutputFormals []int
	FormalInputs  []int
}

func New(source string, position parser.Position, inputs []sqltype.Usage, result sqltype.Usage) (*Definition, error) {
	statements, err := parser.ParseWithOptions(source, parser.Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: len(inputs)})
	if err != nil {
		return nil, err
	}
	if !result.Valid() || !result.Type.Concrete() {
		return nil, fmt.Errorf("invalid SQL routine result usage")
	}
	for _, input := range inputs {
		if !input.Valid() || !input.Type.Concrete() {
			return nil, fmt.Errorf("invalid SQL routine input usage")
		}
	}
	return &Definition{Source: source, Position: position, Statements: statements, Inputs: inputs, Result: result}, nil
}

func (d *Definition) Void() bool { return d.Result.Type.ID() == sqltype.Void }
