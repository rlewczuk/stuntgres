package analyze

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

type workAdmissionBinderHost struct{ Host }

func (workAdmissionBinderHost) CaptureEnumOperationSafety(*sqltype.InputEnvironment) {}

func TestWorkAdmissionBinderSuspendsRuntime(t *testing.T) {
	type key struct{}
	cause := errors.New("source canceled")
	parent, cancel := context.WithCancelCause(context.WithValue(context.Background(), key{}, "source"))
	defer cancel(nil)
	refusal := errors.New("runtime refused")
	runtime := workadmission.Activate(workadmission.With(parent, func(context.Context, workadmission.Class) error { return refusal }))
	binder := NewBinder(runtime, workAdmissionBinderHost{}, sqltype.InputEnvironment{}, Expected{})
	if binder.Ctx.Done() != parent.Done() || binder.Ctx.Value(key{}) != "source" {
		t.Fatal("binding changed source context authority")
	}
	if err := workadmission.Admit(binder.Ctx, workadmission.WorkCalendarArithmetic); err != nil {
		t.Fatal("binding invoked active runtime policy", err)
	}
	if err := workadmission.Admit(workadmission.Activate(binder.Ctx), workadmission.WorkCalendarArithmetic); err != refusal {
		t.Fatal("binding discarded inert request policy", err)
	}
	cancel(cause)
	if binder.Ctx.Err() != context.Canceled || context.Cause(binder.Ctx) != cause {
		t.Fatal("binding changed original cancellation cause")
	}
}
