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
func prepareAddedChecks(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, parent *catalog.Relation, raw []*parser.CheckConstraint) (table.PreparedAddedChecks, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.PrepareAddedChecks(alterHost{TableHost{DDLHost{E: e, Op: op}}, scope}, inheritancePlan(scope), auth, parent, raw)
}

func stageAddedChecks(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw []*parser.CheckConstraint, scans *[]catalog.CheckConstraint) (*catalog.Relation, error) {
	var prepared table.PreparedAddedChecks
	var err error
	if definition := inheritancePreparedNode(scope); definition != nil && !definition.PhaseZeroOnly {
		prepared = definition.Checks
		if prepared.Old != parent {
			return nil, ids.ErrCatalogReplaced
		}
		err = definition.ChecksError
	} else {
		prepared, err = prepareAddedChecks(e, scope, op, auth, parent, raw)
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
	inheritancePlan(scope).RecordChecks(parent, &next, raw, prepared.Ordinal)
	return &next, nil
}

func stageAddedUnique(e *Engine, op *session.Operation, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw *parser.UniqueConstraint) (*catalog.Relation, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	h := TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}
	next, err := table.StageAddedUnique(h, op, parent, raw)
	if err != nil {
		return nil, index.DefaultClassDiagnostic(h, auth, err)
	}
	return next, nil
}

func stageAddedForeignKey(e *Engine, scope *StatementScope, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw *parser.ForeignKeyConstraint, target, supporting ids.ObjectAddress) (*catalog.Relation, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.StageAddedForeignKey(TableHost{DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, parent, raw, target, supporting)
}
