package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

func TestExecutionPauseBindingIsolation(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "constructor", true: "entry_reentry"}[manual], func(t *testing.T) {
			e := engine.New()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			op, cleanup, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			pause := util.NewExecutionPause("json.array_length")
			defer pause.Resume()
			calls := 0
			request := workadmission.With(util.WithExecutionPause(op.Context(), pause), func(context.Context, workadmission.Class) error {
				calls++
				return context.Canceled
			})
			runtime := util.ActivateExecutionPause(workadmission.Activate(request))
			array := "[" + strings.Repeat("0,", 22) + "0]"
			raw := enginetest.Statement(t, "SELECT json_array_length('"+array+"')::regtype").(*parser.Select).Targets[0]
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			binder := core.NewExpressionBinder(runtime, e, sqltype.InputEnvironment{}, op, nil)
			if manual {
				binder.Ctx = runtime
			}
			binder.Query.Auth = &auth
			binder.Query.Namespace = &analyze.QueryNamespace{}
			binder.Consumer.DefaultConsumer = true
			bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("locked immutable capture", err)
			}
			literal, ok := bound.(plan.LiteralExpr)
			if !ok {
				t.Fatal("expected actual captured typed constant", bound)
			}
			oid, err := literal.Value.TypeOID()
			if err != nil || oid != 23 {
				t.Fatal("captured type identity", oid, err)
			}
			if calls != 0 {
				t.Fatal("locked nested binding invoked inherited workload policy", calls)
			}
			pause.Resume()
			if err := pause.Wait(ctx); err == nil {
				t.Fatal("binding admitted suspended runtime work")
			}
		})
	}
}
