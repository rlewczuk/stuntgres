package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

func TestExecutionPauseNestedOperationIsolation(t *testing.T) {
	e := engine.New()
	pause := util.NewExecutionPause("encoding.convert")
	defer pause.Resume()
	calls := 0
	request := workadmission.With(util.WithExecutionPause(context.Background(), pause), func(context.Context, workadmission.Class) error { calls++; return nil })
	inherited := util.ActivateExecutionPause(workadmission.Activate(request))
	op, cleanup, err := enginetest.Operation(e, inherited)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if op.SourceContext != inherited {
		t.Fatal("operation lost original source authority")
	}
	// New operation capture must inherit both requests, but neither activation.
	if _, err = sqltype.ConvertBinaryEncoding(op.Context(), strings.Repeat("a", 8192), "UTF8", "LATIN1"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("nested operation invoked inherited admission", calls)
	}
	pause.Resume()
	if err = pause.Wait(context.Background()); err == nil {
		t.Fatal("metadata operation admitted closed pause")
	}
	cause := errors.New("nested child cancellation")
	op.Cancel(cause)
	if context.Cause(op.Context()) != cause || inherited.Err() != nil {
		t.Fatal("nested cancellation changed source authority")
	}
}

func TestExecutionPauseRuntimeEffectiveChildRefusal(t *testing.T) {
	e := engine.New()
	pause := util.NewExecutionPause("encoding.convert")
	defer pause.Resume()
	type valueKey struct{}
	refusal := errors.New("actual effective child refused")
	var supplied context.Context
	policy := func(ctx context.Context, class workadmission.Class) error {
		if class != workadmission.WorkEncodingConvert || ctx.Value(valueKey{}) != "child" || ctx.Done() != supplied.Done() || context.Cause(ctx) != context.Cause(supplied) {
			return errors.New("wrong work or effective child")
		}
		return refusal
	}
	request := workadmission.With(util.WithExecutionPause(context.Background(), pause), policy)
	op, cleanup, err := enginetest.Operation(e, request)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	if err = scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	child, stop := context.WithTimeout(context.WithValue(op.Context(), valueKey{}, "child"), 5*time.Second)
	defer stop()
	supplied = child
	active := scope.ActivateRuntime(child)
	done := make(chan error, 1)
	go func() {
		_, err := sqltype.ConvertBinaryEncoding(active, strings.Repeat("a", 8192), "UTF8", "LATIN1")
		done <- err
	}()
	joined := false
	defer func() {
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	if err = pause.Wait(child); err != nil {
		t.Fatal(err)
	}
	pause.Resume()
	err = <-done
	joined = true
	if err != refusal {
		t.Fatal("runtime changed effective-child refusal identity", err)
	}
	if child.Err() != nil || op.Context().Err() != nil {
		t.Fatal("work refusal manufactured cancellation")
	}
	if err = scope.Finish(refusal); err != refusal {
		t.Fatal("publication changed refusal identity", err)
	}
}
