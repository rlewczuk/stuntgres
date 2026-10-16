package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestProtocolCopyInputContext(t *testing.T) {
	t.Run("conversion_and_context_contract", func(t *testing.T) {
		e := New()
		session := sessionStart(t, e, nil)
		deadline := time.Now().Add(time.Hour)
		parent, stopDeadline := context.WithDeadline(context.Background(), deadline)
		defer stopDeadline()
		source, cancel := context.WithCancelCause(parent)
		defer cancel(nil)
		op, _, err := session.AcquireCommand(source)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		original, originalSource := op.ctx, op.sourceContext
		op.copyInput.active.Store(true)
		op.copyInput.decoding.Store(true)
		defer op.stopCopyInput()
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
		if err := e.validateOperation(op); err != nil {
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
		if err := op.pendingCopyInputError(); err != context.Canceled {
			t.Fatal("successful conversion lost pending cancellation", err)
		}
		op.copyInput.decoding.Store(false)
		if !errors.Is(e.validateOperation(op), context.Canceled) || op.Context().Err() != context.Canceled {
			t.Fatal("input cancellation hold escaped decoder into assembly")
		}
		op.stopCopyInput()
		if op.ctx != original || op.sourceContext != originalSource || !errors.Is(e.validateOperation(op), context.Canceled) || cause.Context != "" {
			t.Fatal("input hold changed source authority or leaked past its lifetime")
		}
	})
	t.Run("ending_after_user_cancel", func(t *testing.T) {
		e := New()
		session := sessionStart(t, e, nil)
		source, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		op, _, err := session.AcquireCommand(source)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		op.copyInput.active.Store(true)
		op.copyInput.decoding.Store(true)
		defer op.stopCopyInput()
		cause := &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
		cancel(cause)
		input := op.Context()
		session.RequestEnd(InvalidConnection)
		select {
		case <-input.Done():
		default:
			t.Fatal("session ending did not cancel held input before lease drain")
		}
		if input.Err() != context.Canceled || context.Cause(op.ctx) != cause || session.Check(context.Background()) == nil {
			t.Fatal("terminal input cancellation changed original cause or hid ending")
		}
	})
	t.Run("ordinary_cancellation_and_timeout", func(t *testing.T) {
		for _, cause := range []error{context.Canceled, statementTimeoutCause} {
			e := New()
			session := sessionStart(t, e, nil)
			source, cancel := context.WithCancelCause(context.Background())
			op, _, err := session.AcquireCommand(source)
			if err != nil {
				cancel(nil)
				t.Fatal(err)
			}
			op.copyInput.active.Store(true)
			op.copyInput.decoding.Store(true)
			cancel(cause)
			input := op.Context()
			_, err = testUsage(e, sqltype.Int4).Type.InputWithEnvironment(input, "bad", -1, sqltype.InputEnvironment{})
			if !errors.Is(err, context.Canceled) || input.Err() != context.Canceled || context.Cause(input) != cause {
				t.Error("input view masked an ordinary caller or owned timeout", err)
			}
			mapped := op.CommandError(err)
			if cause == statementTimeoutCause {
				var diagnostic *sqlstate.Error
				if !errors.As(mapped, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
					t.Error("owned timeout diagnostic changed during input", mapped)
				}
			} else if mapped != context.Canceled {
				t.Error("ordinary Go cancellation changed identity during input", mapped)
			}
			op.stopCopyInput()
			op.Done()
			cancel(nil)
		}
	})
}
