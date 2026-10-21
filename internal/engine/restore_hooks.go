package engine

import (
	"context"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// restoreAdmission is an admit hook's answer for one statement.
type restoreAdmission uint8

const (
	restoreNotHandled restoreAdmission = iota // not this entry's statement kind
	restoreAdmitted                           // the statement goes on to execute
	restoreConsumed                           // the entry replayed it; nothing executes
)

// restoreRejection is the error text for a statement no admit entry handles.
const restoreRejection = "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"

// restoreHook is one entry of Restore's hook table. Restore runs each phase
// by walking the table in order and stops at the first error. Any field may
// be nil.
type restoreHook struct {
	name string // diagnostics and tests only
	// decode reads one script line comment while the script is parsed.
	// The first entry that recognizes the comment owns it.
	decode func(r *restoreRun, comment string, position parser.Position) (recognized bool, err error)
	// before runs ahead of admission. A consumed statement skips the rest
	// of its replay.
	before func(r *restoreRun, op *session.Operation, stmt parser.Statement) (consumed bool, err error)
	// admit is the statement allow-list. The first entry that does not
	// answer restoreNotHandled decides; if none does, Restore rejects the
	// statement with restoreRejection.
	admit func(r *restoreRun, op *session.Operation, stmt parser.Statement) (restoreAdmission, error)
	// prepare runs after admission and before execution.
	prepare func(r *restoreRun, op *session.Operation, stmt parser.Statement) error
	// after runs once the statement executed and its rows are closed.
	after func(r *restoreRun, op *session.Operation, stmt parser.Statement, result Result) error
	// finish runs once after the last statement, before publication.
	finish func(r *restoreRun, ctx context.Context, op *session.Operation) error
}

// decodeComment offers a line comment to the decode entries in order.
func (r *restoreRun) decodeComment(hooks []restoreHook, comment string, position parser.Position) error {
	for _, hook := range hooks {
		if hook.decode == nil {
			continue
		}
		recognized, err := hook.decode(r, comment, position)
		if err != nil || recognized {
			return err
		}
	}
	return nil
}

// runBefore walks the before entries until one consumes stmt or fails.
func (r *restoreRun) runBefore(hooks []restoreHook, op *session.Operation, stmt parser.Statement) (bool, error) {
	for _, hook := range hooks {
		if hook.before == nil {
			continue
		}
		if consumed, err := hook.before(r, op, stmt); err != nil || consumed {
			return consumed, err
		}
	}
	return false, nil
}

// runAdmit asks the admit entries in order; the first answer wins.
func (r *restoreRun) runAdmit(hooks []restoreHook, op *session.Operation, stmt parser.Statement) (restoreAdmission, error) {
	for _, hook := range hooks {
		if hook.admit == nil {
			continue
		}
		if answer, err := hook.admit(r, op, stmt); err != nil || answer != restoreNotHandled {
			return answer, err
		}
	}
	return restoreNotHandled, sqlError("0A000", restoreRejection)
}

// runPrepare walks the prepare entries until one fails.
func (r *restoreRun) runPrepare(hooks []restoreHook, op *session.Operation, stmt parser.Statement) error {
	for _, hook := range hooks {
		if hook.prepare == nil {
			continue
		}
		if err := hook.prepare(r, op, stmt); err != nil {
			return err
		}
	}
	return nil
}

// runAfter walks the after entries until one fails.
func (r *restoreRun) runAfter(hooks []restoreHook, op *session.Operation, stmt parser.Statement, result Result) error {
	for _, hook := range hooks {
		if hook.after == nil {
			continue
		}
		if err := hook.after(r, op, stmt, result); err != nil {
			return err
		}
	}
	return nil
}

// runFinish walks the finish entries until one fails.
func (r *restoreRun) runFinish(ctx context.Context, hooks []restoreHook, op *session.Operation) error {
	for _, hook := range hooks {
		if hook.finish == nil {
			continue
		}
		if err := hook.finish(r, ctx, op); err != nil {
			return err
		}
	}
	return nil
}

// restoreHooks is Restore's hook table. Each phase walks it in order, so an
// entry's position fixes when it runs relative to the others in its phase.
var restoreHooks = []restoreHook{
	// Line comment decoders.
	{name: "typedef: domain replay records", decode: decodeRestoreDomainReplay},
	{name: "extension: owner records", decode: decodeRestoreExtensionOwners},
	{name: "view: materialized snapshots", decode: decodeRestoreMaterializedSnapshot},

	// Before admission.
	{name: "inline before", before: (*restoreRun).inlineBefore},
	{name: "table: physical NOT NULL boundary", before: beforeRestorePhysicalBoundary},
	{name: "table: physical NOT NULL constructor", before: beforeRestorePhysicalConstructor},
	{name: "table: deferred physical primary owner", before: beforeRestoreDeferredPhysicalOwner},
	{name: "table: physical import index base", before: beforeRestorePhysicalIndexBase},
	{name: "inline before 2", before: (*restoreRun).inlineBefore2},
	{name: "inheritance: inherited foreign key", before: beforeRestoreInheritedForeignKey},
	{name: "table: physical self foreign key", before: beforeRestorePhysicalSelfForeignKey},

	// Admission.
	{name: "inline admit", admit: (*restoreRun).inlineAdmit},

	// After admission, before execution.
	{name: "inheritance: generation recipe data", prepare: prepareRestoreRecipeData},
	{name: "table: NOT NULL pair reset", prepare: prepareRestoreNotNullPairReset},
	{name: "table: NOT NULL pair", prepare: prepareRestoreNotNullPair},
	{name: "inheritance: generation pair", prepare: prepareRestoreGenerationPair},
	{name: "inline prepare 2", prepare: (*restoreRun).inlinePrepare2},
	{name: "partition: CHECK replay", prepare: prepareRestorePartitionCheck},
	{name: "partition: USING INDEX replay", prepare: prepareRestorePartitionUsingIndex},
	{name: "partition: UNIQUE replay", prepare: prepareRestorePartitionUnique},

	// After execution.
	{name: "table: physical NOT NULL constructor capture", after: afterRestorePhysicalConstructor},
	{name: "table: physical foreign key provider", after: afterRestorePhysicalForeignKeyProvider},
	{name: "table: physical import index", after: afterRestorePhysicalImportIndex},
	{name: "inline after", after: (*restoreRun).inlineAfter},
	{name: "typedef: domain owner", after: afterRestoreDomainOwner},
	{name: "inheritance: historical relation", after: afterRestoreHistoricalRelation},
	{name: "table: NOT NULL origins", after: afterRestoreNotNullOrigins},
	{name: "inheritance: generation placeholders", after: afterRestoreGenerationPlaceholders},
	{name: "inheritance: inherited owner", after: afterRestoreInheritedOwner},
	{name: "inheritance: historical facts", after: afterRestoreHistoricalFacts},
	{name: "table: foreign key targets", after: afterRestoreForeignKeys},
	{name: "table: deferred default", after: afterRestoreDefault},
	{name: "inheritance: generation capture", after: afterRestoreGeneration},

	// After the last statement.
	{name: "table: physical NOT NULL boundary at end", finish: finishRestorePhysicalBoundary},
	{name: "table: foreign keys", finish: finishRestoreForeignKeys},
	{name: "inline finish", finish: (*restoreRun).inlineFinish},
}
