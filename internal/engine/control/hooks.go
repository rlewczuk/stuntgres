package control

import (
	"context"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Admission is an admit entry's answer for one statement.
type Admission uint8

const (
	NotHandled Admission = iota // not this entry's statement kind
	Admitted                    // the statement goes on to execute
	Consumed                    // the entry replayed it; nothing executes
)

// rejection is the error text for a statement no admit entry handles.
const rejection = "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"

// Hook is one entry of Restore's hook table. R is the caller's run state,
// the only channel between entries. Restore runs each phase by walking the
// table in order and stops at the first error. Any field may be nil.
type Hook[R any] struct {
	Name string // diagnostics and tests only
	// Decode reads one script line comment while the script is parsed. The
	// first entry that recognizes the comment owns it.
	Decode func(run R, comment string, position parser.Position) (recognized bool, err error)
	// Before runs ahead of admission. A consumed statement skips the rest of
	// its replay.
	Before func(run R, op *session.Operation, stmt parser.Statement) (consumed bool, err error)
	// Admit is the statement allow-list. The first entry that does not answer
	// NotHandled decides; if none does, Restore rejects the statement.
	Admit func(run R, op *session.Operation, stmt parser.Statement) (Admission, error)
	// Prepare runs after admission and before execution.
	Prepare func(run R, op *session.Operation, stmt parser.Statement) error
	// After runs once the statement executed and its rows are closed.
	After func(run R, op *session.Operation, stmt parser.Statement, outcome Outcome) error
	// Finish runs once after the last statement, before publication.
	Finish func(run R, ctx context.Context, op *session.Operation) error
}

// decodeComment offers a line comment to the Decode entries in order.
func decodeComment[R any](hooks []Hook[R], run R, comment string, position parser.Position) error {
	for _, hook := range hooks {
		if hook.Decode == nil {
			continue
		}
		recognized, err := hook.Decode(run, comment, position)
		if err != nil || recognized {
			return err
		}
	}
	return nil
}

// runBefore walks the Before entries until one consumes stmt or fails.
func runBefore[R any](hooks []Hook[R], run R, op *session.Operation, stmt parser.Statement) (bool, error) {
	for _, hook := range hooks {
		if hook.Before == nil {
			continue
		}
		if consumed, err := hook.Before(run, op, stmt); err != nil || consumed {
			return consumed, err
		}
	}
	return false, nil
}

// runAdmit asks the Admit entries in order; the first answer wins.
func runAdmit[R any](hooks []Hook[R], run R, op *session.Operation, stmt parser.Statement) (Admission, error) {
	for _, hook := range hooks {
		if hook.Admit == nil {
			continue
		}
		if answer, err := hook.Admit(run, op, stmt); err != nil || answer != NotHandled {
			return answer, err
		}
	}
	return NotHandled, sqlError("0A000", rejection)
}

// runPrepare walks the Prepare entries until one fails.
func runPrepare[R any](hooks []Hook[R], run R, op *session.Operation, stmt parser.Statement) error {
	for _, hook := range hooks {
		if hook.Prepare == nil {
			continue
		}
		if err := hook.Prepare(run, op, stmt); err != nil {
			return err
		}
	}
	return nil
}

// runAfter walks the After entries until one fails.
func runAfter[R any](hooks []Hook[R], run R, op *session.Operation, stmt parser.Statement, outcome Outcome) error {
	for _, hook := range hooks {
		if hook.After == nil {
			continue
		}
		if err := hook.After(run, op, stmt, outcome); err != nil {
			return err
		}
	}
	return nil
}

// runFinish walks the Finish entries until one fails.
func runFinish[R any](ctx context.Context, hooks []Hook[R], run R, op *session.Operation) error {
	for _, hook := range hooks {
		if hook.Finish == nil {
			continue
		}
		if err := hook.Finish(run, ctx, op); err != nil {
			return err
		}
	}
	return nil
}
