package engine

import (
	"context"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
)

func decodeRestoreTriggerClone(r *restoreRun, comment string, position parser.Position) (bool, error) {
	record, recognized, err := stored.DecodeTriggerClone(comment)
	if err != nil || !recognized { return recognized, err }
	r.triggerClones = append(r.triggerClones, stored.PositionedTriggerClone{Record: record, End: position.Offset + len(comment)})
	return true, nil
}

func afterRestoreTriggerClone(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	trigger, ok := stmt.(*parser.CreateTrigger)
	if !ok { return nil }
	if err := viewcmds.CheckRestoredTriggerCloneCreate(r.stage, op, trigger, r.triggerCloneCaptures); err != nil { return err }
	record, paired := r.triggerClonePairs[trigger]
	if !paired { return nil }
	capture, err := viewcmds.CaptureRestoredTriggerClone(r.stage, op, record)
	if err != nil { return err }
	r.triggerCloneCaptures = append(r.triggerCloneCaptures, capture)
	return nil
}

func finishRestoreTriggerClones(r *restoreRun, ctx context.Context, op *session.Operation) error {
	return viewcmds.ImportTriggerCloneLinks(r.stage, ctx, op, r.triggerCloneCaptures)
}
