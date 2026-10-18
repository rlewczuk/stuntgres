package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

// Nodes whose Reconstruct depends only on metadata and children implement the
// expr node protocol. Rebuild delegates to Reconstruct after checking that the
// children match Children in length. Fixed-arity nodes keep any argument slots
// beyond Children unchanged.

func rebuilt[T expr.Expr](node T, err error) (expr.Expr, error) {
	if err != nil {
		return nil, err
	}
	return node, nil
}

func checkChildren(name string, children []expr.Expr, want int) error {
	if len(children) != want {
		return fmt.Errorf("stuntgres: invalid rebuilt %s arity", name)
	}
	return nil
}
