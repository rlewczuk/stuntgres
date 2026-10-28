// Package workadmission owns request-scoped cooperative workload admission.
// Attached policies are inert until a known-unlocked runtime owner activates them.
package workadmission

import "context"

// Class identifies one documented semantic quantum, not a CPU or memory amount.
type Class uint8

const (
	WorkPatternNormalize Class = iota + 1
	WorkPatternMatch
	WorkJSONArrayCount
	WorkJSONObjectExpand
	WorkJSONStripNulls
	WorkJSONBContainment
	WorkJSONBObjectExpand
	WorkJSONBArrayExpand
	WorkJSONBExistence
	WorkJSONBPathDelete
	WorkJSONBPretty
	WorkArrayTransform
	WorkJSONRecordExpand
	WorkJSONBRecordPopulate
	WorkCalendarArithmetic
	WorkEncodingConvert
	WorkPasswordBcrypt
	WorkPasswordSHA
	WorkAggregateInput
	WorkRecursiveUnion
	WorkTSVectorConvert
)

// Admission grants one quantum or returns an unchanged refusal error.
// Implementations must honor ctx and be safe for concurrent calls.
type Admission func(context.Context, Class) error

type policyKey struct{}
type activeKey struct{}

type runtimePolicy struct {
	admission Admission
}

// Request returns the inert application policy to the unlocked runtime owner.
// It neither activates nor invokes the policy.
func Request(ctx context.Context) Admission {
	if ctx == nil {
		return nil
	}
	policy, _ := ctx.Value(policyKey{}).(Admission)
	return policy
}

// With attaches an inert policy, shadowing any inherited activation. A nil
// policy disables admission, including policies inherited from parent contexts.
func With(ctx context.Context, admission Admission) context.Context {
	return context.WithValue(context.WithValue(ctx, policyKey{}, admission), activeKey{}, (*runtimePolicy)(nil))
}

// Activate enables a policy on a local execution context. It changes no context
// cancellation, deadline, cause, or unrelated value authority. Without a policy
// it returns ctx unchanged and allocates nothing.
func Activate(ctx context.Context) context.Context {
	policy := Request(ctx)
	if policy == nil {
		return ctx
	}
	return ActivateRuntime(ctx, policy)
}

// Admit checks cancellation before entering the policy and again after a grant,
// preventing a callback that ignores cancellation from authorizing success.
func Admit(ctx context.Context, class Class) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if policy, _ := ctx.Value(activeKey{}).(*runtimePolicy); policy != nil {
		if err := policy.admission(ctx, class); err != nil {
			return err
		}
		return ctx.Err()
	}
	return nil
}

// Rebase transfers only an active workload policy onto the effective runtime
// context selected by an executor. The target remains the sole authority for
// cancellation, deadlines, causes and every unrelated context value.
func Rebase(ctx, runtime context.Context) context.Context {
	if runtime == nil {
		return ctx
	}
	policy, _ := runtime.Value(activeKey{}).(*runtimePolicy)
	if policy == nil {
		return ctx
	}
	if current, _ := ctx.Value(activeKey{}).(*runtimePolicy); current == policy {
		return ctx
	}
	return context.WithValue(ctx, activeKey{}, policy)
}

// Suspend preserves the attached policy but makes it inert while an existing
// nested invocation owner binds or captures metadata. Only its local unlocked
// runtime may reactivate it. All context authority remains with ctx.
func Suspend(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	if active, _ := ctx.Value(activeKey{}).(*runtimePolicy); active == nil {
		return ctx
	}
	return context.WithValue(ctx, activeKey{}, (*runtimePolicy)(nil))
}

// ActivateRuntime installs a core-owned active admission callable without
// replacing the original inert request policy. Suspending an execution and
// starting a new operation must never inherit another scope's runtime owner.
func ActivateRuntime(ctx context.Context, admission Admission) context.Context {
	return context.WithValue(ctx, activeKey{}, &runtimePolicy{admission: admission})
}
