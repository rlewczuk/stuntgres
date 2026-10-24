package session

import (
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestClassifyControl(t *testing.T) {
	const (
		already = "25001 there is already a transaction in progress"
		none    = "25P01 there is no transaction in progress"
	)
	for _, tc := range []struct {
		kind    parser.TransactionControlKind
		chain   bool
		state   TxState
		action  ControlAction
		warning string
		err     string
	}{
		{parser.KindStart, false, TxIdle, ControlBegin, "", ""},
		{parser.KindStart, false, TxImplicit, ControlPromote, "", ""},
		{parser.KindStart, false, TxActive, ControlPreflight, already, ""},
		{parser.KindStart, false, TxAborted, ControlPreflight, already, ""},
		{parser.KindCommit, false, TxIdle, ControlPreflight, none, ""},
		{parser.KindRollback, true, TxIdle, ControlPreflight, "", "25P01 ROLLBACK AND CHAIN can only be used in transaction blocks"},
		{parser.KindCommit, true, TxImplicit, ControlRefuse, "", "25P01 COMMIT AND CHAIN can only be used in transaction blocks"},
		{parser.KindRollback, false, TxImplicit, ControlEnd, none, ""},
		{parser.KindCommit, true, TxActive, ControlEnd, "", ""},
		{parser.KindRollback, false, TxAborted, ControlEnd, "", ""},
		{parser.KindPrepareTransaction, false, TxIdle, ControlWarn, none, ""},
		{parser.KindPrepareTransaction, false, TxImplicit, ControlPrepare, none, ""},
		{parser.KindPrepareTransaction, false, TxAborted, ControlPrepare, "", ""},
		{parser.KindCommitPrepared, false, TxIdle, ControlComplete, "", ""},
		{parser.KindRollbackPrepared, false, TxImplicit, ControlRefuse, "", "25001 ROLLBACK PREPARED cannot run inside a transaction block"},
		{parser.KindCommitPrepared, false, TxActive, ControlRefuse, "", "25001 COMMIT PREPARED cannot run inside a transaction block"},
	} {
		step := ClassifyControl(tc.kind, tc.chain, tc.state)
		warning, err := "", ""
		if step.Warning != nil {
			if step.Warning.Severity != "WARNING" {
				t.Fatal("warning severity", tc, step.Warning)
			}
			warning = step.Warning.Code + " " + step.Warning.Message
		}
		if step.Err != nil {
			e := step.Err.(*sqlstate.Error)
			err = e.Code + " " + e.Message
		}
		if step.Action != tc.action || warning != tc.warning || err != tc.err {
			t.Fatal(tc, step.Action, warning, err)
		}
	}
}

func TestAbortedControlAllowed(t *testing.T) {
	for _, tc := range []struct {
		stmt    parser.Statement
		allowed bool
	}{
		{&parser.SavepointControl{Kind: parser.RollbackSavepoint}, true},
		{&parser.SavepointControl{Kind: parser.CreateSavepoint}, false},
		{&parser.SavepointControl{Kind: parser.ReleaseSavepoint}, false},
		{&parser.TransactionControl{Kind: parser.KindCommit}, true},
		{&parser.TransactionControl{Kind: parser.KindRollback}, true},
		{&parser.TransactionControl{Kind: parser.KindPrepareTransaction}, true},
		{&parser.TransactionControl{Kind: parser.KindStart}, false},
		{&parser.TransactionControl{Kind: parser.KindCommitPrepared}, false},
		{&parser.TransactionControl{Kind: parser.KindRollbackPrepared}, false},
		{&parser.Select{}, false},
	} {
		if got := AbortedControlAllowed(tc.stmt); got != tc.allowed {
			t.Fatalf("%T %+v: got %v", tc.stmt, tc.stmt, got)
		}
	}
	if err := SavepointOutsideBlockError(parser.RollbackSavepoint).(*sqlstate.Error); err.Code != "25P01" || err.Message != "ROLLBACK TO SAVEPOINT can only be used in transaction blocks" {
		t.Fatal(err)
	}
}
