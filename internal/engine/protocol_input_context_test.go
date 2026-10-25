package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestProtocolCopyInputContext(t *testing.T) {
	t.Run("conversion_and_context_contract", func(t *testing.T) {
		e := New()
		sess := sessionStart(t, e, nil)
		deadline := time.Now().Add(time.Hour)
		parent, stopDeadline := context.WithDeadline(context.Background(), deadline)
		defer stopDeadline()
		source, cancel := context.WithCancelCause(parent)
		defer cancel(nil)
		op, _, err := sess.AcquireCommand(source)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		original, originalSource := op.Ctx, op.SourceContext
		op.CopyInput.Active.Store(true)
		op.CopyInput.Decoding.Store(true)
		defer op.StopCopyInput()
		before := op.Context()
		cause := &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
		cancel(cause)
		input := op.Context()
		if before.Err() != context.Canceled || input.Err() != nil || context.Cause(input) != nil {
			t.Fatal("input view changed an observed context or leaked its cancellation key")
		}
		select {
		case <-input.Done():
			t.Fatal("uncanceled input view returned a closed Done")
		default:
		}
		if observed, ok := input.Deadline(); !ok || !observed.Equal(deadline) {
			t.Fatal("COPY input changed its captured absolute deadline", observed, ok)
		}
		if err := e.ValidateOperation(op); err != nil {
			t.Fatal("input view lost actual operation admission", err)
		}
		typ := testUsage(e, sqltype.Int4).Type
		_, err = typ.InputWithEnvironment(input, "bad", -1, sqltype.InputEnvironment{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" {
			t.Fatal("pending SQL cancellation hid actual conversion error", err)
		}
		value, err := typ.InputWithEnvironment(input, "1", -1, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal("pending SQL cancellation prevented actual conversion", err)
		}
		integer, err := value.Integer()
		if err != nil || integer != 1 {
			t.Fatal("pending SQL cancellation changed the converted value", integer, err)
		}
		if err := op.PendingCopyInputError(); err != context.Canceled {
			t.Fatal("successful conversion lost pending cancellation", err)
		}
		op.CopyInput.Decoding.Store(false)
		if !errors.Is(e.ValidateOperation(op), context.Canceled) || op.Context().Err() != context.Canceled {
			t.Fatal("input cancellation hold escaped decoder into assembly")
		}
		op.StopCopyInput()
		if op.Ctx != original || op.SourceContext != originalSource || !errors.Is(e.ValidateOperation(op), context.Canceled) || cause.Context != "" {
			t.Fatal("input hold changed source authority or leaked past its lifetime")
		}
	})
	t.Run("ending_after_user_cancel", func(t *testing.T) {
		e := New()
		sess := sessionStart(t, e, nil)
		source, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		op, _, err := sess.AcquireCommand(source)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		op.CopyInput.Active.Store(true)
		op.CopyInput.Decoding.Store(true)
		defer op.StopCopyInput()
		cause := &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
		cancel(cause)
		input := op.Context()
		sess.RequestEnd(session.InvalidConnection)
		select {
		case <-input.Done():
		default:
			t.Fatal("session ending did not cancel held input before lease drain")
		}
		if input.Err() != context.Canceled || context.Cause(op.Ctx) != cause || sess.Check(context.Background()) == nil {
			t.Fatal("terminal input cancellation changed original cause or hid ending")
		}
	})
	t.Run("ordinary_cancellation_and_timeout", func(t *testing.T) {
		for _, cause := range []error{context.Canceled, session.StatementTimeoutCause} {
			e := New()
			sess := sessionStart(t, e, nil)
			source, cancel := context.WithCancelCause(context.Background())
			op, _, err := sess.AcquireCommand(source)
			if err != nil {
				cancel(nil)
				t.Fatal(err)
			}
			op.CopyInput.Active.Store(true)
			op.CopyInput.Decoding.Store(true)
			cancel(cause)
			input := op.Context()
			_, err = testUsage(e, sqltype.Int4).Type.InputWithEnvironment(input, "bad", -1, sqltype.InputEnvironment{})
			if !errors.Is(err, context.Canceled) || input.Err() != context.Canceled || context.Cause(input) != cause {
				t.Error("input view masked an ordinary caller or owned timeout", err)
			}
			mapped := op.CommandError(err)
			if cause == session.StatementTimeoutCause {
				var diagnostic *sqlstate.Error
				if !errors.As(mapped, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
					t.Error("owned timeout diagnostic changed during input", mapped)
				}
			} else if mapped != context.Canceled {
				t.Error("ordinary Go cancellation changed identity during input", mapped)
			}
			op.StopCopyInput()
			op.Done()
			cancel(nil)
		}
	})
}

func TestProtocolCopyStartCancellation(t *testing.T) {
	for _, simple := range []bool{false, true} {
		for _, input := range []struct {
			text, code, message, where string
		}{
			{"1\n", "57014", "canceling statement due to user request", `COPY t, line 1: "1"`},
			{"bad\n", "22P02", `invalid input syntax for type integer: "bad"`, `COPY t, line 1, column v: "bad"`},
		} {
			t.Run(fmt.Sprintf("simple=%t/%s", simple, input.code), func(t *testing.T) {
				e := New()
				session := sessionStart(t, e, nil)
				sessionSQL(t, session, "CREATE TABLE t(v INT)")
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				op, _, err := session.AcquireCommand(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				*ProtocolScopeSimpleRef(ScopeProtocol(scope)), *ProtocolScopeCommandRef(ScopeProtocol(scope)) = simple, !simple
				cause := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
				count, err := scope.CopyProtocol(statement(t, "COPY t FROM STDIN").(*parser.Copy), strings.NewReader(input.text), nil, CopyProtocolHooks{
					Start: func(CopyProtocolMetadata) error {
						// The frontend can cancel as soon as CopyInResponse is sent,
						// before the start hook returns to the engine.
						cancel(cause)
						return nil
					},
				})
				finishErr := scope.Finish(err)
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: input.code, Message: input.message, Context: input.where}
				if count != 0 || !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatalf("COPY start cancellation = count %d, error %#v (%v), want %#v; finish %v", count, diagnostic, err, want, finishErr)
				}
				if op.CopyInput.Active.Load() || op.CopyInput.Decoding.Load() || cause.Context != "" {
					t.Fatal("COPY input hold or owned diagnostic escaped its invocation")
				}
			})
		}
	}
}
