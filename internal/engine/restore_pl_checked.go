package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func decodeRestorePLChecked(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := routine.DecodePLChecked(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	r.plChecked = append(r.plChecked, routine.PositionedPLChecked{Record: record, End: position.Offset + len(comment)})
	return true, nil
}

func prepareRestorePLChecked(r *restoreRun, op *session.Operation, statement parser.Statement) error {
	create, ok := statement.(*parser.CreateFunction)
	if !ok {
		return nil
	}
	record, paired := r.plCheckedPairs[create]
	if !paired {
		return nil
	}
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	auth, err := r.stage.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	owner, exists := auth.Roles.Lookup(record.Owner)
	if !actor.Superuser || !exists {
		return fmt.Errorf("stuntgres: invalid checked PL replay owner authority")
	}
	capture := &core.PLCheckedReplayCapture{Statement: create, Owner: owner.Ref}
	r.cur.plChecked = capture
	core.OperationReplay(op).PLChecked = capture
	return nil
}

func afterRestorePLChecked(r *restoreRun, _ *session.Operation, statement parser.Statement, _ control.Outcome) error {
	create, ok := statement.(*parser.CreateFunction)
	if !ok {
		return nil
	}
	if _, paired := r.plCheckedPairs[create]; !paired {
		return nil
	}
	if r.cur.plChecked == nil || !r.cur.plChecked.Consumed {
		return fmt.Errorf("stuntgres: unconsumed checked PL replay metadata")
	}
	delete(r.plCheckedPairs, create)
	return nil
}

func finishRestorePLChecked(r *restoreRun, _ context.Context, _ *session.Operation) error {
	if len(r.plCheckedPairs) != 0 {
		return fmt.Errorf("stuntgres: unconsumed checked PL replay metadata")
	}
	return nil
}
