package functions

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
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

// checkPresent rejects a nil child where the node has no optional slots.
func checkPresent(message string, children []expr.Expr) error {
	for _, child := range children {
		if child == nil {
			return errors.New(message)
		}
	}
	return nil
}

// Rebuild keeps descriptors a node retains from binding. Only retag replaces
// them, through the node's Retag hook.
func keepDescriptor(d *sqltype.Descriptor) *sqltype.Descriptor { return d }

func keepValue(v sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) { return v, nil }
