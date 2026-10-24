package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// prepareAddedChecks cooks ADD CHECK under Engine.mu. Definition cooking is
// independent of stage ownership. Admission retains this exact result; the
// sole physical pipeline installs it and scans rows once.
func (e *Engine) prepareAddedChecks(scope *StatementScope, op *session.Operation, auth AuthorizationView, parent *catalog.Relation, raw []*parser.CheckConstraint) (table.PreparedAddedChecks, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.PrepareAddedChecks(alterHost{ddlHost{e: e, op: op}, scope}, scope.inheritancePlan(), auth, parent, raw)
}

func (e *Engine) stageAddedChecks(scope *StatementScope, op *session.Operation, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw []*parser.CheckConstraint, scans *[]catalog.CheckConstraint) (*catalog.Relation, error) {
	var prepared table.PreparedAddedChecks
	var err error
	if definition := scope.inheritancePreparedNode(); definition != nil && !definition.PhaseZeroOnly {
		prepared = definition.Checks
		if prepared.Old != parent {
			return nil, ids.ErrCatalogReplaced
		}
		err = definition.ChecksError
	} else {
		prepared, err = e.prepareAddedChecks(scope, op, auth, parent, raw)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return parent, nil
	}
	// The cached definition stays immutable even when OIDs/revision are assigned
	// by the later normal pipeline.
	next := *prepared.Next
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
	*scans = append(*scans, prepared.Scans...)
	scope.inheritancePlan().RecordChecks(parent, &next, raw, prepared.Ordinal)
	return &next, nil
}

func (e *Engine) stageAddedUnique(op *session.Operation, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw *parser.UniqueConstraint) (*catalog.Relation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := ddlHost{e: e, op: op, stage: stage}
	next, err := table.StageAddedUnique(h, op, parent, raw)
	if err != nil {
		return nil, index.DefaultClassDiagnostic(h, auth, err)
	}
	return next, nil
}

func (e *Engine) stageAddedForeignKey(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw *parser.ForeignKeyConstraint, target, supporting ids.ObjectAddress) (*catalog.Relation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return table.StageAddedForeignKey(ddlHost{e: e, op: scope.op, stage: stage}, auth, parent, raw, target, supporting)
}
