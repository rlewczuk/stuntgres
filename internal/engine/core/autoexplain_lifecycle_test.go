package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func TestAutoExplainLifecycle(t *testing.T) {
	t.Run("nested_pl_owners", autoExplainNestedLifecycle)
	for _, mode := range []string{"success_once", "source_close_error", "begun_cancel", "finish_publication_veto", "provider_error"} {
		t.Run(mode, func(t *testing.T) {
			e, s, provider := explainExecutionFixture(t)
			provider.declineExecution = true
			autoExplainComposedConfigure(t, s)
			op := enginetest.TransactionOperation(t, s)
			const query = "INSERT INTO executed_explain_local SELECT k FROM executed_explain_data"
			op.OriginalSQL, op.SQLAvailable = query, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			explainExecutionCleanup(t, scope)
			ctx, cancel := context.WithCancel(op.Context())
			defer cancel()
			op.Ctx = ctx
			sentinel := errors.New("actual source owner veto")
			switch mode {
			case "source_close_error":
				provider.endFailure = sentinel
			case "provider_error":
				provider.failure = sentinel
			case "begun_cancel":
				provider.cancelFetch = cancel
			}
			result, executionErr := scope.Execute(enginetest.Statement(t, query), 0, nil)
			_, _, retired := core.AutoExplainWitnessForTest(scope)
			if result.Rows != nil {
				if closeErr := result.Rows.Close(); executionErr == nil {
					executionErr = closeErr
				}
			}
			if mode == "finish_publication_veto" {
				if executionErr != nil || len(scope.Stage.Rows) == 0 {
					scope.Finish(executionErr)
					t.Fatal("no real staged work before veto", executionErr)
				}
				op.Ctx = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 2}
			}
			finishErr := scope.Finish(executionErr)
			if mode == "success_once" {
				if finishErr != nil || len(op.Notices) != 1 {
					t.Fatal("successful completion", finishErr, op.Notices)
				}
			} else {
				if finishErr == nil || len(op.Notices) != 0 {
					t.Fatal("failed owner fabricated completion", mode, finishErr, op.Notices)
				}
				if mode == "source_close_error" || mode == "provider_error" {
					if !errors.Is(finishErr, sentinel) {
						t.Fatal("original provider error replaced", finishErr)
					}
				}
				if mode == "begun_cancel" || mode == "finish_publication_veto" {
					if !errors.Is(finishErr, context.Canceled) {
						t.Fatal("original cancellation replaced", finishErr)
					}
				}
			}
			if !retired() || core.AutoExplainActiveForTest(scope) || provider.begins != provider.ends || scope.View != nil || scope.LockTicket != nil {
				t.Fatal("terminal observer/source/lock ownership leaked")
			}
			count := len(op.Notices)
			_ = scope.Finish(nil)
			if len(op.Notices) != count {
				t.Fatal("repeated Finish emitted twice")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func autoExplainProtocolCommand(t *testing.T, e *core.Engine, s *session.Session, cycle *session.ProtocolCycle, body func(*core.StatementScope) error) int {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		t.Fatal(err)
	}
	explainExecutionCleanup(t, scope)
	err = body(scope)
	if err := scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	return len(op.Notices)
}

func TestAutoExplainCompletion(t *testing.T) {
	e, s, provider := explainExecutionFixture(t)
	provider.declineExecution = true
	autoExplainComposedConfigure(t, s)
	cycle := s.NewProtocolCycle()
	command := func(body func(*core.StatementScope) error) int {
		return autoExplainProtocolCommand(t, e, s, cycle, body)
	}
	if command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("ae_completion", "SELECT k FROM executed_explain_data", nil)
		return err
	}) != 0 {
		t.Fatal("Parse logged")
	}
	if command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("ae_retained", "ae_completion", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	}) != 0 {
		t.Fatal("Bind logged")
	}
	if provider.begins != 0 {
		t.Fatal("Bind began source execution")
	}
	if command(func(scope *core.StatementScope) error {
		result, err := scope.ExecuteProtocolPortal("ae_retained", 1, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		if err == nil && (!result.Suspended || provider.begins != 1 || provider.ends != 0) {
			t.Fatal("retained producer did not suspend live source", result, provider.begins, provider.ends)
		}
		return err
	}) != 0 {
		t.Fatal("suspension fabricated completed plan")
	}
	portal := core.ProtocolPortalOf(core.PayloadOf(s).PreparedSQL.Binding("ae_retained"))
	if portal == nil || !core.AutoExplainPortalActiveForTest(portal) {
		t.Fatal("normal close witness lacks a live accepted observer")
	}
	retired := core.AutoExplainPortalRetirementForTest(portal)
	// Normal close classifies the reached partial executor as successful, but
	// only the close command's Finish can publish its completion.
	count := command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("ae_retained") })
	if count != 1 || !retired() || core.AutoExplainPortalActiveForTest(portal) || provider.begins != provider.ends {
		t.Fatal("normal retained close completion/release", count, retired(), provider.begins, provider.ends)
	}
	if command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("ae_retained") }) != 0 {
		t.Fatal("repeated portal close logged")
	}
	// Portal close retires its executor, not the extended cycle's implicit
	// transaction. Sync releases that transaction's relation locks while the
	// named prepared definition and physical session remain available for reuse.
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil || len(op.Notices) != 0 || s.TransactionState() != session.TxIdle {
		t.Fatal("protocol Sync completion/release", err, op.Notices, s.TransactionState())
	}
	enginetest.LockEmpty(t, e)
	// A second genuine suspended executor is retired by physical shutdown,
	// not by a lease return or a fabricated successful Close.
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("ae_teardown", "ae_completion", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("ae_teardown", 1, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	portal = core.ProtocolPortalOf(core.PayloadOf(s).PreparedSQL.Binding("ae_teardown"))
	if portal == nil || !core.AutoExplainPortalActiveForTest(portal) || provider.begins == provider.ends {
		t.Fatal("teardown witness lacks a live accepted executor")
	}
	retired = core.AutoExplainPortalRetirementForTest(portal)
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !retired() || s.Settings.AutoExplainLoaded() || provider.begins != provider.ends {
		t.Fatal("physical teardown retained observer/provider ownership")
	}
	enginetest.LockEmpty(t, e)
}
