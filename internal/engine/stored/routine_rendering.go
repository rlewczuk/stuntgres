package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// A renderable node's provider symbol is immutable executable syntax. Only a
// genuinely relabelled declaration replaces it with its checked current SQL
// identity; unchanged provider formatting remains byte-for-byte unchanged.
func currentRoutineRendering(view catalog.Read, node expr.Expr, spec expr.Rendering) (expr.Rendering, error) {
	if call, installed := citextRegexpCallOf(node); installed {
		current, err := currentCitextRegexp(view, call)
		if err != nil { return spec, err }
		namespace, ok := view.Namespace(current.Namespace())
		if !ok { return spec, fmt.Errorf("stuntgres: missing citext regexp namespace") }
		spec.Name = current.Name()
		spec.Namespace = namespace.Name()
		spec.Quote = expr.QuoteIdentifier
		return spec, nil
	}
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		return spec, err
	}
	cast, installedCast := node.(functions.CastRoutine)
	if captured.Builtin == nil && !installedCast {
		return spec, nil
	}
	current, err := exec.ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return spec, err
	}
	if captured.Builtin != nil {
		if current.Name() == current.Builtin.Identity.Name && current.Namespace() == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
			return spec, nil
		}
	} else {
		symbol, err := extension.ProcedureSymbol(view, current)
		if err != nil {
			return spec, err
		}
		if extension.CitextProviderOf(current) != extension.StockCitext || symbol != "citext" || cast.Count != 1 || current.Signature != extension.CitextSignature(cast.Formals[0]) || current.Result != ids.OID(cast.Result) {
			return spec, fmt.Errorf("stuntgres: invalid installed cast rendering identity")
		}
		if err := extension.StockCitext.ValidateProcedure(view, current); err != nil {
			return spec, err
		}
		if err := cast.ValidateStoredProvider(); err != nil {
			return spec, err
		}
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
