package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

// CaptureInheritedForeignKey captures an inherited FOREIGN KEY that a
// private Restore ALTER TABLE adds, for binding at finish; it reports false
// for any other ALTER.
//
// This is not a general valid ALTER whitelist. Only a successful isolated
// import of both actual relations, with a strict ordinary inheritance edge,
// can issue a receipt. The incumbent pending binder remains the sole FK binder.
func CaptureInheritedForeignKey(e *core.Engine, op *session.Operation, raw *parser.AlterTable, created map[ids.ObjectAddress]ids.ObjectRef) (table.RestoredForeignKeys, bool, error) {
	if handled, err := table.InheritedForeignKeyShape(raw); !handled || err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	foreign := raw.Actions[0].ForeignKey
	pending, err := tablecmds.CaptureRestoredForeignKeys(e, op, []parser.ForeignKeyConstraint{*foreign})
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	auth := core.DDLAuthorityOf(pending.Auth)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !core.PartitionConstraintReplayImport(e, op, auth) {
		return table.RestoredForeignKeys{}, false, core.SQLError("0A000", table.RestoreInheritedForeignKeyBoundary)
	}
	sourceRef, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	targetRef, err := e.ResolveRelationReference(auth, foreign.Target, false)
	if err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	if err := table.CaptureInheritedForeignKey(auth.View, auth, sourceRef, targetRef, created, *foreign, &pending); err != nil {
		return table.RestoredForeignKeys{}, false, err
	}
	return pending, true, nil
}
