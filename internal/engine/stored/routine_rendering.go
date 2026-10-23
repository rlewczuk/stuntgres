package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// A renderable node's provider symbol is immutable executable syntax. Only a
// genuinely relabelled declaration replaces it with its checked current SQL
// identity; unchanged provider formatting remains byte-for-byte unchanged.
func currentRoutineRendering(view catalog.Read, node expr.Expr, spec expr.Rendering) (expr.Rendering, error) {
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil || captured.Builtin == nil {
		return spec, err
	}
	current, err := exec.ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return spec, err
	}
	if current.Name() == current.Builtin.Identity.Name && current.Namespace() == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
		return spec, nil
	}
	namespace, ok := view.Namespace(current.Namespace())
	if !ok {
		return spec, fmt.Errorf("stuntgres: missing routine rendering namespace")
	}
	spec.Form = expr.FormCall
	spec.Name, spec.Replay.Name = current.Name(), current.Name()
	spec.Namespace = namespace.Name()
	spec.Pieces, spec.Replay.Pieces = nil, nil
	spec.Quote = expr.QuoteIdentifier
	return spec, nil
}
