package engine

import (
	"testing"

	"stuntgres/internal/parser"
)

func castTarget(t *testing.T, q string) parser.Expr {
	t.Helper()
	return statement(t, q).(*parser.Select).Targets[0]
}
