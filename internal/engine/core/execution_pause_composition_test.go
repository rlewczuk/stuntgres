package core_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestExecutionPauseStatementBudgetRefresh(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "deadline"}[expire], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				source, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				sess, err := enginetest.OpenSession(source, e, session.SessionStart{ID: 1, Defaults: executionPauseTimeoutDefaults{enabled: true}})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					sess.RequestEnd(session.CleanClose)
					if err := sess.WaitEnd(context.Background()); err != nil {
						t.Error(err)
					}
				}()
				oldPause := util.NewExecutionPause("encoding.convert")
				defer oldPause.Resume()
				op, _, err := sess.AcquireCommand(util.WithExecutionPause(source, oldPause))
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				old, parent := op.Context(), op.CommandContext
				deadline, ok := old.Deadline()
				if !ok || deadline != op.StatementDeadline || op.TimerCancel == nil {
					t.Fatal("initial timed child missing")
				}
				oldDone := make(chan error, 1)
				joined := false
				defer func() {
					op.Done()
					oldPause.Resume()
					if !joined {
						<-oldDone
					}
				}()
				go func() {
					_, err := sqltype.ConvertBinaryEncoding(util.ActivateExecutionPause(old), "abc", "UTF8", "LATIN1")
					oldDone <- err
				}()
				if err := oldPause.Wait(source); err != nil {
					t.Fatal("old child admission", err)
				}
				if err := op.RefreshStatementBudget(); err != nil {
					t.Fatal(err)
				}
				if err := <-oldDone; err != context.Canceled || context.Cause(old) != context.Canceled {
					t.Fatal("old timer cancel classification", err, context.Cause(old))
				}
				joined = true
				fresh := op.Context()
				freshDeadline, freshOK := fresh.Deadline()
				if fresh == old || op.CommandContext != parent || parent.Err() != nil || source.Err() != nil || fresh.Err() != nil || !freshOK || freshDeadline != op.StatementDeadline || freshDeadline.Before(deadline) || op.TimerCancel == nil {
					t.Fatal("refresh retained old child or changed parent authority")
				}
				// The original one-shot remains an admitted owner, not a new
				// admission event. Retire it explicitly, then scope a NEW pause
				// to work borrowing the actual refreshed effective child.
				oldPause.Resume()
				if err := oldPause.Wait(source); err != nil {
					t.Fatal("old admission lost", err)
				}
				pause := util.NewExecutionPause("encoding.convert")
				defer pause.Resume()
				execution := util.ActivateExecutionPause(util.WithExecutionPause(fresh, pause))
				type outcome struct {
					value string
					err   error
				}
				done := make(chan outcome, 1)
				freshJoined := false
				defer func() {
					op.Done()
					pause.Resume()
					if !freshJoined {
						<-done
					}
				}()
				go func() {
					value, err := sqltype.ConvertBinaryEncoding(execution, "abc", "UTF8", "LATIN1")
					done <- outcome{value, err}
				}()
				if err := pause.Wait(source); err != nil {
					t.Fatal("refreshed child admission", err)
				}
				if expire {
					<-fresh.Done()
				} else {
					pause.Resume()
				}
				out := <-done
				freshJoined = true
				if expire {
					if out.err != context.DeadlineExceeded || out.value != "" || context.Cause(fresh) != session.StatementTimeoutCause {
						t.Fatal("refreshed deadline authority", out, context.Cause(fresh))
					}
					var diagnostic *sqlstate.Error
					if err := op.CommandError(out.err); !errors.As(err, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
						t.Fatal("refreshed deadline diagnostic", err)
					}
				} else if out.err != nil || out.value != "abc" || fresh.Err() != nil {
					t.Fatal("refreshed normal work", out)
				}
				if parent.Err() != nil || source.Err() != nil {
					t.Fatal("refreshed child canceled parent")
				}
				op.Done()
				if fresh.Err() == nil {
					t.Fatal("Done retained refreshed timer child")
				}
				if !expire && context.Cause(fresh) != context.Canceled {
					t.Fatal("release invented timeout", context.Cause(fresh))
				}
				executionPauseReleased(t, e, op)
			})
		})
	}
}

