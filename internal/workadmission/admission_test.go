package workadmission

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkAdmissionContextAuthority(t *testing.T) {
	type key struct{}
	cause := errors.New("caller cause")
	deadline := time.Now().Add(time.Hour)
	parent, cancel := context.WithDeadlineCause(context.WithValue(context.Background(), key{}, "value"), deadline, cause)
	defer cancel()
	refusal := errors.New("credit refused")
	attached := With(parent, func(ctx context.Context, class Class) error {
		if class != WorkPatternMatch || ctx.Value(key{}) != "value" || ctx.Done() != parent.Done() {
			t.Fatal("admission replaced effective context authority")
		}
		if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
			t.Fatal("deadline changed", got, ok)
		}
		return refusal
	})
	if err := Admit(attached, WorkPatternMatch); err != nil {
		t.Fatal("inert policy entered", err)
	}
	if err := Admit(Activate(attached), WorkPatternMatch); err != refusal {
		t.Fatal("refusal identity changed", err)
	}
	if err := Admit(Activate(With(Activate(attached), nil)), WorkPatternMatch); err != nil {
		t.Fatal("nil policy did not disable inherited admission", err)
	}
}

func TestWorkAdmissionTerminalGrant(t *testing.T) {
	cause := errors.New("application stopped")
	parent, cancel := context.WithCancelCause(context.Background())
	active := Activate(With(parent, func(ctx context.Context, _ Class) error {
		cancel(cause)
		if context.Cause(ctx) != cause {
			t.Fatal("custom cause lost")
		}
		return nil
	}))
	if err := Admit(active, WorkRecursiveUnion); err != context.Canceled {
		t.Fatal("terminal grant authorized success", err)
	}
	if context.Cause(active) != cause || active.Done() != parent.Done() {
		t.Fatal("terminal context authority changed")
	}
	if err := Admit(Activate(With(parent, func(context.Context, Class) error {
		t.Fatal("callback entered after cancellation")
		return nil
	})), WorkRecursiveUnion); err != context.Canceled {
		t.Fatal("cancellation identity changed", err)
	}
}

func TestWorkAdmissionInactiveAllocations(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), With(context.Background(), nil)} {
		if allocations := testing.AllocsPerRun(100, func() {
			if Activate(ctx) != ctx || Admit(ctx, WorkEncodingConvert) != nil {
				t.Fatal("disabled admission changed context or result")
			}
		}); allocations != 0 {
			t.Fatal("disabled admission allocated", allocations)
		}
	}
}

func TestWorkAdmissionNestedPolicyIsInert(t *testing.T) {
	refusal := errors.New("nested refusal")
	outer := Activate(With(context.Background(), func(context.Context, Class) error {
		t.Fatal("shadowed policy entered")
		return nil
	}))
	inner := With(outer, func(context.Context, Class) error { return refusal })
	if err := Admit(inner, WorkJSONArrayCount); err != nil {
		t.Fatal("nested attachment inherited activation", err)
	}
	if err := Admit(Activate(inner), WorkJSONArrayCount); err != refusal {
		t.Fatal("nested policy lost", err)
	}
}

func TestWorkAdmissionRebaseAuthority(t *testing.T) {
	type key struct{}
	refusal := errors.New("runtime credit refused")
	runtime := Activate(With(context.WithValue(context.Background(), key{}, "runtime"), func(ctx context.Context, _ Class) error {
		if ctx.Value(key{}) != "effective" {
			t.Fatal("runtime replaced effective unrelated values")
		}
		return refusal
	}))
	effective, cancel := context.WithCancelCause(context.WithValue(context.Background(), key{}, "effective"))
	defer cancel(nil)
	rebased := Rebase(effective, runtime)
	if rebased.Done() != effective.Done() || rebased.Err() != effective.Err() {
		t.Fatal("runtime replaced effective cancellation")
	}
	if err := Admit(rebased, WorkArrayTransform); err != refusal {
		t.Fatal("active policy did not follow effective replacement", err)
	}
	cause := errors.New("effective stopped")
	cancel(cause)
	if err := Admit(rebased, WorkArrayTransform); err != context.Canceled || context.Cause(rebased) != cause {
		t.Fatal("rebased cancellation lost effective cause", err, context.Cause(rebased))
	}
	if Rebase(effective, With(context.Background(), func(context.Context, Class) error { return refusal })) != effective {
		t.Fatal("inert policy crossed runtime replacement")
	}
}

func TestWorkAdmissionDeadlineCause(t *testing.T) {
	cause := errors.New("deadline owner")
	parent, cancel := context.WithDeadlineCause(context.Background(), time.Now().Add(-time.Hour), cause)
	defer cancel()
	active := Activate(With(parent, func(context.Context, Class) error {
		t.Fatal("expired request entered callback")
		return nil
	}))
	if err := Admit(active, WorkPasswordSHA); err != context.DeadlineExceeded || context.Cause(active) != cause {
		t.Fatal("deadline cause changed", err, context.Cause(active))
	}
}

func TestWorkAdmissionSuspendedNestedBinding(t *testing.T) {
	type key struct{}
	refusal := errors.New("nested runtime refused")
	parent, cancel := context.WithCancelCause(context.WithValue(context.Background(), key{}, "source"))
	defer cancel(nil)
	active := Activate(With(parent, func(context.Context, Class) error { return refusal }))
	binding := Suspend(active)
	if binding.Done() != parent.Done() || binding.Value(key{}) != "source" || Admit(binding, WorkJSONBRecordPopulate) != nil {
		t.Fatal("nested binding changed context authority or remained active")
	}
	if err := Admit(Activate(binding), WorkJSONBRecordPopulate); err != refusal {
		t.Fatal("nested runtime did not reactivate original policy", err)
	}
	cause := errors.New("nested source stopped")
	cancel(cause)
	if binding.Err() != context.Canceled || context.Cause(binding) != cause {
		t.Fatal("suspended binding lost cancellation cause")
	}
}

func TestWorkAdmissionDelegationAllocations(t *testing.T) {
	refusal := errors.New("credit refused")
	runtime := Activate(With(context.Background(), func(context.Context, Class) error { return refusal }))
	effective := Rebase(context.Background(), runtime)
	if allocations := testing.AllocsPerRun(100, func() {
		if err := Admit(effective, WorkEncodingConvert); err != refusal {
			t.Fatal("delegation changed refusal", err)
		}
	}); allocations != 0 {
		t.Fatal("per-quantum delegation allocated", allocations)
	}
}

type incomparableAdmissionContext struct {
	context.Context
	values []int
}

func TestWorkAdmissionIncomparableContext(t *testing.T) {
	parent := incomparableAdmissionContext{Context: context.Background(), values: []int{7}}
	if Request(parent) != nil || Admit(Activate(parent), WorkPatternMatch) != nil {
		t.Fatal("inactive custom context acquired policy")
	}
	if Admit(Rebase(parent, parent), WorkPatternMatch) != nil {
		t.Fatal("inactive custom context acquired runtime policy")
	}
	refusal := errors.New("custom context refused work")
	active := Activate(With(parent, func(context.Context, Class) error { return refusal }))
	effective := Rebase(parent, active)
	if err := Admit(effective, WorkPatternMatch); err != refusal {
		t.Fatal("custom context lost runtime authority", err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := Admit(Rebase(effective, active), WorkPatternMatch); err != refusal {
			t.Fatal(err)
		}
	}); allocations != 0 {
		t.Fatal("unchanged active capability allocated", allocations)
	}
}
