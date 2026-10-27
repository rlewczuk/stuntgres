package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// prepareRestoreReplayFlags sets the operation's replay flags for one
// admitted Restore statement. restoreRun.execute clears defaultReplay,
// partitionAttach and commentAuthority after execution; tableDestination
// and insertAuthority stay set until the next statement's prepare.
func prepareRestoreReplayFlags(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	replay := core.OperationReplay(op)
	replay.TableDestination = restoreTableDestination(stmt) || r.cur.attributeConstruction
	_, replay.InsertAuthority = stmt.(*parser.Insert)
	_, replay.CommentAuthority = stmt.(*parser.Comment)
	_, replay.DefaultReplay = stmt.(*parser.AlterTableDefault)
	_, replay.PartitionAttach = stmt.(*parser.AlterTableAttachPartition)
	replay.LargeObjectAuthority = false
	if selected, ok := stmt.(*parser.Select); ok {
		replay.LargeObjectAuthority = largeobject.RestoresBytes(selected)
	}
	return nil
}

// restoreTableDestination reports the creations Restore replays with
// trusted importer provenance (the tableDestination flag).
func restoreTableDestination(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.CreateTable, *parser.CreateDomain, *parser.CreateRange, *parser.CreateEnum, *parser.CreateComposite,
		*parser.CreateTrigger, *parser.CreatePolicy, *parser.CreateRule, *parser.CreateSequence, *parser.CreateView,
		*parser.CreateIndex, *parser.CreateCollation, *parser.CreateConversion, *parser.AlterConversion,
		*parser.CreateOperator, *parser.AlterOperator, *parser.CreateExtension, *parser.AlterRoutine:
		return true
	case *parser.CreateStatistics:
		return true
	case *parser.CreateDictionary, *parser.AlterDictionary, *parser.CreateConfiguration, *parser.AlterConfiguration:
		return true
	case *parser.AlterTrigger:
		return s.Action == "depends"
	case *parser.CreateQueryTable:
		return s.Form == parser.CreateMaterializedView
	}
	return false
}
