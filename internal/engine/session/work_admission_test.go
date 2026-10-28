package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/workadmission"
)

func TestWorkAdmissionNewOperationSourceAuthority(t *testing.T) {
	type key struct{}
	deadline := time.Now().Add(time.Hour)
	parentDeadline, stopDeadline := context.WithDeadline(context.WithValue(context.Background(), key{}, "caller"), deadline)
	defer stopDeadline()
	parent, cancel := context.WithCancelCause(parentDeadline)
	defer cancel(nil)
	refusal := errors.New("original request policy")
	request := workadmission.With(parent, func(context.Context, workadmission.Class) error { return refusal })
	// This models the actual active callable owned by a completed runtime,
	// distinct from the original request's application policy.
	effective := workadmission.ActivateRuntime(request, func(context.Context, workadmission.Class) error {
		t.Fatal("new operation borrowed old runtime owner")
		return nil
	})
	s := &Session{}
	op := s.Operation(effective, false)
	defer op.Cancel(nil)
	if op.SourceContext != effective || op.CommandContext != op.Ctx {
		t.Fatal("new operation changed exact caller source identity")
	}
	if op.Ctx.Value(key{}) != "caller" {
		t.Fatal("new operation lost unrelated source value")
	}
	if got, ok := op.Ctx.Deadline(); !ok || !got.Equal(deadline) {
		t.Fatal("new operation changed original deadline", got, ok)
	}
	if err := workadmission.Admit(op.Context(), workadmission.WorkEncodingConvert); err != nil {
		t.Fatal("new operation inherited runtime activation", err)
	}
	if err := workadmission.Admit(workadmission.Activate(op.Context()), workadmission.WorkEncodingConvert); err != refusal {
		t.Fatal("new runtime did not recover original request policy", err)
	}
	cause := errors.New("caller stopped")
	cancel(cause)
	if op.Ctx.Err() != context.Canceled || context.Cause(op.Ctx) != cause || context.Cause(op.SourceContext) != cause {
		t.Fatal("new operation changed cancellation provenance")
	}
}
