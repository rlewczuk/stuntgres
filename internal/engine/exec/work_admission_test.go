package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

type workAdmissionContextHost struct {
	Host
	ctx context.Context
}

func (h workAdmissionContextHost) Context() context.Context { return h.ctx }

func TestWorkAdmissionEffectiveRuntimeContext(t *testing.T) {
	type key struct{}
	cause := errors.New("effective owner stopped")
	effective, cancel := context.WithCancelCause(context.WithValue(context.Background(), key{}, "effective"))
	defer cancel(nil)
	refusal := errors.New("credit refused")
	runtime := workadmission.Activate(workadmission.With(context.WithValue(context.Background(), key{}, "incoming"), func(ctx context.Context, _ workadmission.Class) error {
		if ctx.Value(key{}) != "effective" || ctx.Done() != effective.Done() {
			t.Fatal("admission replaced selected runtime authority")
		}
		return refusal
	}))
	evaluation := Context{EvalFrame: EvalFrame{Query: &State{ExecutionContext: effective}}}
	evaluation.setContext(runtime)
	if err := workadmission.Admit(evaluation.Context, workadmission.WorkPatternMatch); err != refusal {
		t.Fatal("execution context replacement dropped active policy", err)
	}
	cancel(cause)
	if err := workadmission.Admit(evaluation.Context, workadmission.WorkPatternMatch); err != context.Canceled || context.Cause(evaluation.Context) != cause {
		t.Fatal("effective cancellation cause lost", err)
	}
	host := workAdmissionContextHost{ctx: workadmission.With(context.Background(), func(context.Context, workadmission.Class) error {
		t.Fatal("inert host fallback activated policy")
		return nil
	})}
	evaluation.Query = &State{Source: &Source{Authority: &Authority{Host: host}}}
	evaluation.setContext(nil)
	if evaluation.Context != host.ctx || workadmission.Admit(evaluation.Context, workadmission.WorkPatternMatch) != nil {
		t.Fatal("host fallback context authority changed")
	}
}

func TestWorkAdmissionProjectedContextLifetime(t *testing.T) {
	d := transitionUnitType(t)
	value, err := sqltype.Integer(d, 7)
	if err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("later request refused")
	first := workadmission.Activate(workadmission.With(context.Background(), func(context.Context, workadmission.Class) error { return nil }))
	second := workadmission.Activate(workadmission.With(context.Background(), func(context.Context, workadmission.Class) error { return refusal }))
	stage := Stage{Source: &Source{Flat: []sqltype.Value{value}, FlatWidth: 1, FlatCount: 1, Materialized: true}, Columns: []catalog.Column{{Usage: d.Usage()}}, Retain: true}
	stage.Expressions = []expr.Expr{queryProbe(d.Usage(), func(c *Context) (sqltype.Value, error) {
		if err := workadmission.Admit(c.Context, workadmission.WorkCalendarArithmetic); err != nil {
			return sqltype.Value{}, err
		}
		return value, nil
	})}
	defer stage.Close()
	row := make([]sqltype.Value, 1)
	if present, err := stage.ProjectOrdinal(first, 0, row); err != nil || !present || !sqltype.SameDatum(row[0], value) {
		t.Fatal("first request failed", present, err)
	}
	if stage.Evaluation.Context != nil {
		t.Fatal("retained stage owns completed request context")
	}
	if _, err := stage.ProjectOrdinal(second, 0, row); err != refusal {
		t.Fatal("later request reused earlier policy", err)
	}
	if stage.Evaluation.Context != nil {
		t.Fatal("failed projection retained request context")
	}
}
