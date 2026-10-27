package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func afterRestoreStatisticsOwner(r *restoreRun, op *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	if _, ok := stmt.(*parser.CreateStatistics); !ok || outcome.Created == (ids.ObjectRef{}) || r.cur.ownerForCreate == (authorization.PrincipalRef{}) {
		return nil
	}
	pair, err := schemacmds.CaptureRestoredStatisticsOwner(r.stage, op, outcome.Created, r.cur.ownerForCreate)
	if err == nil {
		r.pairedStatisticsOwner = pair
	}
	return err
}

func admitRestoreStatisticsOwner(r *restoreRun, op *session.Operation, stmt parser.Statement) (control.Admission, error) {
	raw, ok := stmt.(*parser.AlterOwner)
	if !ok || raw.Kind != "statistics" {
		return control.NotHandled, nil
	}
	if err := schemacmds.RestoreStatisticsOwner(r.stage, op, raw, r.cur.statisticsOwnerPair); err != nil {
		return control.NotHandled, err
	}
	return control.Consumed, nil
}
