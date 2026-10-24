package session

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ControlAction is what a transaction control statement does in the owner's
// state. The root runs it with its own locks, preflights and order.
type ControlAction uint8

const (
	// ControlBegin starts a block from idle (START).
	ControlBegin ControlAction = iota
	// ControlPromote turns the implicit transaction into a block (START).
	ControlPromote
	// ControlPreflight checks the operation, then returns Err or emits Warning.
	ControlPreflight
	// ControlRefuse returns Err before any preflight.
	ControlRefuse
	// ControlWarn emits Warning and does nothing else.
	ControlWarn
	// ControlEnd emits Warning, if any, then commits or rolls back.
	ControlEnd
	// ControlPrepare emits Warning, if any, then prepares the block; an
	// aborted block is rolled back instead.
	ControlPrepare
	// ControlComplete commits or rolls back a prepared transaction.
	ControlComplete
)

// ControlStep is the classified outcome of one transaction control statement.
type ControlStep struct {
	Action  ControlAction
	Warning *sqlstate.Error
	Err     error
}

// ClassifyControl decides what a transaction control of kind does in state.
func ClassifyControl(kind parser.TransactionControlKind, chain bool, state TxState) ControlStep {
	switch kind {
	case parser.KindStart:
		switch state {
		case TxIdle:
			return ControlStep{Action: ControlBegin}
		case TxImplicit:
			return ControlStep{Action: ControlPromote}
		}
		warning := warningNotice("25001", "there is already a transaction in progress")
		return ControlStep{Action: ControlPreflight, Warning: &warning}
	case parser.KindPrepareTransaction:
		switch state {
		case TxIdle:
			return noTransactionStep(ControlWarn)
		case TxImplicit:
			return noTransactionStep(ControlPrepare)
		}
		return ControlStep{Action: ControlPrepare}
	case parser.KindCommitPrepared, parser.KindRollbackPrepared:
		if state != TxIdle {
			return ControlStep{Action: ControlRefuse, Err: sqlError("25001", controlWord(kind)+" PREPARED cannot run inside a transaction block")}
		}
		return ControlStep{Action: ControlComplete}
	}
	switch {
	case state == TxIdle && chain:
		return ControlStep{Action: ControlPreflight, Err: chainOutsideBlockError(kind)}
	case state == TxIdle:
		return noTransactionStep(ControlPreflight)
	case state == TxImplicit && chain:
		return ControlStep{Action: ControlRefuse, Err: chainOutsideBlockError(kind)}
	case state == TxImplicit:
		return noTransactionStep(ControlEnd)
	}
	return ControlStep{Action: ControlEnd}
}

func noTransactionStep(action ControlAction) ControlStep {
	warning := NoTransactionWarning()
	return ControlStep{Action: action, Warning: &warning}
}

func chainOutsideBlockError(kind parser.TransactionControlKind) error {
	return sqlError("25P01", controlWord(kind)+" AND CHAIN can only be used in transaction blocks")
}

func controlWord(kind parser.TransactionControlKind) string {
	if kind == parser.KindRollback || kind == parser.KindRollbackPrepared {
		return "ROLLBACK"
	}
	return "COMMIT"
}

// AbortedControlAllowed reports whether a control statement runs in an
// aborted block: ROLLBACK TO SAVEPOINT, COMMIT, ROLLBACK and PREPARE
// TRANSACTION. Every other statement is refused with AbortedError.
func AbortedControlAllowed(stmt parser.Statement) bool {
	switch control := stmt.(type) {
	case *parser.SavepointControl:
		return control.Kind == parser.RollbackSavepoint
	case *parser.TransactionControl:
		return control.Kind != parser.KindStart && control.Kind != parser.KindCommitPrepared && control.Kind != parser.KindRollbackPrepared
	}
	return false
}

// SavepointOutsideBlockError refuses a savepoint control outside a block.
func SavepointOutsideBlockError(kind parser.SavepointControlKind) error {
	word := "SAVEPOINT"
	switch kind {
	case parser.RollbackSavepoint:
		word = "ROLLBACK TO SAVEPOINT"
	case parser.ReleaseSavepoint:
		word = "RELEASE SAVEPOINT"
	}
	return sqlError("25P01", word+" can only be used in transaction blocks")
}

// AbortedError refuses a statement in an aborted transaction block.
func AbortedError() error {
	return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}

// NoTransactionWarning is the notice for an end control outside a block.
func NoTransactionWarning() sqlstate.Error {
	return warningNotice("25P01", "there is no transaction in progress")
}

func warningNotice(code, message string) sqlstate.Error {
	return sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message}
}