func TestExecutionPauseCopyInputComposition(t *testing.T) {
	for _, mode := range []string{"request_end", "decoder_release", "statement_deadline", "caller_deadline", "ordinary", "fatal"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				duration := 5 * time.Second
				if mode == "caller_deadline" {
					duration = time.Second
				}
				source, stop := context.WithTimeout(context.Background(), duration)
				defer stop()
				sess, err := enginetest.OpenSession(source, e, session.SessionStart{ID: 1, Defaults: executionPauseTimeoutDefaults{enabled: mode == "statement_deadline"}})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					sess.RequestEnd(session.CleanClose)
					if err := sess.WaitEnd(context.Background()); err != nil {
						t.Error(err)
					}
				}()
				pause := util.NewExecutionPause("encoding.convert")
				defer pause.Resume()
				op, _, err := sess.AcquireCommand(util.WithExecutionPause(source, pause))
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				// Match the existing real protocol input-context owner tests:
				// install its actual decoder lifetime, not a replacement context.
				op.CopyInput.Active.Store(true)
				op.CopyInput.Decoding.Store(true)
				defer op.StopCopyInput()
				original := op.Ctx
				cause := error(&sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"})
				if mode == "ordinary" {
					cause = context.Canceled
				}
				if mode == "fatal" {
					cause = &sqlstate.Error{Severity: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"}
				}
				op.Cancel(cause)
				input := op.Context()
				if mode == "ordinary" || mode == "fatal" {
					if input != original || op.CopyInput.Masked != nil || input.Err() != context.Canceled || context.Cause(input) != cause {
						t.Fatal("non-user cancellation was masked")
					}
					value, err := sqltype.ConvertBinaryEncoding(util.ActivateExecutionPause(input), "abc", "UTF8", "LATIN1")
					if value != "" || err != context.Canceled {
						t.Fatal("nonmask provider cancellation", value, err)
					}
					pause.Resume()
					if err := pause.Wait(source); err == nil {
						t.Fatal("canceled provider fabricated pause admission")
					}
					op.StopCopyInput()
					op.Done()
					executionPauseReleased(t, e, op)
					return
				}
				if input == original || op.CopyInput.Masked != input || input.Err() != nil || context.Cause(input) != nil || context.Cause(original) != cause {
					t.Fatal("actual COPY projection failed to mask only pending user cancellation")
				}
				wantDeadline, wantOK := original.Deadline()
				gotDeadline, gotOK := input.Deadline()
				if gotOK != wantOK || gotDeadline != wantDeadline {
					t.Fatal("COPY projection replaced absolute deadline")
				}
				if err := core.EngineValidateOperation(e, op); err != nil {
					t.Fatal("masked decode lost effective authority", err)
				}
				sess.Mu.Lock()
				publicationErr := session.PublicationOperationLocked(e.Catalog.Domain, op)
				sess.Mu.Unlock()
				if publicationErr != context.Canceled {
					t.Fatal("masked decoding weakened publication fence", publicationErr)
				}
				type outcome struct {
					value string
					err   error
				}
				done := make(chan outcome, 1)
				joined := false
				defer func() {
					op.StopCopyInput()
					op.Done()
					pause.Resume()
					if !joined {
						<-done
					}
				}()
				go func() {
					value, err := sqltype.ConvertBinaryEncoding(util.ActivateExecutionPause(input), "abc", "UTF8", "LATIN1")
					done <- outcome{value, err}
				}()
				if err := pause.Wait(source); err != nil {
					t.Fatal("actual masked child pause admission", err)
				}
				if original.Err() != context.Canceled || input.Err() != nil {
					t.Fatal("source cancellation escaped into masked pause")
				}
				wantErr, wantCause := error(context.Canceled), error(context.Canceled)
				switch mode {
				case "request_end":
					sess.RequestEnd(session.CleanClose)
				case "decoder_release":
					op.StopCopyInput()
				case "statement_deadline":
					wantErr = context.DeadlineExceeded
					wantCause = session.StatementTimeoutCause
					<-input.Done()
				case "caller_deadline":
					wantErr = context.DeadlineExceeded
					wantCause = context.DeadlineExceeded
					<-input.Done()
				}
				out := <-done
				joined = true
				if out.err != wantErr || out.value != "" || context.Cause(input) != wantCause || context.Cause(original) != cause {
					t.Fatal("effective COPY cancellation changed authority", out, context.Cause(input), context.Cause(original))
				}
				if mode == "statement_deadline" {
					var diagnostic *sqlstate.Error
					if err := op.CommandError(out.err); !errors.As(err, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
						t.Fatal("masked deadline diagnostic", err)
					}
				}
				if mode != "caller_deadline" && source.Err() != nil {
					t.Fatal("COPY child changed live source", source.Err())
				}
				op.StopCopyInput()
				if op.CopyInput.Active.Load() || op.CopyInput.Decoding.Load() || op.CopyInput.Masked != nil || op.Context() != original {
					t.Fatal("decoder release retained masked authority")
				}
				op.Done()
				executionPauseReleased(t, e, op)
			})
		})
	}
}

func executionPauseReleased(t *testing.T, e *core.Engine, op *session.Operation) {
	t.Helper()
	sess := op.Session
	sess.Mu.Lock()
	_, member := sess.Operations[op]
	leases, released := sess.Leases, op.Released
	veto := session.PublicationOperationLocked(e.Catalog.Domain, op)
	sess.Mu.Unlock()
	if member || leases != 0 || !released || veto == nil {
		t.Fatal("released pause operation retained membership/publication", member, leases, released, veto)
	}
}
