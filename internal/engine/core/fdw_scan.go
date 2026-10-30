package core

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Capture only metadata while locked. Planning, source admission, conversion,
// restart and teardown all run from internal demand after the mutex is released.
func (e *Engine) captureForeignSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation, invoker authorization.PrincipalRef, uses []int, mark plan.RowMark) (*exec.Source, error) {
	if err := CheckRestoreEffect(scope.Op); err != nil {
		return nil, err
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	handler, request, err := fdw.ScanRequest(auth.View, invoker, relation, uses)
	if err != nil {
		return nil, err
	}
	importer, exporter, ok := e.Foreign.Codecs()
	if !ok {
		return nil, fmt.Errorf("stuntgres: foreign driver value codecs are not installed")
	}
	f := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Request: request, Columns: slices.Clone(relation.Columns), ImportValue: importer, ExportValue: exporter}
	scope.ownForeignSource(f)
	return &exec.Source{Ref: ref, Anonymous: [][]sqltype.Value{}, Foreign: f}, nil
}
