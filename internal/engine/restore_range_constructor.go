package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// admitRestoreInsert extends syntax admission only for the qualified names
// emitted by the dump renderer. Catalog membership is not overload resolution
// or permission to execute: the ordinary binder must select a genuine owned
// constructor whenever the occurrence is not an incumbent static recipe.
func admitRestoreInsert(r *restoreRun, _ *session.Operation, s *parser.Insert) error {
	r.stage.mu.Lock()
	defer r.stage.mu.Unlock()
	view := r.stage.catalog.read(r.stage.catalog.state)
	eligible := func(raw parser.FunctionExpr) bool { return catalog.RestoreRangeConstructorEligible(view, raw) }
	if err := control.CheckInsert(s, eligible); err != nil {
		return err
	}
	return nil
}
