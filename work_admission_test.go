package stuntgres

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/workadmission"
)

// API attachment mechanics only; SQL credit/lifecycle witnesses live in reference.
func TestWorkAdmissionPublicAttachment(t *testing.T) {
	type requestKey struct{}
	cause := errors.New("request stopped")
	parent, cancel := context.WithCancelCause(context.WithValue(context.Background(), requestKey{}, "request"))
	defer cancel(nil)
	refusal := errors.New("credit refused")
	var policy WorkAdmission = func(ctx context.Context, class WorkClass) error {
		if class != WorkTSVectorConvert || ctx.Value(requestKey{}) != "request" {
			t.Fatal("public policy lost request or class authority")
		}
		return refusal
	}
	attached := WithWorkAdmission(parent, policy)
	if err := workadmission.Admit(attached, WorkTSVectorConvert); err != nil {
		t.Fatal("public attachment activated policy", err)
	}
	if err := workadmission.Admit(workadmission.Activate(attached), WorkTSVectorConvert); err != refusal {
		t.Fatal("public alias changed refusal", err)
	}
	cancel(cause)
	if attached.Err() != context.Canceled || context.Cause(attached) != cause || attached.Done() != parent.Done() {
		t.Fatal("public attachment changed context authority")
	}
}
