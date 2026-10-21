package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

// Both retained bound definitions and raw view trees keep the original four
// arguments; presentation chooses the ordinary name, not the SQL provenance.
func RenderOverlapsCall[T any](syntax parser.FunctionSyntax, args []T, ordinary string, child func(int, T) (string, error)) (string, error) {
	if len(args) != 4 {
		return "", fmt.Errorf("stuntgres: invalid stored OVERLAPS arity")
	}
	var parts [4]string
	for i, arg := range args {
		text, err := child(i, arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if syntax == parser.SQLFunctionSyntax {
		return "((" + parts[0] + ", " + parts[1] + ") OVERLAPS (" + parts[2] + ", " + parts[3] + "))", nil
	}
	return ordinary + "(" + parts[0] + ", " + parts[1] + ", " + parts[2] + ", " + parts[3] + ")", nil
}
