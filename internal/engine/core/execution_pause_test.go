package core_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

type executionPauseTimeoutDefaults struct{ enabled bool }

func (d executionPauseTimeoutDefaults) SessionDefault(_ context.Context, definition settings.Definition) (string, bool, error) {
	if d.enabled && definition.Name == "statement_timeout" {
		return "1s", true, nil
	}
	return "", false, nil
}

func TestExecutionPauseOptionalEvaluationContext(t *testing.T) {
	state := exec.State{}
	evaluation := state.Evaluation(nil)
	if evaluation.Context != nil || evaluation.FunctionContext() != nil {
		t.Fatal("runtime construction invented optional context")
	}
	// Construction accepts absent cancellation authority, but a demanded
	// expression retains the existing missing-context error rather than
	// silently gaining a Background context.
	if err := exec.EvaluationContext(&evaluation); err == nil || err.Error() != "stuntgres: missing expression context" {
		t.Fatal("missing expression context contract changed", err)
	}
}

func TestExecutionPauseOperationLifecycle(t *testing.T) {
	for _, mode := range []string{"request_end", "release", "cause", "statement_deadline"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				pause := util.NewExecutionPause("encoding.convert")
				defer pause.Resume()
				source, stop := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), 5*time.Second)
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
				op, _, err := sess.AcquireCommand(source)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				catalog, frontier := e.Catalog.State, e.Visibility.CommitSeq()
				done := make(chan error, 1)
				joined := false
				defer func() {
					op.Done()
					pause.Resume()
					if !joined {
						<-done
					}
				}()
				execution := util.ActivateExecutionPause(op.Context())
				go func() { _, err := sqltype.ConvertBinaryEncoding(execution, "abc", "UTF8", "LATIN1"); done <- err }()
				if err := pause.Wait(source); err != nil {
					t.Fatal("actual provider admission", err)
				}
				sess.Mu.Lock()
				err = session.PublicationOperationLocked(e.Catalog.Domain, op)
				sess.Mu.Unlock()
				if err != nil {
					t.Fatal("live publication authority", err)
				}
				wantErr, wantCause := error(context.Canceled), error(context.Canceled)
				switch mode {
				case "request_end":
					sess.RequestEnd(session.CleanClose)
				case "release":
					op.Done()
				case "cause":
					wantCause = errors.New("operation-only cause")
					op.Cancel(wantCause)
				case "statement_deadline":
					wantErr = context.DeadlineExceeded
					wantCause = session.StatementTimeoutCause
					<-op.Context().Done()
				}
				if err := <-done; err != wantErr {
					t.Fatal("provider cancellation", err, wantErr)
				}
				joined = true
				if context.Cause(op.Context()) != wantCause || source.Err() != nil {
					t.Fatal("child ownership", context.Cause(op.Context()), source.Err())
				}
				if mode == "statement_deadline" {
					var diagnostic *sqlstate.Error
					if err := op.CommandError(wantErr); !errors.As(err, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
						t.Fatal("own timer diagnostic", err)
					}
				}
				sess.Mu.Lock()
				err = session.PublicationOperationLocked(e.Catalog.Domain, op)
				sess.Mu.Unlock()
				if err == nil {
					t.Fatal("canceled operation retained publication authority")
				}
				op.Done()
				sess.Mu.Lock()
				_, member := sess.Operations[op]
				leases, released := sess.Leases, op.Released
				sess.Mu.Unlock()
				if member || leases != 0 || !released {
					t.Fatal("operation lease retained", member, leases, released)
				}
				if e.Catalog.State != catalog || e.Visibility.CommitSeq() != frontier {
					t.Fatal("paused pure work published state")
				}
			})
		})
	}
}
