// Package proc owns immutable SQL routine syntax and private invocation values.
// Resolution, catalog captures, execution and transactions belong above proc.
package proc

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type BodyKind uint8

const (
	SQLBody BodyKind = iota
	PLpgSQLBody
)

// InvocationKind is the function's immutable call and result contract.
type InvocationKind uint8

const (
	OrdinaryInvocation InvocationKind = iota
	DMLTriggerInvocation
	EventTriggerInvocation
)

// Definition contains syntax, not executable resources or permission decisions.
// Standard bodies additionally require a checked catalog-owned frozen program.
type Definition struct {
	Kind              BodyKind
	Procedure         bool
	Invocation        InvocationKind
	PLSyntax          *parser.PLBlock
	PL                *PLProgram
	Source            string
	Position          parser.Position
	Statements        []parser.Statement
	Standard          bool
	Name              string
	Names             []string
	Inputs            []sqltype.Usage
	Result            sqltype.Usage
	ReturnsSet        bool
	TableResult       bool
	Formals           []Formal
	InputFormals      []int
	OutputFormals     []int
	FormalInputs      []int
	PolymorphicResult bool
}

func New(source string, position parser.Position, inputs []sqltype.Usage, result sqltype.Usage) (*Definition, error) {
	statements, err := parser.ParseWithOptions(source, parser.Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: len(inputs)})
	if err != nil {
		return nil, err
	}
	if !DeclarationUsage(result) {
		return nil, fmt.Errorf("invalid SQL routine result usage")
	}
	for _, input := range inputs {
		if !DeclarationUsage(input) {
			return nil, fmt.Errorf("invalid SQL routine input usage")
		}
	}
	return &Definition{Source: source, Position: position, Statements: statements, Inputs: inputs, Result: result}, nil
}

func (d *Definition) Void() bool { return d.Result.Type.ID() == sqltype.Void }
